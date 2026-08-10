# easy-docker-proxy

多上游 Docker Registry **加速代理**：流式转发、**不缓存镜像**，记录拉取并提供统计网页。  
对外一个域名（如 `https://reg.a.c`），客户端用 **mirrors + 路径前缀**；**不要**用改 hosts 劫持 `ghcr.io` 等官方名。

**完整安装、配置项与验收步骤 → [docs/install.md](./docs/install.md)**

## 客户端怎么用

### Docker Hub（mirrors）

```json
// /etc/docker/daemon.json
{ "registry-mirrors": ["https://reg.a.c"] }
```

```bash
docker pull nginx:alpine
```

### 其它上游（同一域名 + 路径前缀）

```bash
docker pull reg.a.c/ghcr.io/owner/app:tag
docker pull reg.a.c/quay.io/prometheus/prometheus:latest
docker pull reg.a.c/docker.io/library/nginx:latest
```

## 快速部署

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git && cd easy-docker-proxy
cp .env.example .env                    # PROXY_ADMIN_TOKEN、PROXY_WEB_PASSWORD
cp configs/config.docker.yaml configs/config.yaml   # 改 hosts 为你的域名
docker compose -f deploy/docker-compose.yaml up -d --build
```

- 数据面 **:5000** · 管理面默认不映射  
- 已有 Caddy：反代 `127.0.0.1:5000`（见安装文档）  
- 统计：映射 `127.0.0.1:5001` 后打开 `/stats/login.html`  

更细的环境变量、YAML 字段、热重载、公网注意见 **[安装与配置指南](./docs/install.md)** · 运维清单见 [deploy/README.md](./deploy/README.md)。

## 开发

```bash
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)" PROXY_WEB_PASSWORD='your-long-password'
go run ./cmd/proxy -config configs/config.example.yaml
CGO_ENABLED=0 go test ./...
```

## 致谢 / License

数据面思路借鉴 [Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)（非 fork，无容器管理）。  
[MIT](./LICENSE)
