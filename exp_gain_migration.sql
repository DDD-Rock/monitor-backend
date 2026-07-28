-- 经验累计（极简模式伪装为流量 / 资源包用量）持久化表。
-- 在已有库上执行本文件即可；新环境也会随 monitor_schema.sql 一并建表。

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
