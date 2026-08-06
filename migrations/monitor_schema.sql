-- AutoBuff 远程纯标注监控
-- 目标数据库：MySQL 8.0+
-- 执行前可根据部署规范修改数据库名和账号授权。

CREATE DATABASE IF NOT EXISTS `autobuff_monitor`
    CHARACTER SET utf8mb4
    COLLATE utf8mb4_0900_ai_ci;

USE `autobuff_monitor`;

CREATE TABLE IF NOT EXISTS `users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `username` VARCHAR(32) NOT NULL,
    `nickname` VARCHAR(24) NOT NULL,
    `password_hash` VARCHAR(255) NOT NULL,
    `status` TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1=active, 0=disabled',
    `is_super_admin` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0=no, 1=yes; database-only setting',
    `max_client_count` INT UNSIGNED NOT NULL DEFAULT 5 COMMENT 'maximum number of bound computers',
	`dead_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1,
	`live_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1,
	`temple_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1,
	`follow_heal_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0,
	`monitor_mode_enabled` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    `last_login_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_users_username` (`username`),
    KEY `idx_users_status` (`status`)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `monitor_sessions` (
    `id` CHAR(36) NOT NULL,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `client_id` VARCHAR(64) NULL COMMENT '客户端本机持久化标识',
    `name` VARCHAR(64) NOT NULL DEFAULT '我的电脑',
	`role_name` VARCHAR(24) NOT NULL DEFAULT '' COMMENT '游戏角色名称',
    `mode` VARCHAR(24) NOT NULL DEFAULT 'dead',
    `running` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `status` TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1=active, 0=revoked',
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    `last_publish_at` DATETIME(3) NULL,
    `revoked_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_monitor_sessions_user_client` (`user_id`, `client_id`),
    UNIQUE KEY `uk_monitor_sessions_name` (`name`),
    KEY `idx_monitor_sessions_user_status` (`user_id`, `status`),
    CONSTRAINT `fk_monitor_sessions_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `rope_teams` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `leader_session_id` CHAR(36) NOT NULL,
	`created_in_game` TINYINT UNSIGNED NOT NULL DEFAULT 0,
	`boss_role_name` VARCHAR(24) NOT NULL DEFAULT '',
	`boss_cycle_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
	`boss_cycle_state` VARCHAR(16) NOT NULL DEFAULT 'idle',
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_rope_teams_user` (`user_id`),
    CONSTRAINT `fk_rope_teams_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_rope_teams_leader` FOREIGN KEY (`leader_session_id`) REFERENCES `monitor_sessions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `rope_team_members` (
    `team_id` BIGINT UNSIGNED NOT NULL,
    `session_id` CHAR(36) NOT NULL,
	`invited` TINYINT UNSIGNED NOT NULL DEFAULT 0,
	`boss_completed_cycle_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `joined` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `joined_at` DATETIME(3) NULL,
    PRIMARY KEY (`team_id`, `session_id`),
    CONSTRAINT `fk_rope_team_members_team` FOREIGN KEY (`team_id`) REFERENCES `rope_teams` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_rope_team_members_session` FOREIGN KEY (`session_id`) REFERENCES `monitor_sessions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `invite_codes` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `code` VARCHAR(32) CHARACTER SET ascii COLLATE ascii_general_ci NOT NULL,
    `created_by` BIGINT UNSIGNED NOT NULL,
    `used_by` BIGINT UNSIGNED NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `expires_at` DATETIME(3) NOT NULL,
    `used_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_invite_codes_code` (`code`),
    KEY `idx_invite_codes_validity` (`used_at`, `expires_at`),
    KEY `idx_invite_codes_created_at` (`created_at`),
    CONSTRAINT `fk_invite_codes_creator`
        FOREIGN KEY (`created_by`) REFERENCES `users` (`id`),
    CONSTRAINT `fk_invite_codes_consumer`
        FOREIGN KEY (`used_by`) REFERENCES `users` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `client_version_policies` (
    `platform` VARCHAR(16) NOT NULL COMMENT 'macos or windows',
    `version` VARCHAR(32) NOT NULL,
    `enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1=allowed, 0=disabled',
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`platform`, `version`),
    KEY `idx_client_version_policies_enabled` (`enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `notification_channels` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `provider` VARCHAR(32) NOT NULL,
    `secret_ciphertext` TEXT NOT NULL COMMENT '使用服务端密钥加密后的渠道凭证',
    `enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1,
    `urgent_alerts_muted` TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '1=符文和鼠标跟随紧急警报全程静音',
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

CREATE TABLE IF NOT EXISTS `exp_gain_stats` (
    `user_id` BIGINT UNSIGNED NOT NULL,
    `total_gained` BIGINT NOT NULL DEFAULT 0 COMMENT '跨启动总累计经验获取量，可手动清零',
    `daily_gained` BIGINT NOT NULL DEFAULT 0 COMMENT '当日累计，按 Asia/Shanghai 自然日',
    `daily_date` DATE NOT NULL COMMENT '当日用量所属业务日（上海时区）',
    `last_current_exp` BIGINT NULL COMMENT '上次采样的 currentEXP，用于算正增量',
    `session_id` CHAR(36) NULL COMMENT '最近一次采样所属会话',
    `last_sampled_at` DATETIME(3) NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`user_id`),
    CONSTRAINT `fk_exp_gain_stats_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `exp_gain_samples` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `gained` BIGINT NOT NULL COMMENT '该采样间隔内的正增量',
    `sampled_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_exp_gain_samples_user_sampled` (`user_id`, `sampled_at`),
    CONSTRAINT `fk_exp_gain_samples_user`
        FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `cloud_maps` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `map_name` VARCHAR(128) NOT NULL,
    `map_json` MEDIUMBLOB NOT NULL,
    `uploaded_by` BIGINT UNSIGNED NOT NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_cloud_maps_name` (`map_name`),
    KEY `idx_cloud_maps_updated_at` (`updated_at`),
    CONSTRAINT `fk_cloud_maps_uploader`
        FOREIGN KEY (`uploaded_by`) REFERENCES `users` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
