# ops-portal Phase 2 路线（证书自管 + 命令下发，不在本期实现）

- 证书页：发证（调 CF 建证 → p12 一次下载）/ 吊销（调 CF 吊销 → 边缘即时生效）/
  重签（一键吊销旧发新）/ 90 天有效期提前 14 天提醒；表结构预留 `issuer` 字段。
- 命令队列：`POST /containers/{id}/{start,stop,restart}` 白名单动作，
  `ops.operator` 以上 + 二次确认；日志 SSE 流式回传。
- RBAC：`ops.admin / ops.operator / ops.viewer` 三档 + 站点圈域。
- 审计：用户 × 证书序列号双记。
- break-glass：不走 mTLS 的应急通道，平时关闭、单独审计。
