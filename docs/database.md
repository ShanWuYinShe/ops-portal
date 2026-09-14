# 数据层（SQLite）

portal 的全部运营数据存在单个 SQLite 文件 `portal.db`
（`DATA_DIR`，容器内 `/app/data/portal.db`），WAL 模式。
表：`certs` / `portal_agent_tokens` / `users` / `sites` / `commands` / `audit`。
portal-agent 心跳等实时状态仍在内存（backend 重启后靠下次心跳自愈）。

## 从 JSON 迁移

旧版（≤ 4c2bd14）用 `certs.json` / `agent_tokens.json` / `users.json` /
`sites.json` / `commands.json` / `audit.jsonl`。
新版首次启动时自动一次性导入（目标表空才导），成功后原文件改名
`.migrated` 备查，失败只记日志不挡启动。回滚到旧版时 `.migrated`
改回原名即可（旧版读 JSON，新版写 DB，两边不互写）。

## portal-agent 改名迁移（未发版破坏性变更）

agent 更名 portal-agent 时同步改名了表/列与 kind 值，启动时幂等执行
（`db.go migrateRenames`，仅旧名存在时动作，全新库空转）：

- 表 `agent_tokens` → `portal_agent_tokens`；
- 列 `commands.agent_id` → `portal_agent_id`；
- `certs.kind` / `portal_agent_tokens.kind` 存量值 `agent` → `portal-agent`。

令牌明文前缀 `opsa_` → `opa_`：hash 对明文计算，旧令牌不迁移，
改名版本部署后为 portal-agent 重新签发令牌（旧令牌作废）。

## 备份与恢复

- 在线备份：`POST /api/portal/backup`（admin，审计页有点按钮），
  `VACUUM INTO data/backups/portal-<时间>.db`，只留最新 14 份。
  cron 调这个接口即可（持 human 证书），示例每天 03:00。
- 异地：把 `backups/` 目录再同步一份出去（rsync/s3 任选）。
- 恢复：停 backend → 用备份文件覆盖 `portal.db`（删同目录
  `portal.db-wal` / `-shm`）→ 起 backend。
- `service-client.key` / `.pem` 与 `portal-agent` 证书包是文件，
  不在库里，随 `data/` 目录一起备份。

## 保留期

- 审计：`AUDIT_RETENTION_DAYS`（默认 180），启动与备份前清理过期行。
- 命令：终结（done/failed/expired）只留最新 500 条，排队命令
  `COMMAND_TTL`（默认 24h）过期后标 expired 不再下发。

## 容量实测（2026-09-14，本机 + 容器内实测，非推算）

### 索引设计（重要，勿回退）

`serial` / `fingerprint` 是 hex 串，CF 透传**大写**、建证 API 返回**小写**，
故比较必须大小写不敏感。SQLite 的坑（已踩并修复）：

- **BINARY 索引无法服务 `COLLATE NOCASE` 比较**，会静默退化为全表扫描；
  索引必须显式声明 `COLLATE NOCASE`。
- **含 `OR` 的复合条件不做索引下推**（`A OR B` → `SCAN`）；
  拆成 `UNION ALL` 两个分支后各自命中索引。
- 两者叠加的实测差距（5000 张证书）：

| 实现 | 单次门禁查表 |
|---|---|
| 修复前（`OR` + BINARY 索引 → 全表扫描） | 633 µs |
| 修复后（`UNION ALL` + NOCASE 索引） | 35 µs（约 18–48 倍） |

`users.cert_id` 也建了索引（`currentUser` 每请求按证书反查用户）。
回归测试：`certs_lookup_test.go`（11 个大小写/吊销/空串语义用例）。

### 实测性能（5000 证书 + 10 万行审计的容器实跑）

| 操作 | 耗时 |
|---|---|
| `whoami`（门禁：查证书 + 查角色） | 2–6 ms |
| `GET /certs`（5000 行列表 + 排序） | 44 ms |
| `GET /audit`（10 万行取最近 200） | 1.8 ms |
| `POST /backup`（10 万审计全量快照） | 109 ms |

单次查表基准：35 µs；50 并发 ×（查表+角色）合 49 µs/组。
迁移吞吐：10 万行审计 JSONL 导入约 2 s。

### 存储占用

审计约 **107 字节/行**，线性增长（10 万行 ≈ 10 MB，100 万行 ≈ 102 MB）。
5000 证书 + 10 万审计整库 ≈ 11 MB。控制备份目录增长的是
`AUDIT_RETENTION_DAYS`。

### 结论与换库触发条件

按当前画像（证书/令牌/用户/站点个位到百级，命令裁剪后 500 条封顶，
审计按天累积、180 天保留），SQLite 余量在 3 个数量级以上：
单库进入 GB 级需审计留存数十年，或把保留期设定到荒谬长度。

出现以下任一情况才需要迁移（当前均不满足）：

1. **portal 多实例/水平扩展**——SQLite 是单机文件，多副本共享不可靠（硬门槛）；
2. 外部 BI/报表直连库，或需与 hotspot 等库跨库 JOIN；
3. 需要 PITR / 流复制级灾备 SLA；
4. 单表进入百万级且查询模式变成复杂分析。

迁移成本评估：改动集中在 `db.go` + 各 store 的 SQL——换 `pgx`、调方言
（`AUTOINCREMENT`→`IDENTITY`、`?`→`$1`、`COLLATE NOCASE`→`citext`/`LOWER()`、
`VACUUM INTO`→`pg_dump`）、compose 加服务、写一次性导入。
6 张表都是简单 CRUD，无存储过程/复杂查询，约半天。

