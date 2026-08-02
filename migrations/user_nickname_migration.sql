-- 为既有账号增加公开显示昵称；可重复执行。
SET @monitor_schema = DATABASE();

SET @add_nickname_sql = (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE `users` ADD COLUMN `nickname` VARCHAR(24) NULL AFTER `username`',
        'SELECT 1'
    )
    FROM information_schema.columns
    WHERE table_schema = @monitor_schema
      AND table_name = 'users'
      AND column_name = 'nickname'
);
PREPARE add_nickname_stmt FROM @add_nickname_sql;
EXECUTE add_nickname_stmt;
DEALLOCATE PREPARE add_nickname_stmt;

UPDATE `users`
SET `nickname` = LEFT(`username`, 24)
WHERE `nickname` IS NULL OR TRIM(`nickname`) = '';

ALTER TABLE `users`
    MODIFY COLUMN `nickname` VARCHAR(24) NOT NULL;
