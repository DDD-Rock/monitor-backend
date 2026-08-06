-- 客户端模式账号授权。基础三种默认开放，跟补和监控默认关闭。
ALTER TABLE `users`
    ADD COLUMN `dead_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER `max_client_count`,
    ADD COLUMN `live_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER `dead_mode_enabled`,
    ADD COLUMN `temple_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER `live_mode_enabled`,
    ADD COLUMN `follow_heal_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER `temple_mode_enabled`,
    ADD COLUMN `monitor_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER `follow_heal_mode_enabled`;
