# UI 页面职责矩阵

新增功能前先对表：**功能放它唯一归属的页面，禁止在两个页面实现同一操作**。
共享 UI（徽标、密文展示、轮询）一律用 `$lib/` 里的组件/帮手，不要在页面里内联。

| 路由 | 页面 | 只负责 | 明确不做 |
|---|---|---|---|
| `/dashboard` | 仪表盘 | 数字摘要卡（可点击跳转）、站点健康一览（只读）、agent 主机列表 | 任何操作；不重复 sites 的容器/版本明细 |
| `/sites` | 站点与服务 | 站点详情卡、容器表、行内 start/stop/restart、拨测健康徽标、注销原生注册 | deploy 批量发布（→ `/ops`） |
| `/ops` | 命令与发布 | deploy 发布表单（勾选服务）、命令历史 | 单容器快捷启停（`/sites` 行内已覆盖） |
| `/certs` | 用户与设备 | 人类设备证书登记/吊销、用户登录绑定（RBAC） | 机器凭据签发（→ `/tokens`） |
| `/tokens` | 机器凭据 | portal-agent 令牌（enroll/长效）、机器证书包、service 证书、site 注册证书的签发与管理 | 人类设备证书登记（→ `/certs`） |
| `/audit` | 审计日志 | 审计查询（含 `alert.*` 告警）、手动备份 | — |

数据约定：

- whoami / agents / sites 由 `+layout.svelte` 每 15s 轮询进 `state.svelte.ts`，
  页面直接读 `portal` 全局状态，**不要**再为同一数据自建轮询；
- 页面私有数据（certs/tokens/commands/audit）用 `$lib/poll.ts` 轮询；
- 站点拨测健康徽标用 `SiteHealthBadge.svelte`；一次性签发结果展示用
  `SecretReveal.svelte`；角色判定用 `roleLevel`。
