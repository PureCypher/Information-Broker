package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"information-broker/config"

	"github.com/lib/pq"
)

// ClusteringScheduler periodically embeds recent article summaries and
// assigns them to story clusters, backing the digest feature's cross-feed
// "important" bucket. It never runs concurrently with active summarization
// -- see isIdle -- so it doesn't compete for Ollama capacity.
//
// Embeds the Ollama-generated summary, not the raw title: a live threshold
// spot-check found title-only embeddings unreliable on this corpus (CVE
// advisory titles share so much boilerplate -- "... Elevation of Privilege
// Vulnerability" -- that different vulnerabilities scored MORE similar,
// 0.85+, than genuine cross-outlet duplicates of the same story, 0.67-0.74).
// Summaries are prose describing the actual content, not a formulaic header.
type ClusteringScheduler struct {
	db         *sql.DB
	config     *config.Config
	httpClient *http.Client
	summarizer *SummarizationScheduler
}

// NewClusteringScheduler creates a new story-clustering scheduler.
func NewClusteringScheduler(db *sql.DB, cfg *config.Config, summarizer *SummarizationScheduler) *ClusteringScheduler {
	return &ClusteringScheduler{
		db:         db,
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.OLLAMA.Timeout},
		summarizer: summarizer,
	}
}

// Start begins the ticker-driven background loop. Blocks until ctx is done.
func (c *ClusteringScheduler) Start(ctx context.Context) {
	log.Printf("Starting story-clustering scheduler (interval: %v)", c.config.Clustering.Interval)
	ticker := time.NewTicker(c.config.Clustering.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Story-clustering scheduler stopping")
			return
		case <-ticker.C:
			c.runCycle(ctx)
		}
	}
}

// isIdleFromStats reports whether the summarization scheduler is idle, given
// its GetStats() snapshot -- extracted as a pure function so the decision
// logic is testable without a real SummarizationScheduler.
func isIdleFromStats(stats map[string]interface{}) bool {
	depth, _ := stats["queue_depth"].(int)
	current, _ := stats["current_request"].(bool)
	return depth == 0 && !current
}

func (c *ClusteringScheduler) isIdle() bool {
	return isIdleFromStats(c.summarizer.GetStats())
}

// runCycle runs one embed-then-cluster pass, skipping entirely if
// summarization is active this tick.
func (c *ClusteringScheduler) runCycle(ctx context.Context) {
	if !c.isIdle() {
		log.Println("Story-clustering: summarization active, skipping this cycle")
		return
	}

	if err := c.embedBatch(ctx); err != nil {
		log.Printf("Story-clustering: embed batch failed: %v", err)
		return
	}
	if err := c.clusterBatch(ctx); err != nil {
		log.Printf("Story-clustering: cluster batch failed: %v", err)
	}
}

// embedBatch embeds up to BatchSize summaries in the clustering window that
// have a real summary but no embedding yet. Articles without a summary yet
// (summarization is async, queued separately) are skipped -- not embedded
// via a title fallback -- and picked up automatically once summarized, on
// a later cycle. Articles whose summarization genuinely failed (stored as
// the literal fallback string "summary unavailable") are excluded too,
// since embedding that identical string would falsely cluster every
// failed-summary article together regardless of actual topic.
func (c *ClusteringScheduler) embedBatch(ctx context.Context) error {
	since := time.Now().Add(-time.Duration(c.config.Clustering.WindowDays) * 24 * time.Hour)

	rows, err := c.db.QueryContext(ctx, `
		SELECT id, summary FROM articles
		WHERE publish_date >= $1 AND summary_embedding IS NULL
		  AND summary IS NOT NULL AND summary <> 'summary unavailable'
		ORDER BY publish_date DESC LIMIT $2`,
		since, c.config.Clustering.BatchSize)
	if err != nil {
		return err
	}
	type idSummary struct {
		id      int64
		summary string
	}
	var toEmbed []idSummary
	for rows.Next() {
		var it idSummary
		if err := rows.Scan(&it.id, &it.summary); err != nil {
			log.Printf("Story-clustering: embed batch scan error: %v", err)
			continue
		}
		toEmbed = append(toEmbed, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range toEmbed {
		emb, err := fetchEmbedding(ctx, c.httpClient, c.config.OLLAMA.URL, c.config.Clustering.EmbedModel, it.summary)
		if err != nil {
			log.Printf("Story-clustering: embedding failed for article %d: %v", it.id, err)
			continue
		}
		if _, err := c.db.ExecContext(ctx,
			`UPDATE articles SET summary_embedding = $1 WHERE id = $2`,
			pq.Array(emb), it.id,
		); err != nil {
			log.Printf("Story-clustering: failed to store embedding for article %d: %v", it.id, err)
		}
	}
	return nil
}

// clusterBatch assigns every embedded-but-unclustered article in the window
// to the most similar existing cluster seed, or seeds a new cluster.
func (c *ClusteringScheduler) clusterBatch(ctx context.Context) error {
	since := time.Now().Add(-time.Duration(c.config.Clustering.WindowDays) * 24 * time.Hour)

	seedRows, err := c.db.QueryContext(ctx, `
		SELECT id, summary_embedding FROM articles
		WHERE publish_date >= $1 AND story_cluster_id = id`,
		since)
	if err != nil {
		return err
	}
	seeds := map[int64][]float32{}
	for seedRows.Next() {
		var id int64
		var emb []float32
		if err := seedRows.Scan(&id, pq.Array(&emb)); err != nil {
			log.Printf("Story-clustering: seed scan error: %v", err)
			continue
		}
		seeds[id] = emb
	}
	seedRows.Close()
	if err := seedRows.Err(); err != nil {
		return err
	}

	unclusteredRows, err := c.db.QueryContext(ctx, `
		SELECT id, summary_embedding FROM articles
		WHERE publish_date >= $1 AND story_cluster_id IS NULL AND summary_embedding IS NOT NULL`,
		since)
	if err != nil {
		return err
	}
	type idEmbedding struct {
		id  int64
		emb []float32
	}
	var toCluster []idEmbedding
	for unclusteredRows.Next() {
		var it idEmbedding
		if err := unclusteredRows.Scan(&it.id, pq.Array(&it.emb)); err != nil {
			log.Printf("Story-clustering: unclustered scan error: %v", err)
			continue
		}
		toCluster = append(toCluster, it)
	}
	unclusteredRows.Close()
	if err := unclusteredRows.Err(); err != nil {
		return err
	}

	for _, it := range toCluster {
		clusterID, ok := assignCluster(it.emb, seeds, c.config.Clustering.SimilarityThreshold)
		if !ok {
			clusterID = it.id
			seeds[it.id] = it.emb // available as a seed for the rest of this batch
		}
		if _, err := c.db.ExecContext(ctx,
			`UPDATE articles SET story_cluster_id = $1 WHERE id = $2`,
			clusterID, it.id,
		); err != nil {
			log.Printf("Story-clustering: failed to store cluster assignment for article %d: %v", it.id, err)
		}
	}
	return nil
}
