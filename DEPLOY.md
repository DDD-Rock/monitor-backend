# 云服务器部署

当前部署约定：

- Vue 静态文件：`/var/www/autobuff-monitor`
- Go 源码与环境：`/opt/autobuff-monitor/backend`
- Go 可执行文件：`/opt/autobuff-monitor/bin/autobuff-monitor`
- Go 监听：`127.0.0.1:28672`
- Nginx 公网监听：`28671`
- MySQL：同机部署时使用 `127.0.0.1:3306`

## 服务管理

```bash
sudo systemctl status autobuff-monitor
sudo systemctl restart autobuff-monitor
sudo journalctl -u autobuff-monitor -f
```

## Nginx

```bash
sudo nginx -t
sudo systemctl reload nginx
```

## 健康检查

```bash
curl http://127.0.0.1:28672/api/healthz
curl http://127.0.0.1:28671/api/healthz
```

生产环境的 `.env` 不进入源码或版本控制。关闭网页开放注册时，将
`ALLOW_REGISTRATION` 改为 `false` 并重启 `autobuff-monitor`。
