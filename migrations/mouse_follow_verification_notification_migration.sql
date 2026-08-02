-- 为已绑定 Bark 的用户默认开启「鼠标跟随验证」紧急推送。
-- 新绑定 Bark 的用户由服务端 SaveBark 流程自动创建同一规则。

USE `autobuff_monitor`;

INSERT INTO `notification_rules` (
    `user_id`,
    `rule_key`,
    `enabled`,
    `interval_seconds`,
    `last_sent_at`
)
SELECT
    `user_id`,
    'mouse_follow_verification',
    1,
    2,
    NULL
FROM `notification_channels`
WHERE `provider` = 'bark' AND `enabled` = 1
ON DUPLICATE KEY UPDATE
    `interval_seconds` = VALUES(`interval_seconds`);
