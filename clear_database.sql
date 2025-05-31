-- Clear Database Script for Information Broker
-- This script will clear all data from the database tables to demonstrate summarization functionality

-- Clear all tables (order matters for foreign key constraints)
DELETE FROM webhook_logs;
DELETE FROM discord_error_logs;
DELETE FROM summary_logs;
DELETE FROM articles;

-- Reset the sequence counters to start from 1
ALTER SEQUENCE articles_id_seq RESTART WITH 1;
ALTER SEQUENCE webhook_logs_id_seq RESTART WITH 1;
ALTER SEQUENCE discord_error_logs_id_seq RESTART WITH 1;
ALTER SEQUENCE summary_logs_id_seq RESTART WITH 1;

-- Display confirmation
SELECT 'Database cleared successfully - all tables reset!' as status;

-- Show current table counts (should all be 0)
SELECT
    'articles' as table_name,
    COUNT(*) as record_count
FROM articles
UNION ALL
SELECT
    'webhook_logs' as table_name,
    COUNT(*) as record_count
FROM webhook_logs
UNION ALL
SELECT
    'discord_error_logs' as table_name,
    COUNT(*) as record_count
FROM discord_error_logs
UNION ALL
SELECT
    'summary_logs' as table_name,
    COUNT(*) as record_count
FROM summary_logs;