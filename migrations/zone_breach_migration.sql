-- 离开安全区报警上线 + 每分钟经验推送下线。
--
-- notification_rules.rule_key 是 VARCHAR(64)，没有枚举约束，
-- 所以新规则 zone_breach 会在用户第一次打开开关时自动写入，无需建表。
-- 这里只需要清掉已经下线的 exp_minute 规则，避免残留行占着唯一键
-- 并在 notification_deliveries 里继续产生看不懂的历史类型。

DELETE FROM `notification_rules` WHERE `rule_key` = 'exp_minute';
DELETE FROM `notification_rule_states` WHERE `rule_key` = 'exp_minute';

-- 投递记录保留作为历史审计，不删除：
-- SELECT COUNT(*) FROM notification_deliveries WHERE event_type = 'exp_minute';
