-- 将鼠标跟随验证提升为高频紧急提醒：首次约 1 秒内，持续期间每 2 秒一次。

USE `autobuff_monitor`;

UPDATE `notification_rules`
SET `interval_seconds` = 2
WHERE `rule_key` = 'mouse_follow_verification';
