# Issue #3：后端工程底座

状态：实施中。范围限于配置、HTTP 探针、数据库生命周期和工程检查，不包含业务实体、认证或 AI。

## 决策

- 单 Go module，flat internal 包；app 装配依赖，api 隔离 Gin，db 隔离 pgx/sqlc。
- 后续业务 API 使用 `/api/v1`；运维探针使用 `/healthz` 和 `/readyz`，不要求认证或客户端时区。
- OpenAPI 是 HTTP 唯一权威；migrations 是 schema 唯一权威；queries 是 SQL 查询唯一权威。生成代码提交并通过固定版本工具更新。
- 本地使用 Compose PostgreSQL 18；测试使用独立测试库。API 不自动迁移，迁移由显式命令执行。
- liveness 不访问数据库；readiness 执行限时 SQL 查询。关闭先撤销 readiness，再排空 HTTP，超时强制关闭并取消请求，最后释放数据库池。
- 基线迁移无业务 DDL，只验证迁移版本机制；不声称它验证了业务数据回滚。
- 错误响应只含稳定分类、安全消息和服务端 request ID；日志不记录原始 DSN、SQL 错误或 panic 值。

## 实施与验证顺序

1. 配置、OpenAPI/Gin 和 HTTP 行为测试；独立提交。
2. pgx、goose/sqlc、组合根和信号关闭；真实数据库和生命周期测试；独立提交。
3. 固定生成检查、CI、README 全流程和负向漂移检查；独立提交。

每个单元提交前检查 staged diff 与 README 索引。推送、PR、Issue 状态修改不在本轮范围。
