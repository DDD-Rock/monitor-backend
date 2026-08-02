-- 神殿挂绳组队：角色名称、队伍配置与入队状态。

ALTER TABLE `monitor_sessions`
    ADD COLUMN `role_name` VARCHAR(24) NOT NULL DEFAULT ''
        COMMENT '游戏角色名称'
        AFTER `name`;

CREATE TABLE `rope_teams` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `leader_session_id` CHAR(36) NOT NULL,
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_rope_teams_user` (`user_id`),
    CONSTRAINT `fk_rope_teams_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_rope_teams_leader` FOREIGN KEY (`leader_session_id`) REFERENCES `monitor_sessions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE `rope_team_members` (
    `team_id` BIGINT UNSIGNED NOT NULL,
    `session_id` CHAR(36) NOT NULL,
    `joined` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `joined_at` DATETIME(3) NULL,
    PRIMARY KEY (`team_id`, `session_id`),
    CONSTRAINT `fk_rope_team_members_team` FOREIGN KEY (`team_id`) REFERENCES `rope_teams` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_rope_team_members_session` FOREIGN KEY (`session_id`) REFERENCES `monitor_sessions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB;
