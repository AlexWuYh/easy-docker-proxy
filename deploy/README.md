# 部署指南 — easy-docker-proxy

## 架构

```text
Internet → Caddy (:443, TLS, 子域名)
              → proxy:5000  (Registry 数据面，可公网)
         (内网) → proxy:5001 (Admin / Stats /metrics，勿对公网映射)
```

- **不**挂载 `docker.sock`
- 镜像层 **不**落盘；仅 SQLite 元数据在 `/data`
- 进程以非 root（uid 65532）运行

## 快速部署（VPS）

### 1. 准备密钥与配置

```bash
cd /path/to/easy-docker-proxy
cp .env.example .env
# 生成强 token
echo "PROXY_ADMIN_TOKEN=$(openssl rand -hex 32)" >> .env

cp configs/config.docker.yaml configs/config.yaml
# 编辑 configs/config.yaml：把 hub.example.com 等改成你的域名
# 公网环境建议 access_control.mode: whitelist
```

### 2. 仅数据面

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
curl -H 'Host: hub.example.com' http://127.0.0.1:5000/v2/
```

### 3. 带 TLS 边缘（Caddy）

1. DNS：`hub` / `ghcr` / … 子域名 A 记录指向本机  
2. 编辑 `deploy/Caddyfile` 中的域名  
3. 启动 edge profile：

```bash
docker compose -f deploy/docker-compose.yaml --profile edge up -d --build
```

### 4. 客户端拉取

```bash
docker pull hub.example.com/library/nginx:alpine
docker pull ghcr.example.com/owner/image:tag
```

### 5. Stats / Metrics（仅本机或内网）

默认 **不** 发布 5001。临时调试可在 compose 中打开：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

```bash
export TOKEN=$(grep PROXY_ADMIN_TOKEN .env | cut -d= -f2)
open "http://127.0.0.1:5001/stats/?token=$TOKEN"
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/api/v1/summary?range=7d
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/metrics
```

## 安全清单

| 项 | 建议 |
|----|------|
| Admin 端口 | 不对 `0.0.0.0` 发布；仅 loopback / VPN / 内网反代 |
| `PROXY_ADMIN_TOKEN` | 随机 ≥32 字节；勿写入 git |
| `trusted_proxies` | 仅包含真实边缘（Caddy/Nginx）网段 |
| `access_control` | 公网建议 `whitelist` |
| `upstream_allowlist` | 默认开启；自定义上游需加入 hosts |
| `rate_limit` | 默认开启 |
| docker.sock | **禁止**挂载 |

### trusted_proxies 说明

仅当连接 peer IP 落在 `trusted_proxies` 时，才会信任：

- `X-Forwarded-For` / `X-Real-IP`（客户端 IP）
- `X-Forwarded-Host`（Host 路由）

错误配置会导致 IP ACL 失真或 Host 被伪造。

### upstream_allowlist 说明

加载配置时校验每个 `registries[].upstream` 的 hostname。  
默认允许 Docker Hub / GHCR / Quay / k8s / MCR / NVCR 等常见公共 registry。  
私有仓库：把 hostname 写入 `upstream_allowlist.hosts`，或（不推荐）`enabled: false`。

## 热重载

修改挂载的 `config.yaml` 后：

```bash
docker kill -s HUP $(docker compose -f deploy/docker-compose.yaml ps -q proxy)
# 或
curl -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/-/reload
```

注意：SQLite 路径变更需重建/重启容器。

## 备份

只需备份 volume `proxy-data`（或 `/data/proxy.db`）与 `config.yaml` / `.env`（密钥）。

## 健康检查

镜像内置 `HEALTHCHECK`：请求容器内 `http://127.0.0.1:5000/v2/`。
