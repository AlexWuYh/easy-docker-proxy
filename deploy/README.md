# 部署指南 — easy-docker-proxy

## 架构

```text
客户端 / registry-mirrors
        → 主机 :5000              → proxy 数据面
        → 已有 Caddy（:443 等）   → reverse_proxy → 127.0.0.1:5000
可选: compose --profile edge（自带 Caddy，默认 8080/8443）
管理面 proxy:5001 默认不映射到宿主机
```

- **不**挂载 `docker.sock`
- 镜像层 **不**落盘；仅 SQLite 元数据在 `/data`
- 进程以非 root（uid 65532）运行
- TLS 边缘：**优先接现有 Caddy/Nginx**；compose 内 Caddy 仅可选

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

`config.docker.yaml` 默认 `default: dockerhub`，mirrors 指向本机数据面即可，无需改 hosts。  
若将 `default` 设为 `""`，则须把 mirrors 使用的 `IP:端口` 或域名写入 `registries[].hosts`。

**多仓库** — 内网 DNS 将 `registry-1.docker.io`、`ghcr.io` 等指到代理，且 `registries[].hosts` 填写这些官方主机名。

上游凭据：`registries[].auth`。可选客户端 `pull_auth`。详见根目录 [README.md](../README.md)。

### 4. 使用已有 Caddy（推荐）

不必使用本仓库的 `edge` profile。只跑 proxy，由**机器上已有的 Caddy** 做 HTTPS / 域名反代即可。

#### 4.1 只启动代理

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
# 确认宿主机 5000 可访问
curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: registry-1.docker.io' http://127.0.0.1:5000/v2/
```

#### 4.2 代理侧 `trusted_proxies`

Caddy 与 proxy **同机**时，保证配置包含 loopback（示例已有 `127.0.0.1/32`）。  
Caddy 在**另一台机或 Docker 网桥**时，把 Caddy 的出口网段写进 `trusted_proxies`，否则不要信任 `X-Forwarded-*`。

```yaml
# configs/config.yaml
trusted_proxies:
  - "127.0.0.1/32"
  - "10.0.0.0/8"      # 按实际调整
  - "172.16.0.0/12"
```

改完后热重载或重启 proxy。

#### 4.3 Caddyfile 片段（复制到你现有配置）

完整示例文件：[caddy.external.example](./caddy.external.example)。

**场景 A — HTTPS 镜像域名（给 `registry-mirrors` 用）**

```caddy
# 客户端: "registry-mirrors": ["https://registry.example.com"]
# 代理 config 保持 default: dockerhub
registry.example.com {
	reverse_proxy 127.0.0.1:5000 {
		header_up Host {host}
		header_up X-Forwarded-Host {host}
		header_up X-Forwarded-Proto {scheme}
		header_up X-Real-IP {remote_host}
		header_up X-Forwarded-For {remote_host}
		transport http {
			read_timeout 0
			write_timeout 0
		}
	}
}
```

客户端 `daemon.json` 示例：

```json
{
  "registry-mirrors": ["https://registry.example.com"]
}
```

（HTTPS 一般无需 `insecure-registries`。）

**场景 B — proxy 在 Docker 网络、Caddy 在宿主机**

compose 已映射 `5000:5000` 时，上面 `127.0.0.1:5000` 即可。  
若 Caddy 也在 compose 同一 `proxy-net` 且**不**映射 5000，可写成：

```caddy
reverse_proxy proxy:5000 { ... }
```

并保证 Caddy 容器 `depends_on` / 网络可达。

**要点**

| 项 | 说明 |
|----|------|
| 上游地址 | `reverse_proxy` 到 **数据面 5000**，不是 5001 |
| Host | 建议 `header_up Host {host}`，与代理按 Host 路由一致 |
| 超时 | blob 较大，建议 `read_timeout 0` / `write_timeout 0` |
| 证书 | 用你现有 Caddy 的自动 HTTPS 或企业证书即可 |
| 勿重复 | 已有 Caddy 时不要再 `compose --profile edge` |

#### 4.4 可选：本仓库自带 Caddy（无现成边缘时）

默认映射 **8080/8443**（`EDGE_HTTP_PORT` / `EDGE_HTTPS_PORT`），不占用 80/443：

```bash
docker compose -f deploy/docker-compose.yaml --profile edge up -d --build
```

配置见 [Caddyfile](./Caddyfile)。

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
