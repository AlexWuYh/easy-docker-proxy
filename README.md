# easy-docker-proxy

轻量、安全的 **多上游 Docker Registry 加速代理**。

- **零镜像缓存**：manifest / blob 流式转发，不落盘
- **只记拉取记录**：异步写入元数据与日聚合，便于统计
- **简单 Stats 页**：只读看板（Pulls、流量、热门镜像、客户端、错误）
- **安全默认**：无 Docker socket、无容器管理、管理面本机监听 + 强 Token

> 状态：脚手架阶段（M0）。数据面与统计能力按 [`.ai/01_DESIGN.md`](./.ai/01_DESIGN.md) 分阶段实现。

## 功能规划

| 阶段 | 内容 |
|------|------|
| M1 | Host 路由、上游 Token 换发、流式代理、ACL、限流 |
| M2 | 拉取事件 + SQLite 聚合 |
| M3 | Stats Web（鉴权） |
| M4 | 部署硬化、文档、可选 Prometheus |

详见 [设计方案](./.ai/01_DESIGN.md)。

## 快速开始（脚手架）

### 依赖

- Go 1.22+
- （可选）Docker / Docker Compose

### 本地运行（占位入口）

```bash
cp configs/config.example.yaml configs/config.yaml
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"

# 当前 main 为脚手架占位，实现完成后：
go run ./cmd/proxy -config configs/config.yaml
```

### 配置

示例见 [`configs/config.example.yaml`](./configs/config.example.yaml)。  
密钥请用环境变量注入，勿提交真实密码。

### Compose（规划）

```bash
# 实现完成后
cp .env.example .env   # 填写 PROXY_ADMIN_TOKEN
docker compose -f deploy/docker-compose.yaml up -d
```

## 客户端用法（规划）

```bash
# 示例：通过子域名拉取（需 DNS + 反代就绪）
docker pull hub.example.com/library/nginx:alpine
docker pull ghcr.example.com/owner/image:tag
```

## 项目结构

```text
.ai/           # 架构与约束文档（供人与 AI 阅读）
cmd/proxy/     # 入口
internal/      # 业务包（见 .ai/02_STRUCTURE.md）
configs/       # 示例配置
deploy/        # compose / Caddy
web/static/    # Stats 前端
```

## 文档

| 文档 | 说明 |
|------|------|
| [`AGENTS.md`](./AGENTS.md) | AI / 贡献者执行入口与常用命令 |
| [`.ai/00_PROJECT.md`](./.ai/00_PROJECT.md) | 项目上下文 |
| [`.ai/01_DESIGN.md`](./.ai/01_DESIGN.md) | 完整设计方案 |
| [`.ai/02_STRUCTURE.md`](./.ai/02_STRUCTURE.md) | 目录与模块 |
| [`.ai/03_SECURITY.md`](./.ai/03_SECURITY.md) | 安全强制约束 |
| [`.ai/MILESTONES.md`](./.ai/MILESTONES.md) | 里程碑与验收 |

## 致谢 / Acknowledgments

本项目在 **数据面设计** 上借鉴了 [**dqzboy/Docker-Proxy**](https://github.com/dqzboy/Docker-Proxy) 的优秀思路，包括但不限于：

- 按请求 `Host` 路由到多上游 Registry
- 服务端完成 Token 换发，客户端无感
- 流式转发、不落盘
- 数据面与管理面端口分离

**项目地址**：[https://github.com/dqzboy/Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)

感谢 [dqzboy](https://github.com/dqzboy) 及 Docker-Proxy 贡献者们的开源工作。

> easy-docker-proxy **不是** Docker-Proxy 的 fork。我们刻意未引入其 Web 管理面板中的容器管理、Docker socket 挂载等能力，并在鉴权、拉取记录持久化、统计展示等方面采用不同的产品边界与安全默认值。

## License

[MIT](./LICENSE)
