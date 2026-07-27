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

发布端支持 `map`、`frame`、`status`、`exp` 和 `rune` 五类消息。`exp` 包含当前
EXP、经验百分比、识别置信度、状态与识别时间。`rune` 包含符文提示是否出现、
识别置信度和识别时间，客户端在状态翻转时立刻上报，状态不变时每 3 秒心跳重发
一次。两者都会进入查看端首次连接时的最新快照。
