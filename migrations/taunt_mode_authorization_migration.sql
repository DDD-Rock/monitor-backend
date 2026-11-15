-- 挑衅模式需要单独授权：已有账号和新注册账号默认均不开通。
ALTER TABLE `users`
    ADD COLUMN `taunt_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER `monitor_mode_enabled`;
