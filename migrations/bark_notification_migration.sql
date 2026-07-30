-- AutoBuff Bark 通知增量建表脚本
-- 适用于已执行 monitor_schema.sql 的 MySQL 8.0 数据库。

USE `autobuff_monitor`;

CREATE TABLE IF NOT EXISTS `notification_channels` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `provider` VARCHAR(32) NOT NULL,
    `secret_ciphertext` TEXT NOT NULL COMMENT '使用服务端密钥加密后的渠道凭证',
    `enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_notification_channels_user_provider` (`user_id`, `provider`),
    KEY `idx_notification_channels_enabled` (`provider`, `enabled`),
    CONSTRAINT `fk_notification_channels_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `notification_rules` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `rule_key` VARCHAR(64) NOT NULL,
    `enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `interval_seconds` INT UNSIGNED NOT NULL DEFAULT 60,
    `last_sent_at` DATETIME(3) NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_notification_rules_user_rule` (`user_id`, `rule_key`),
    KEY `idx_notification_rules_due` (`rule_key`, `enabled`, `last_sent_at`),
    CONSTRAINT `fk_notification_rules_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `notification_deliveries` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `provider` VARCHAR(32) NOT NULL,
    `event_type` VARCHAR(64) NOT NULL,
    `status` VARCHAR(16) NOT NULL,
    `error_message` VARCHAR(500) NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    KEY `idx_notification_deliveries_user_created` (`user_id`, `created_at`),
    KEY `idx_notification_deliveries_status_created` (`status`, `created_at`),
    CONSTRAINT `fk_notification_deliveries_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `notification_rule_states` (
    `user_id` BIGINT UNSIGNED NOT NULL,
    `rule_key` VARCHAR(64) NOT NULL,
    `session_id` CHAR(36) NOT NULL,
    `last_numeric_value` BIGINT NOT NULL,
    `last_changed_at` DATETIME(3) NOT NULL,
    `last_notification_at` DATETIME(3) NULL,
    `consecutive_count` INT UNSIGNED NOT NULL DEFAULT 0,
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`user_id`, `rule_key`),
    CONSTRAINT `fk_notification_rule_states_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;
