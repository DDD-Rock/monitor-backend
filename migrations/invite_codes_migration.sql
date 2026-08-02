-- 超级管理员生成的一次性限时注册邀请码。
-- 可重复执行；MySQL 8.0+。

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
