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
# 确认 hosts 为官方 Registry 主机名，和/或 registry-mirrors 使用的代理地址
# 公网环境建议 access_control.mode: whitelist
```

### 2. 仅数据面

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
curl -H 'Host: registry-1.docker.io' http://127.0.0.1:5000/v2/
```

### 3. 客户端：镜像名不改，只改接入

```bash
# 与直连上游时相同，不要改写成自定义域名镜像名
docker pull nginx:alpine
docker pull ghcr.io/owner/image:tag
```

**Docker Hub（推荐）** — 客户端 `daemon.json`：

```json
{
  "registry-mirrors": ["http://<代理IP>:5000"],
  "insecure-registries": ["<代理IP>:5000"]
}
```

代理 `hosts` 需包含该 mirrors 地址，或设置 `default: dockerhub`。

**多仓库** — 内网 DNS 将 `registry-1.docker.io`、`ghcr.io` 等指到代理，且 `registries[].hosts` 填写这些官方主机名。

上游凭据：`registries[].auth`。可选客户端 `pull_auth`。详见根目录 [README.md](../README.md)。

### 4. TLS 边缘（Caddy，可选）

示例 `Caddyfile` 使用自有子域名做反代；若采用「官方主机名 + DNS」方案，证书需自行规划（企业 CA 等），不能对公网申请 `ghcr.io` 的证书。

```bash
docker compose -f deploy/docker-compose.yaml --profile edge up -d --build
```

### 5. Stats / Metrics（仅本机或内网）

默认 **不** 发布 5001。临时调试可在 compose 中打开：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

```bash
# .env 中配置 PROXY_WEB_PASSWORD（及可选 PROXY_WEB_USER）后启动
open "http://127.0.0.1:5001/stats/login.html"
# 浏览器登录 Web 控制台；拉取账号在「账号」页单独管理

# API 也可用 PROXY_ADMIN_TOKEN：
export TOKEN=$(grep PROXY_ADMIN_TOKEN .env | cut -d= -f2-)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/api/v1/summary?range=7d
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/metrics
```

### 拉取鉴权（可选）

默认 `pull_auth.mode: off`（匿名 pull）。若需 `docker login`：

1. 配置 `pull_auth.mode: optional` 或 `required`  
2. 用 `PROXY_PULL_PASSWORD` 引导首个拉取账号，或在控制台「账号 → Docker 拉取账号」创建  
3. `docker login <proxy-host> -u puller -p '...'`

## 安全清单

| 项 | 建议 |
|----|------|
| Admin 端口 | 不对 `0.0.0.0` 发布；仅 loopback / VPN / 内网反代 |
| `PROXY_ADMIN_TOKEN` | 随机 ≥32 字节；勿写入 git |
| `trusted_proxies` | 仅包含真实边缘（Caddy/Nginx）网段 |
| `access_control` | 公网建议 `whitelist`（或开启 `pull_auth.required`） |
| `default` | 公网建议留空：未知 Host → 404；勿与 `ACL off` 叠加成开放加速器 |
| `upstream_allowlist` | 默认开启；自定义上游需加入 hosts |
| `rate_limit` | 默认开启 |
| `/-/config` / `/-/reload` | 仅 **admin** 会话或 `PROXY_ADMIN_TOKEN`；viewer 无权限 |
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
# 或（需 admin：PROXY_ADMIN_TOKEN 或 web admin 会话）
curl -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/-/reload
```

热重载范围：路由 / ACL / 限流 / 上游与 token 缓存。  
**不会**重开 SQLite；改 `storage.dsn` 或账号库路径需重启容器。

## 备份

只需备份 volume `proxy-data`（或 `/data/proxy.db`）与 `config.yaml` / `.env`（密钥）。

## 健康检查

镜像内置 `HEALTHCHECK`：请求容器内 `http://127.0.0.1:5000/v2/`。
