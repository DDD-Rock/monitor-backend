-- 每用户客户端绑定数量限制。
-- 可重复执行；已经存在该字段时不会报错。

SET @monitor_schema = DATABASE();

SET @add_max_client_count_sql = (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE `users` ADD COLUMN `max_client_count` INT UNSIGNED NOT NULL DEFAULT 5 COMMENT ''maximum number of bound computers'' AFTER `is_super_admin`',
        'SELECT 1'
    )
    FROM information_schema.columns
    WHERE table_schema = @monitor_schema
      AND table_name = 'users'
      AND column_name = 'max_client_count'
);
PREPARE add_max_client_count_stmt FROM @add_max_client_count_sql;
EXECUTE add_max_client_count_stmt;
DEALLOCATE PREPARE add_max_client_count_stmt;

-- 只修改后续新注册账号使用的默认值；已有账号当前配置保持不变。
ALTER TABLE `users`
    MODIFY COLUMN `max_client_count` INT UNSIGNED NOT NULL DEFAULT 5
        COMMENT 'maximum number of bound computers';

-- 如需保留某个现有账号当前已经绑定的全部客户端，可在上线前按实际数量调整：
-- UPDATE users u
-- SET max_client_count = (
--     SELECT COUNT(*)
--     FROM monitor_sessions ms
--     WHERE ms.user_id = u.id AND ms.status = 1 AND ms.client_id IS NOT NULL
-- )
-- WHERE username = 'your_username';
