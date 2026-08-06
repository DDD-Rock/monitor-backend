# AutoBuff Monitor Server

桌面客户端登录时上报平台和版本。服务端会自动登记新版本，超级管理员可在网页端启用或禁用指定的 macOS、Windows 版本；禁用版本会被拒绝登录并提示更新。

注册邀请码固定为 6 位大写字母和数字；旧的长邀请码不再接受，超级管理员可在网页端删除邀请码记录。

AutoBuff 远程控制与纯标注监控的 Go 服务端。一个账号可以登录多台客户端，每台
客户端拥有独立监控通道和全局唯一的趣味名称。

## 本地配置

复制 `.env.example` 中的变量到运行环境，至少设置：

- `DATABASE_DSN`
- `JWT_SECRET`
- `JWT_TTL`（默认 `168h`，即 7 天）
- `PUBLIC_BASE_URL`

执行仓库内的 `migrations/monitor_schema.sql` 创建 MySQL 数据库表：

```bash
mysql -u root -p < migrations/monitor_schema.sql
```

从旧版预览 Token 模式升级时，先执行
`migrations/account_monitor_migration.sql`。

升级已有账号版数据库时，还需执行：

```bash
mysql -u root -p autobuff_monitor < migrations/client_management_migration.sql
mysql -u root -p autobuff_monitor < migrations/user_mode_authorization_migration.sql
```

超级管理员只能直接在数据库中授予：

```sql
UPDATE users SET is_super_admin = 1 WHERE username = 'your_admin_username';
```

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

网页注册还必须提交 `inviteCode`。邀请码由超级管理员在网页“注册邀请码”页面
生成，每个邀请码只能成功注册一次，并且只能在设定的有效期内使用；默认有效期
为 30 分钟。服务端忽略邀请码首尾空格和大小写。

注册时还必须填写 1–24 个字符的昵称。用户名只用于登录，网页和客户端公开
展示昵称。既有数据库升级时执行 `migrations/user_nickname_migration.sql`。

## API

- `POST /api/auth/register`（需要邀请码）
- `POST /api/auth/login`
- `GET /api/auth/me`
- `GET /api/clients`
- `POST /api/clients/bind`（登录后的软件登记本机身份）
- `GET /api/clients/authorization?client_id=...`（客户端定期检查授权状态）
- `DELETE /api/clients/{sessionId}`（登录用户解绑自己的客户端）
- `GET /api/admin/users`（超级管理员）
- `PATCH /api/admin/users/{id}/status`（超级管理员）
- `PUT /api/admin/users/{id}/password`（超级管理员）
- `GET /api/admin/users/{id}/clients`（超级管理员查看用户已绑定客户端）
- `DELETE /api/admin/users/{id}/clients/{sessionId}`（超级管理员解绑客户端）
- `PATCH /api/admin/users/{id}/client-limit`（超级管理员设置客户端数量上限）
- `GET /api/admin/invite-codes`（超级管理员查看最近生成的邀请码）
- `POST /api/admin/invite-codes`（超级管理员生成一次性限时邀请码）
- `GET /api/admin/maps`（登录用户浏览云端地图库元数据）
- `POST /api/admin/maps`（超级管理员上传/更新地图标注）
- `GET /api/admin/maps/{id}`（登录用户按需下载单张地图）
- `DELETE /api/admin/maps/{id}`（超级管理员删除云端地图）
- `GET /api/healthz`
- `GET /ws/device?client_id=...`（客户端设备与控制通道）
- `GET /ws/clients?access_token=...`（网页客户端管理通道）
- `GET / PUT / DELETE /api/rope-team`（每个账号唯一的挂绳队伍；删除时通知在线队长退出队伍）
- `GET /ws/view?access_token=...`（账号当前唯一活跃会话的监控通道；客户端变更时自动切换）
- `GET /api/notifications/bark`
- `PUT /api/notifications/exp-stalled`
- `PUT /api/notifications/rune-alert`
- `PUT /api/notifications/zone-breach`
- `GET /api/monitor/exp-gain`
- `POST /api/monitor/exp-gain/reset-total`

除注册、登录和健康检查外，以上 HTTP 接口均要求账号登录。

发布端支持 `map`、`frame`、`status`、`client_state`、`exp`、`rune` 和 `zone`
消息。`client_state` 同步当前模式和开始/停止状态；网页通过 WebSocket 下发
`start` / `stop` 指令。`exp`
包含当前 EXP、经验百分比、识别置信度、状态与识别时间。`rune` 包含符文提示是否
出现、识别置信度和识别时间。`zone` 包含角色是否离开安全区，以及归一化的安全区
矩形（左上角原点，`x/y/width/height` 都在 0~1，网页据此画框）；矩形为空表示
本机没有配置安全区。

服务端还会根据 EXP 正增量汇总 `gain` 消息（设备端不能伪造）：
`inflow10m`（近 10 分钟）、`outflow1h`（近 1 小时）、`totalUsage`（跨启动总累计，
可手动清零）、`dailyUsage`（北京时间当日累计，每天 0 点清零）。累计与近 1 小时
采样每 15 秒落库，进程退出前也会再刷一次。升级已有库时执行仓库根目录
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

`exp_minute`（每分钟经验推送）已下线，升级时执行
`migrations/zone_breach_migration.sql` 清掉残留规则行。
