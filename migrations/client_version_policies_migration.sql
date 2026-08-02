USE `autobuff_monitor`;

CREATE TABLE IF NOT EXISTS `client_version_policies` (
    `platform` VARCHAR(16) NOT NULL COMMENT 'macos or windows',
    `version` VARCHAR(32) NOT NULL,
    `enabled` TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1=allowed, 0=disabled',
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`platform`, `version`),
    KEY `idx_client_version_policies_enabled` (`enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
