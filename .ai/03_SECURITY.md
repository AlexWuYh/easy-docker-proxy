# 安全约束（强制）

本文档约束实现与 PR 审查。违反项应直接拒绝合并。

## 禁止

1. **禁止**挂载、连接或调用 Docker Engine API / `docker.sock`。
2. **禁止**实现容器 create/start/stop/delete/logs/exec 等能力。
3. **禁止**默认弱口令、硬编码 session/admin secret。
4. **禁止**在 API 响应中返回密码重置 token 或明文验证码答案。
5. **禁止**未鉴权的配置写入接口。
6. **禁止**无条件信任客户端 `X-Forwarded-For`（必须 `trusted_proxies`）。
7. **禁止**将上游 password / Bearer 写入日志或 pull_events。
8. **禁止**在仓库提交真实凭据；示例一律 `${ENV}`。
9. **禁止** stats/admin 在无 token 时对公网开放（fail-closed）。
10. **禁止**在 pull 热路径同步阻塞写库。

## 必须

1. `PROXY_ADMIN_TOKEN`（或等价）未设置时：admin/stats 受保护接口不可用。
2. 数据面仅允许 GET/HEAD（及 `/v2/` 握手）；拒绝 push 类方法。
3. 管理监听默认 loopback。
4. 上游 URL 校验：优先 https；限制危险 scheme。
5. 异步记录队列有界；满则丢弃事件并 metric++，不拖垮代理。

## 威胁模型（简）

| 资产 | 威胁 | 缓解 |
|------|------|------|
| 宿主机 | RCE via docker.sock | 不挂载 socket |
| 上游账号 | 被滥用拉私有库 / 刷配额 | ACL + 限流 + 文档警示 |
| 管理面 | 未授权改配置 | token + 本机绑定 |
| 统计数据 | 信息泄露 | stats 鉴权 |
| 节点带宽 | 开放代理被刷 | 默认建议白名单 |
