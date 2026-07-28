# AutoBuff Monitor Server

AutoBuff 远程纯标注监控的 Go 服务端。它负责账号注册登录、监控会话和预览链接管理，以及 AutoBuff 到网页之间的 WebSocket 实时转发。

## 本地配置

复制 `.env.example` 中的变量到运行环境，至少设置：

- `DATABASE_DSN`
- `JWT_SECRET`
- `PUBLIC_BASE_URL`

执行工作区根目录的 `monitor_schema.sql` 创建 MySQL 数据库表。

## 启动

```bash
go mod download
set -a
source .env
set +a
go run ./cmd/server
```

服务默认只监听 `127.0.0.1:28672`，由 Nginx 的公网端口 `28671` 代理。

当前 AutoBuff 默认连接 `http://106.52.208.129:28671`。这是开发联调地址；上线前应切换到 HTTPS/WSS。

## 开关注册

```text
ALLOW_REGISTRATION=true
```

改成 `false` 并重启服务后，注册接口会返回 `403`，已有账号仍可登录。

网页注册还必须提交 `inviteCode`。当前固定邀请码为 `XIAOXIN`，服务端会
忽略首尾空格和大小写；缺少或错误的邀请码统一返回 `403`。

## API

- `POST /api/auth/register`（需要邀请码）
- `POST /api/auth/login`
- `GET /api/auth/me`
- `POST /api/monitor/sessions`
- `GET /api/monitor/sessions/current`
- `DELETE /api/monitor/sessions/current`
- `GET /api/healthz`
- `GET /ws/device?session_id=...`
- `GET /ws/view?token=...`
- `GET /api/preview/notifications/bark`
- `PUT /api/preview/notifications/exp-stalled`
- `PUT /api/preview/notifications/rune-alert`
- `PUT /api/preview/notifications/zone-breach`
- `GET /api/preview/exp-gain`
- `POST /api/preview/exp-gain/reset-total`

发布端支持 `map`、`frame`、`status`、`exp`、`rune` 和 `zone` 六类消息。`exp`
包含当前 EXP、经验百分比、识别置信度、状态与识别时间。`rune` 包含符文提示是否
出现、识别置信度和识别时间。`zone` 包含角色是否离开安全区，以及归一化的安全区
矩形（左上角原点，`x/y/width/height` 都在 0~1，网页据此画框）；矩形为空表示
本机没有配置安全区。

服务端还会根据 EXP 正增量汇总 `gain` 消息（设备端不能伪造）：
`inflow10m`（近 10 分钟）、`outflow1h`（近 1 小时）、`totalUsage`（跨启动总累计，
可手动清零）、`dailyUsage`（北京时间当日累计，每天 0 点清零）。累计与近 1 小时
采样每 15 秒落库，进程退出前也会再刷一次。升级已有库时执行根目录
`exp_gain_migration.sql`。

`rune` 和 `zone` 都在状态翻转时立刻上报，状态不变时每 3 秒心跳重发一次，
服务端据此判断数据是否新鲜。所有消息都会进入查看端首次连接时的最新快照。

## 推送规则

| 规则 key | 说明 | 间隔 |
| --- | --- | --- |
| `exp_stalled` | 经验持续无增长 | 10–86400 秒，可配置 |
| `rune_alert` | 画面出现紫色符文提示 | 固定 5 秒 |
| `zone_breach` | 角色离开安全区 | 固定 5 秒 |

每条规则都有独立开关。`rune_alert` 和 `zone_breach` 只在上报处于 12 秒新鲜度
窗口内时才推送，客户端掉线后最后一次告警会很快过期，不会无限推送。

`exp_minute`（每分钟经验推送）已下线，升级时执行根目录的
`zone_breach_migration.sql` 清掉残留规则行。
