-- 挂绳组队运行状态已迁移到 Go 进程内存。
-- 保留 monitor_sessions.role_name 作为低频角色名配置。

DROP TABLE IF EXISTS `rope_join_receipts`;
DROP TABLE IF EXISTS `rope_team_members`;
DROP TABLE IF EXISTS `rope_teams`;
