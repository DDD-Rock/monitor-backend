SET @monitor_schema = DATABASE();

SET @drop_preview_index_sql = (
    SELECT IF(
        COUNT(*) > 0,
        'ALTER TABLE `monitor_sessions` DROP INDEX `uk_monitor_sessions_preview_token`',
        'SELECT 1'
    )
    FROM information_schema.statistics
    WHERE table_schema = @monitor_schema
      AND table_name = 'monitor_sessions'
      AND index_name = 'uk_monitor_sessions_preview_token'
);
PREPARE drop_preview_index_stmt FROM @drop_preview_index_sql;
EXECUTE drop_preview_index_stmt;
DEALLOCATE PREPARE drop_preview_index_stmt;

SET @drop_preview_column_sql = (
    SELECT IF(
        COUNT(*) > 0,
        'ALTER TABLE `monitor_sessions` DROP COLUMN `preview_token_hash`',
        'SELECT 1'
    )
    FROM information_schema.columns
    WHERE table_schema = @monitor_schema
      AND table_name = 'monitor_sessions'
      AND column_name = 'preview_token_hash'
);
PREPARE drop_preview_column_stmt FROM @drop_preview_column_sql;
EXECUTE drop_preview_column_stmt;
DEALLOCATE PREPARE drop_preview_column_stmt;
