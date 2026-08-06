# 云服务器部署

当前部署约定：

- Vue 静态文件：`/var/www/autobuff-monitor`
- Go 源码与环境：`/opt/autobuff-monitor/backend`
- Go 可执行文件：`/opt/autobuff-monitor/bin/autobuff-monitor`
- Go 监听：`127.0.0.1:28672`
- Nginx 公网监听：`28671`
- MySQL：同机部署时使用 `127.0.0.1:3306`
- Bark Server：公网 `29687`，Go 服务通过 `127.0.0.1:29687` 调用

## 构建方式

云服务器上没有安装 Go，二进制在开发机交叉编译后上传。服务器是
`linux/amd64`，现有二进制为静态链接：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o autobuff-monitor ./cmd/server
```

## 服务管理

```bash
sudo systemctl status autobuff-monitor
sudo systemctl restart autobuff-monitor
sudo journalctl -u autobuff-monitor -f
```

## 数据库增量

全新数据库先执行仓库内的初始化脚本：

```bash
mysql -u root -p < migrations/monitor_schema.sql
```

经验累计落库需要先建表（只需执行一次）：

```bash
mysql -u root -p autobuff_monitor < exp_gain_migration.sql
```

未建表时 Go 服务启动会因加载累计数据失败而退出。

本次多客户端与超级管理员升级还需执行一次：

```bash
mysql -u root -p autobuff_monitor < migrations/client_management_migration.sql
mysql -u root -p autobuff_monitor < migrations/user_mode_authorization_migration.sql
```

挂绳组队与角色名称功能升级还需执行：

```bash
mysql -u root -p autobuff_monitor < migrations/rope_party_migration.sql
```

先执行数据库迁移，再替换并重启后端；旧后端不认识新增客户端协议。

云端地图标注功能还需执行一次：

```bash
mysql -u root -p autobuff_monitor < migrations/cloud_maps_migration.sql
mysql -u root -p autobuff_monitor < migrations/client_version_policies_migration.sql
mysql -u root -p autobuff_monitor < migrations/invite_codes_migration.sql
```

执行邀请码迁移并部署新版后端后，旧的固定邀请码 `XIAOXIN` 不再有效；需要由
超级管理员登录网页生成新的一次性邀请码。

## 升级步骤

`/opt/autobuff-monitor/backend/.env` 是生产配置且不在版本控制里，同步源码时
必须排除它，否则 `rsync --delete` 会连同它一起删掉：

```bash
# 1. 上传到家目录暂存区（/opt 与 /var/www 属 root）
rsync -az --delete --exclude '.env' ./ ubuntu@HOST:/home/ubuntu/deploy-staging/backend/
rsync -az ./autobuff-monitor ubuntu@HOST:/home/ubuntu/deploy-staging/autobuff-monitor

# 2. 服务器上备份、同步、原子替换
sudo cp -a /opt/autobuff-monitor/bin/autobuff-monitor \
  /opt/autobuff-monitor/bin/autobuff-monitor.bak-$(date +%Y%m%d-%H%M%S)
sudo rsync -a --delete --exclude '.env' \
  /home/ubuntu/deploy-staging/backend/ /opt/autobuff-monitor/backend/
sudo install -o root -g root -m 755 \
  /home/ubuntu/deploy-staging/autobuff-monitor /opt/autobuff-monitor/bin/autobuff-monitor.new
sudo mv -f /opt/autobuff-monitor/bin/autobuff-monitor.new /opt/autobuff-monitor/bin/autobuff-monitor
sudo systemctl restart autobuff-monitor
```

直接覆盖正在运行的可执行文件会报 `Text file busy`，所以先写成 `.new` 再用
`mv` 原子改名。前端同理：先传到暂存区，再 `sudo rsync -a --delete` 进
`/var/www/autobuff-monitor`。

## Nginx

```bash
sudo nginx -t
sudo systemctl reload nginx
```

使用 `deploy/nginx-autobuff-monitor.conf` 作为站点配置。由于公网使用非标准
端口，必须用 `proxy_set_header Host $http_host` 保留 `:28671`。如果改成
`$host`，浏览器 WebSocket 的 `Origin` 会与 Go 收到的 `Host` 不一致并返回
`403`。同时需保留发布端 `Authorization` 请求头转发。

## 健康检查

```bash
curl http://127.0.0.1:28672/api/healthz
curl http://127.0.0.1:28671/api/healthz
```

生产环境的 `.env` 不进入源码或版本控制。关闭网页开放注册时，将
`ALLOW_REGISTRATION` 改为 `false` 并重启 `autobuff-monitor`。

Bark 相关环境变量：

```bash
BARK_BASE_URL=http://127.0.0.1:29687
BARK_PUBLIC_URL=http://106.52.208.129:29687
```

首次升级需执行 `migrations/bark_notification_migration.sql`。Bark DeviceKey
使用 JWT 服务端密钥派生的 AES-GCM 密钥加密保存，因此轮换 `JWT_SECRET`
之前必须同步迁移已经保存的通知凭证。

符文和鼠标跟随紧急警报静音开关首次上线前还需执行：

```bash
mysql -u root -p autobuff_monitor < migrations/urgent_alert_mute_migration.sql
```

该迁移必须先于新版后端执行，否则通知设置接口会因缺少
`urgent_alerts_muted` 字段而失败。
