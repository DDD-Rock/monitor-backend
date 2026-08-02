-- 为符文和鼠标跟随紧急警报增加账号级静音设置；可重复执行。
USE `autobuff_monitor`;

SET @monitor_schema = DATABASE();
SET @add_urgent_alerts_muted_sql = (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE `notification_channels` ADD COLUMN `urgent_alerts_muted` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''1=符文和鼠标跟随紧急警报全程静音'' AFTER `enabled`',
        'SELECT 1'
    )
    FROM information_schema.columns
    WHERE table_schema = @monitor_schema
      AND table_name = 'notification_channels'
      AND column_name = 'urgent_alerts_muted'
);
PREPARE add_urgent_alerts_muted_stmt FROM @add_urgent_alerts_muted_sql;
EXECUTE add_urgent_alerts_muted_stmt;
DEALLOCATE PREPARE add_urgent_alerts_muted_stmt;
