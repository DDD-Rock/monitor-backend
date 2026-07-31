-- 超级管理员共享的云端地图标注库。
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
