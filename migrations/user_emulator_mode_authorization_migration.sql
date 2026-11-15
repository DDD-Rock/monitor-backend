-- 为已有账号增加模拟器模式授权，默认关闭，由超级管理员按账号开启。
ALTER TABLE `users`
    ADD COLUMN `emulator_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER `monitor_mode_enabled`;
