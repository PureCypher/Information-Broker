package main

import (
	"errors"
	"sync"
	"time"
)

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState string

const (
	// StateClosed - circuit breaker is closed, requests are allowed
	StateClosed CircuitBreakerState = "closed"
	// StateOpen - circuit breaker is open, requests are rejected
	StateOpen CircuitBreakerState = "open"
	// StateHalfOpen - circuit breaker is half-open, limited requests are allowed
	StateHalfOpen CircuitBreakerState = "half_open"
)

// CircuitBreakerConfig holds configuration for circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Number of failures to trigger open state
	SuccessThreshold int           // Number of successes to close from half-open
	Timeout          time.Duration // Time to wait before transitioning from open to half-open
	ResetTimeout     time.Duration // Time to reset failure count in closed state
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name            string
	config          CircuitBreakerConfig
	state           CircuitBreakerState
	failureCount    int
	successCount    int
	lastFailureTime time.Time
	lastSuccessTime time.Time
	mutex           sync.RWMutex
}

// CircuitBreakerManager manages multiple circuit breakers
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker
	metrics  *PrometheusMetrics
	mutex    sync.RWMutex
}

var (
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
	DefaultConfig         = CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          time.Minute * 2,
		ResetTimeout:     time.Minute * 5,
	}
)

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager() *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// SetMetrics sets the metrics instance for the circuit breaker manager
func (cbm *CircuitBreakerManager) SetMetrics(metrics *PrometheusMetrics) {
	cbm.metrics = metrics
}

// GetOrCreateBreaker gets an existing circuit breaker or creates a new one
func (cbm *CircuitBreakerManager) GetOrCreateBreaker(name string, config *CircuitBreakerConfig) *CircuitBreaker {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	if breaker, exists := cbm.breakers[name]; exists {
		return breaker
	}

	if config == nil {
		config = &DefaultConfig
	}

	breaker := &CircuitBreaker{
		name:   name,
		config: *config,
		state:  StateClosed,
	}

	cbm.breakers[name] = breaker
	return breaker
}

// GetStatus returns the status of all circuit breakers
func (cbm *CircuitBreakerManager) GetStatus() map[string]CircuitBreakerStatus {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	status := make(map[string]CircuitBreakerStatus)
	for name, breaker := range cbm.breakers {
		status[name] = breaker.GetStatus()
	}
	return status
}

// CircuitBreakerStatus represents the current status of a circuit breaker
type CircuitBreakerStatus struct {
	Name            string               `json:"name"`
	State           CircuitBreakerState  `json:"state"`
	FailureCount    int                  `json:"failure_count"`
	SuccessCount    int                  `json:"success_count"`
	LastFailureTime *time.Time           `json:"last_failure_time,omitempty"`
	LastSuccessTime *time.Time           `json:"last_success_time,omitempty"`
	Config          CircuitBreakerConfig `json:"config"`
}

// Execute executes a function with circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() error, metrics *PrometheusMetrics) error {
	if !cb.canExecute() {
		return ErrCircuitBreakerOpen
	}

	err := fn()
	if err != nil {
		cb.recordFailure(metrics)
		return err
	}

	cb.recordSuccess(metrics)
	return nil
}

// canExecute checks if the circuit breaker allows execution
func (cb *CircuitBreaker) canExecute() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		// Reset failure count if enough time has passed
		if !cb.lastFailureTime.IsZero() && now.Sub(cb.lastFailureTime) > cb.config.ResetTimeout {
			cb.failureCount = 0
		}
		return true

	case StateOpen:
		// Check if enough time has passed to transition to half-open
		if now.Sub(cb.lastFailureTime) > cb.config.Timeout {
			cb.state = StateHalfOpen
			cb.successCount = 0
			return true
		}
		return false

	case StateHalfOpen:
		return true

	default:
		return false
	}
}

// recordFailure records a failure and updates circuit breaker state
func (cb *CircuitBreaker) recordFailure(metrics *PrometheusMetrics) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	oldState := cb.state
	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.state = StateOpen
			if metrics != nil {
				metrics.RecordCircuitBreakerTrip(cb.name)
			}
		}

	case StateHalfOpen:
		cb.state = StateOpen
		cb.successCount = 0
		if metrics != nil {
			metrics.RecordCircuitBreakerTrip(cb.name)
		}
	}

	// Update metrics if state changed
	if metrics != nil && oldState != cb.state {
		metrics.UpdateCircuitBreakerState(cb.name, cb.state)
	}
}

// recordSuccess records a success and updates circuit breaker state
func (cb *CircuitBreaker) recordSuccess(metrics *PrometheusMetrics) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.lastSuccessTime = time.Now()
	oldState := cb.state

	switch cb.state {
	case StateHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			cb.state = StateClosed
			cb.failureCount = 0
			cb.successCount = 0
		}

	case StateClosed:
		// Reset failure count on success
		if cb.failureCount > 0 {
			cb.failureCount = 0
		}
	}

	// Update metrics if state changed
	if metrics != nil && oldState != cb.state {
		metrics.UpdateCircuitBreakerState(cb.name, cb.state)
	}
}

// GetStatus returns the current status of the circuit breaker
func (cb *CircuitBreaker) GetStatus() CircuitBreakerStatus {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	status := CircuitBreakerStatus{
		Name:         cb.name,
		State:        cb.state,
		FailureCount: cb.failureCount,
		SuccessCount: cb.successCount,
		Config:       cb.config,
	}

	if !cb.lastFailureTime.IsZero() {
		status.LastFailureTime = &cb.lastFailureTime
	}

	if !cb.lastSuccessTime.IsZero() {
		status.LastSuccessTime = &cb.lastSuccessTime
	}

	return status
}

// IsHealthy returns true if the circuit breaker is in a healthy state
func (cb *CircuitBreaker) IsHealthy() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state != StateOpen
}
