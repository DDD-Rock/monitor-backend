ALTER TABLE `rope_teams`
    ADD COLUMN `boss_role_name` VARCHAR(24) NOT NULL DEFAULT '' AFTER `created_in_game`,
    ADD COLUMN `boss_cycle_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER `boss_role_name`,
    ADD COLUMN `boss_cycle_state` VARCHAR(16) NOT NULL DEFAULT 'idle' AFTER `boss_cycle_id`;

ALTER TABLE `rope_team_members`
    ADD COLUMN `boss_completed_cycle_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER `invited`;
