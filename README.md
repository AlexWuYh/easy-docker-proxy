# easy-docker-proxy

多上游 Docker Registry **加速代理**：流式转发、**不缓存镜像**，记录拉取并提供统计网页。  
对外一个**你自己的域名**，客户端用 **mirrors + 路径前缀**；**不要**用改 hosts 劫持 `ghcr.io` 等官方名。

> 下文中的 `reg.a.c` / `https://reg.a.c` **仅为示例**。实际使用前请自行：  
> 1）申请域名；2）把 A/AAAA（或 CNAME）解析到代理机器；3）在 Caddy/Nginx 与 `config.yaml` 的 `hosts` 中填写**真实域名**。

**完整安装、配置项与验收步骤 → [docs/install.md](./docs/install.md)**

## 客户端怎么用

### Docker Hub（mirrors）

```json
// /etc/docker/daemon.json  （把 reg.a.c 换成你的域名）
{ "registry-mirrors": ["https://reg.a.c"] }
```

```bash
docker pull nginx:alpine
```

### 其它上游（同一域名 + 路径前缀）

```bash
# 将 reg.a.c 换成你的域名
docker pull reg.a.c/ghcr.io/owner/app:tag
docker pull reg.a.c/quay.io/prometheus/prometheus:latest
docker pull reg.a.c/docker.io/library/nginx:latest
```

代理按路径前缀选上游，剥前缀后再访问真实仓库。

## 支持的上游仓库

默认配置见 [configs/config.docker.yaml](./configs/config.docker.yaml)。下表「路径前缀」用于：

`docker pull <你的域名>/<路径前缀>/...`  
（Docker Hub 也可用 mirrors，**无前缀**直接 `docker pull nginx`。）

| name | 上游 | 路径前缀 | 拉取示例（`reg` = 你的域名） | 可选鉴权环境变量 |
|------|------|----------|------------------------------|------------------|
| dockerhub | [Docker Hub](https://hub.docker.com) | `docker.io`（及 mirrors 无前缀） | `docker pull nginx` 或 `reg/docker.io/library/nginx:latest` | `DOCKERHUB_USER` / `DOCKERHUB_TOKEN` |
| ghcr | [GHCR](https://docs.github.com/packages) | `ghcr.io` | `reg/ghcr.io/owner/app:tag` | `GITHUB_USER` / `GITHUB_TOKEN` |
| quay | [Quay.io](https://quay.io) | `quay.io` | `reg/quay.io/org/img:tag` | `QUAY_USER` / `QUAY_TOKEN` |
| gcr | [GCR](https://cloud.google.com/container-registry) | `gcr.io` | `reg/gcr.io/project/img:tag` | `GCR_USER` / `GCR_TOKEN` |
| k8s | Kubernetes 官方 | `registry.k8s.io`、`k8s.gcr.io` | `reg/registry.k8s.io/pause:3.9` | 默认匿名 |
| mcr | [MCR](https://mcr.microsoft.com) | `mcr.microsoft.com` | `reg/mcr.microsoft.com/...` | 默认匿名 |
| nvcr | [NVIDIA NGC](https://catalog.ngc.nvidia.com) | `nvcr.io` | `reg/nvcr.io/...` | `NVCR_USER` / `NVCR_TOKEN` |
| elastic | [Elastic](https://www.docker.elastic.co) | `docker.elastic.co` | `reg/docker.elastic.co/...` | `ELASTIC_USER` / `ELASTIC_TOKEN` |
| ecr-public | [AWS Public ECR](https://gallery.ecr.aws) | `public.ecr.aws` | `reg/public.ecr.aws/...` | 默认匿名 |

- 鉴权变量见 [.env.example](./.env.example)；**留空 = 公共匿名**（私有库 / Hub 限流时再填 Token）。  
- 可在配置中 `enabled: false` 关闭某一上游，或自行追加 `registries` 条目。  
- 配置字段说明见 [docs/install.md](./docs/install.md)。

## 快速部署

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git && cd easy-docker-proxy
cp .env.example .env                    # PROXY_ADMIN_TOKEN、PROXY_WEB_PASSWORD
cp configs/config.docker.yaml configs/config.yaml
# 1. 域名已申请并解析到本机
# 2. 把 config 中 hosts: reg.example.com 改成你的真实域名
docker compose -f deploy/docker-compose.yaml up -d --build
```

- 数据面 **:5000** · 管理面默认不映射  
- 已有 Caddy：用**真实域名**反代 `127.0.0.1:5000`（见安装文档）  
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
