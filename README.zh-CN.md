# easy-docker-proxy

[English](./README.md) | **中文**

多上游 Docker Registry **加速代理**。

- 流式转发，**不缓存镜像层**
- 记录拉取并提供统计网页
- 一个**自有域名**入口，客户端用 **mirrors + 路径前缀**
- 无 Docker socket、无容器管理

> **关于文档中的域名**  
> `reg.a.c`、`reg.example.com`、`https://reg.a.c` **只是示例**，不会自动生效。  
> 使用前请：① 自行申请域名 → ② 配置 A/AAAA（或 CNAME）解析到代理机 → ③ 在 Caddy/Nginx 与 `config.yaml` 的 `hosts` 中填写**真实主机名**。

详细步骤见 **[安装与配置指南](./docs/install.md)**。

---

## 界面预览

统计控制台截图（演示数据）。点击图片可查看大图。

| 登录 | 分析看板 |
|:----:|:--------:|
| ![登录](./docs/images/stats-login.png) | ![分析看板](./docs/images/stats-dashboard.png) |

| 上游仓库分析 | 镜像列表 |
|:------------:|:--------:|
| ![上游](./docs/images/stats-upstream.png) | ![镜像](./docs/images/stats-images.png) |

<p align="center">
  <img src="./docs/images/stats-events.png" alt="事件" width="90%" />
</p>

---

## 快速部署

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git
cd easy-docker-proxy

cp .env.example .env
# 填写 PROXY_ADMIN_TOKEN、PROXY_WEB_PASSWORD

cp configs/config.docker.yaml configs/config.yaml
# 将 hosts 中的 reg.example.com 改成你的真实域名

docker compose -f deploy/docker-compose.yaml up -d --build
```

| 项 | 说明 |
|----|------|
| 数据面 | 主机 **:5000**（不占 80/443） |
| 管理面 | **:5001**，默认不映射到公网 |
| HTTPS | 推荐已有 Caddy 反代到 `127.0.0.1:5000` |
| 统计页 | 映射 `127.0.0.1:5001` 后访问 `/stats/login.html` |

更多运维与安全说明：[deploy/README.md](./deploy/README.md)。

---

## 客户端怎么用

以下将 **`reg.a.c` 换成你的域名**。

### 1. Docker Hub（mirrors，无路径前缀）

```json
// /etc/docker/daemon.json
{
  "registry-mirrors": ["https://reg.a.c"]
}
```

```bash
systemctl restart docker   # Linux
docker pull nginx:alpine
```

HTTP 入口时需配置 `insecure-registries`。

### 2. 其它上游（路径前缀）

```bash
docker pull reg.a.c/ghcr.io/owner/app:tag
docker pull reg.a.c/quay.io/prometheus/prometheus:latest
docker pull reg.a.c/docker.io/library/nginx:latest
docker pull reg.a.c/registry.k8s.io/pause:3.9
```

代理按路径前缀选择上游，**剥掉前缀**后再请求真实仓库。  
**不要**用 `/etc/hosts` 把 `ghcr.io` 等官方名指到本机（易 TLS 失败）。

---

## 支持的上游仓库

默认配置：[configs/config.docker.yaml](./configs/config.docker.yaml)。

路径前缀用法：

```text
docker pull <你的域名>/<路径前缀>/...
```

Docker Hub 也可用 mirrors，直接 `docker pull nginx`（无前缀）。

| name | 上游 | 路径前缀 | 拉取示例 | 鉴权环境变量（可选） |
|------|------|----------|----------|----------------------|
| `dockerhub` | Docker Hub | `docker.io` + mirrors 无前缀 | `nginx` 或 `<域>/docker.io/library/nginx:latest` | `DOCKERHUB_USER` / `DOCKERHUB_TOKEN` |
| `ghcr` | GHCR | `ghcr.io` | `<域>/ghcr.io/owner/app:tag` | `GITHUB_USER` / `GITHUB_TOKEN` |
| `quay` | Quay.io | `quay.io` | `<域>/quay.io/org/img:tag` | `QUAY_USER` / `QUAY_TOKEN` |
| `gcr` | GCR | `gcr.io` | `<域>/gcr.io/project/img:tag` | `GCR_USER` / `GCR_TOKEN` |
| `k8s` | Kubernetes | `registry.k8s.io`、`k8s.gcr.io` | `<域>/registry.k8s.io/pause:3.9` | 默认匿名 |
| `mcr` | Microsoft MCR | `mcr.microsoft.com` | `<域>/mcr.microsoft.com/...` | 默认匿名 |
| `nvcr` | NVIDIA NGC | `nvcr.io` | `<域>/nvcr.io/...` | `NVCR_USER` / `NVCR_TOKEN` |
| `elastic` | Elastic | `docker.elastic.co` | `<域>/docker.elastic.co/...` | `ELASTIC_USER` / `ELASTIC_TOKEN` |
| `ecr-public` | AWS Public ECR | `public.ecr.aws` | `<域>/public.ecr.aws/...` | 默认匿名 |

- 环境变量模板：[.env.example](./.env.example)。**留空 = 公共匿名**；私有库或 Hub 限流时再填 Token。  
- 配置中可设 `enabled: false` 关闭某上游，或自行追加 `registries`。  
- 字段说明：[docs/install.md](./docs/install.md)。

---

## 文档

| 文档 | 内容 |
|------|------|
| [README.md](./README.md) | English README（默认） |
| [docs/install.md](./docs/install.md) | 安装、配置项、Caddy、验收、FAQ |
| [deploy/README.md](./deploy/README.md) | 部署架构、安全清单、备份 |
| [configs/config.example.yaml](./configs/config.example.yaml) | 本地配置模板 |

---

## 开发

```bash
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export PROXY_WEB_PASSWORD='your-long-password'
go run ./cmd/proxy -config configs/config.example.yaml
CGO_ENABLED=0 go test ./...
```

---

## 致谢 / License

数据面思路借鉴 [Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)（非其 fork，无容器管理）。

[MIT](./LICENSE)
