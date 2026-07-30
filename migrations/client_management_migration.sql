-- 首页功能列表、超级管理员与多客户端管理
-- 只需在既有 autobuff_monitor 数据库执行一次。

ALTER TABLE `users`
    ADD COLUMN `is_super_admin` TINYINT UNSIGNED NOT NULL DEFAULT 0
        COMMENT '0=no, 1=yes; database-only setting'
        AFTER `status`;

ALTER TABLE `monitor_sessions`
    ADD COLUMN `client_id` VARCHAR(64) NULL
        COMMENT '客户端本机持久化标识'
        AFTER `user_id`,
    ADD COLUMN `mode` VARCHAR(24) NOT NULL DEFAULT 'dead'
        AFTER `name`,
    ADD COLUMN `running` TINYINT UNSIGNED NOT NULL DEFAULT 0
        AFTER `mode`;

-- 旧版本所有账号的默认名称可能相同，先转换成唯一的迁移占位名。
-- 客户端下一次以新版本连接时会创建并显示新的趣味名称。
UPDATE `monitor_sessions`
SET `name` = CONCAT('怀旧时光机-', LEFT(REPLACE(`id`, '-', ''), 8))
WHERE `client_id` IS NULL;

ALTER TABLE `monitor_sessions`
    ADD UNIQUE KEY `uk_monitor_sessions_user_client` (`user_id`, `client_id`),
    ADD UNIQUE KEY `uk_monitor_sessions_name` (`name`);

-- 超级管理员只能在数据库中手动授予，例如：
-- UPDATE users SET is_super_admin = 1 WHERE username = 'your_admin_username';
