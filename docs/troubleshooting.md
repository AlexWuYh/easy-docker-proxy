# 故障排查

## 先确认服务状态

```bash
docker compose --env-file .env -f deploy/docker-compose.yaml ps
docker compose --env-file .env -f deploy/docker-compose.yaml logs --tail=200 proxy
curl -fsS http://127.0.0.1:5001/healthz
```

## 常见问题

### `/v2/` 返回 404

检查请求 Host 是否在 registry 的 `hosts` 中，或路径是否包含已配置的 `path_prefixes`。若希望未知 Host 不回落，设置 `default: ""`；若使用 mirrors，请确认 mirrors 指向入口域名而不是上游域名。

### 401 或 Docker login 失败

确认 `pull_auth.mode` 与客户端是否匹配，并使用安全的密码输入方式：

```bash
printf '%s' "$PROXY_PULL_PASSWORD" | docker login reg.example.com \
  --username "$PROXY_PULL_USER" --password-stdin
```

Web 账号和 Docker 拉取账号是两套独立账号。上游 Token 则使用对应 registry 的 `*_USER` / `*_TOKEN` 环境变量。

### ACL、限流或统计中的客户端 IP 不正确

只检查 `trusted_proxies` 是否包含真实 Caddy/Nginx peer。不要加入 Docker gateway 或整个私网段。修改后重载配置并重新验证反代出口地址。

### 上游返回 502

检查 `upstream_allowlist`、上游 DNS/TLS 和容器网络连通性。服务端日志会记录详细原因，API 只返回稳定的通用错误。

### Compose 网络冲突

默认网络为 `172.30.0.0/29`，如果与 VPN 或现有 Docker 网络冲突，可在 `.env` 同时设置 `PROXY_SUBNET`、`PROXY_GATEWAY`、`PROXY_IP`、`PROXY_CADDY_IP`，并确保这些地址仍在新网段内；Compose 会把 `PROXY_CADDY_IP` 注入配置的 `trusted_proxies`。执行 `docker network ls` 和 `docker network inspect` 确认无重叠后再启动。

## SQLite WAL 备份与恢复

推荐停服务后备份，确保主库和 WAL 一致：

```bash
docker compose --env-file .env -f deploy/docker-compose.yaml stop proxy
docker run --rm -v easy-docker-proxy_proxy-data:/data -v "$PWD/backups:/backup" alpine \
  sh -c 'cp -a /data/proxy.db /backup/proxy.db && cp -a /data/proxy.db-wal /backup/proxy.db-wal 2>/dev/null || true; cp -a /data/proxy.db-shm /backup/proxy.db-shm 2>/dev/null || true'
docker compose --env-file .env -f deploy/docker-compose.yaml start proxy
```

恢复时先停止服务，将备份文件恢复到同一 volume，再启动并验证：

```bash
docker compose --env-file .env -f deploy/docker-compose.yaml stop proxy
# 将 proxy.db、proxy.db-wal、proxy.db-shm 放回 /data，并保持容器用户可读写
docker compose --env-file .env -f deploy/docker-compose.yaml start proxy
docker compose --env-file .env -f deploy/docker-compose.yaml exec proxy \
  sh -c 'wget -qO- http://127.0.0.1:5001/healthz'
```

也可在离线副本上执行 `PRAGMA integrity_check;`。备份 `.env` 和 `config.yaml` 时应使用受限权限存储，因为其中包含密钥引用和部署信息。
