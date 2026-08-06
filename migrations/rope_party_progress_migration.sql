ALTER TABLE `rope_teams`
    ADD COLUMN `created_in_game` TINYINT UNSIGNED NOT NULL DEFAULT 0
        AFTER `leader_session_id`;

ALTER TABLE `rope_team_members`
    ADD COLUMN `invited` TINYINT UNSIGNED NOT NULL DEFAULT 0
        AFTER `session_id`;
