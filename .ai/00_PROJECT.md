# easy-docker-proxy — 项目上下文（供 AI 阅读）

## 一句话

轻量、安全的多上游 Docker Registry V2 **只读加速代理**：零镜像落盘，仅持久化拉取记录，并提供简单统计 Web 页。

## 目标

- 多仓库 Host 路由（Docker Hub / GHCR / Quay / K8s / MCR / NVCR 等）
- 服务端 Token 换发 + 流式转发（不缓存镜像层）
- 异步写入拉取事件 + 日聚合，供分析
- 只读 Stats 页面（鉴权）
- 安全默认：管理面本机监听、强 token、无 docker.sock、无容器管理

## 非目标

- 容器启停 / 日志 / 更新
- 本地镜像缓存 / push
- 复杂多用户 RBAC、文档 CMS、网络诊断

## 文档索引

| 文件 | 内容 |
|------|------|
| [01_DESIGN.md](./01_DESIGN.md) | 完整设计方案 |
| [02_STRUCTURE.md](./02_STRUCTURE.md) | 目录与模块约定 |
| [03_SECURITY.md](./03_SECURITY.md) | 安全约束与禁止事项 |
| [MILESTONES.md](./MILESTONES.md) | 里程碑与验收（开发节奏） |
| [../AGENTS.md](../AGENTS.md) | AI 执行入口与常用命令 |

## 全局规范

所有 Grok 会话还须遵守：`~/.grok/rules/00-ai-coding-standards.md`
（`.ai` 目录、每项目 `AGENTS.md`、里程碑模式、文档同步等）。

## 借鉴说明

数据面思路借鉴 [dqzboy/Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)（Host 路由、服务端鉴权、流式、双端口），管理面板与 docker.sock 相关能力**不采用**。详见 README 致谢。

## 给 Agent 的约束

1. 改动前先读本目录文档，优先对齐设计，勿擅自扩大范围。
2. **禁止**挂载或调用 Docker socket；禁止实现容器管理 API。
3. **禁止**在仓库中提交真实密码、token；示例用 `${ENV}`。
4. 管理 API / Stats 默认需鉴权；`PROXY_ADMIN_TOKEN` 未配置时 fail-closed。
5. 写库不得阻塞 pull 热路径（异步队列）。
