# 数据层（SQLite）

portal 的全部运营数据存在单个 SQLite 文件 `portal.db`
（`DATA_DIR`，容器内 `/app/data/portal.db`），WAL 模式。
表：`certs` / `agent_tokens` / `users` / `sites` / `commands` / `audit`。
agent 心跳等实时状态仍在内存（backend 重启后靠下次心跳自愈）。

## 从 JSON 迁移

旧版（≤ 4c2bd14）用 `certs.json` / `agent_tokens.json` / `users.json` /
`sites.json` / `commands.json` / `audit.jsonl`。
新版首次启动时自动一次性导入（目标表空才导），成功后原文件改名
`.migrated` 备查，失败只记日志不挡启动。回滚到旧版时 `.migrated`
改回原名即可（旧版读 JSON，新版写 DB，两边不互写）。

## 备份与恢复

- 在线备份：`POST /api/portal/backup`（admin，审计页有点按钮），
  `VACUUM INTO data/backups/portal-<时间>.db`，只留最新 14 份。
  cron 调这个接口即可（持 human 证书），示例每天 03:00。
- 异地：把 `backups/` 目录再同步一份出去（rsync/s3 任选）。
- 恢复：停 backend → 用备份文件覆盖 `portal.db`（删同目录
  `portal.db-wal` / `-shm`）→ 起 backend。
- `service-client.key` / `.pem` 与 `agent` 证书包是文件，
  不在库里，随 `data/` 目录一起备份。

## 保留期

- 审计：`AUDIT_RETENTION_DAYS`（默认 180），启动与备份前清理过期行。
- 命令：终结（done/failed/expired）只留最新 500 条，排队命令
  `COMMAND_TTL`（默认 24h）过期后标 expired 不再下发。
