# AGENTS.md — easy-docker-proxy

> Grok / AI 在本仓库的执行入口。  
> 全局规范：`~/.grok/rules/00-ai-coding-standards.md`。

## 项目

- **名称**：easy-docker-proxy  
- **一句话**：轻量多上游 Docker Registry 只读加速代理；零镜像缓存，持久化拉取记录，简单 Stats 页。  
- **当前里程碑**：**M0 — 脚手架**（已完成骨架；下一阶段 **M1 — 数据面代理**）  
- **详情**： [`.ai/MILESTONES.md`](./.ai/MILESTONES.md)、[`.ai/01_DESIGN.md`](./.ai/01_DESIGN.md)

## 必读文档

| 文档 | 内容 |
|------|------|
| [`.ai/00_PROJECT.md`](./.ai/00_PROJECT.md) | 目标 / 非目标 / Agent 约束 |
| [`.ai/01_DESIGN.md`](./.ai/01_DESIGN.md) | 完整设计方案 |
| [`.ai/02_STRUCTURE.md`](./.ai/02_STRUCTURE.md) | 目录与模块 |
| [`.ai/03_SECURITY.md`](./.ai/03_SECURITY.md) | 安全强制约束 |
| [`.ai/MILESTONES.md`](./.ai/MILESTONES.md) | 里程碑与验收 |

## 常用命令

```bash
# 构建
go build -o bin/proxy ./cmd/proxy

# 运行（脚手架占位；M1 起才是真实代理）
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
go run ./cmd/proxy -config configs/config.example.yaml

# 测试（M1+ 补充后）
go test ./...
```

## 约定

- **只做当前里程碑**；设计中的 M2/M3 未开始前不实现 Stats 全量 UI / 复杂管理面。
- 行为、端口、配置字段、安全模型变更时，同步更新 `.ai/` 与本文件命令说明。
- 密钥仅环境变量（`PROXY_ADMIN_TOKEN`、`DOCKERHUB_*`）；禁止提交真值。
- 数据面借鉴 [Docker-Proxy](https://github.com/dqzboy/Docker-Proxy) 思路；**不是**其 fork，不引入容器管理。

## 本项目禁止

- 挂载或调用 `docker.sock` / 任何容器管理 API  
- 本地缓存镜像层  
- 未鉴权的 admin/stats 写接口  
- 硬编码默认 admin 口令  

## 文档同步

代码变更后检查：`MILESTONES.md` 状态、`01_DESIGN` 是否偏差、`README.md` 用户说明、本文件命令是否仍可用。
