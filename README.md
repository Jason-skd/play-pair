# PlayPair

面向已有朋友圈，综合成员时间、硬性约束和软偏好，生成群体聚会方案的 AI 辅助决策系统。

## 常用指令

需要 Go 1.27.1、Docker Compose、Make。启动本地 PostgreSQL、执行迁移并运行 API：

```sh
make db-up
make migrate-up
make run
```

默认连接 `postgres://playpair:playpair_dev@127.0.0.1:55432/playpair?sslmode=disable`，服务监听 `:8080`。另一个终端用 `curl http://127.0.0.1:8080/healthz` 检查进程，用 `curl http://127.0.0.1:8080/readyz` 检查数据库可用性。按 `Ctrl-C` 关闭服务；`make db-down` 停止数据库并保留数据卷。当前提供健康探针以及邮箱注册、登录、退出和当前用户 API。

配置来自环境变量：`DATABASE_URL`（直接运行二进制时必需）、`HTTP_ADDR`（默认 `:8080`）、`DB_MAX_CONNS`（`4`）、`STARTUP_TIMEOUT`（`5s`）、`READY_TIMEOUT`（`1s`）、`SHUTDOWN_TIMEOUT`（`10s`）和 `AUTH_COOKIE_SECURE`（默认 `true`）。`make run` 和 `make migrate-up` 使用 `DB_URL`，未设置时连接本地开发库。API 启动不会自动运行迁移。本地纯 HTTP 开发需要设置 `AUTH_COOKIE_SECURE=false`；生产必须通过 HTTPS 使用默认安全 Cookie。

认证使用同源服务端 Cookie Session。注册和登录请求必须是 JSON，并携带 `X-PlayPair-Request: 1`；前端应通过同一 origin 的 `/api` 访问后端。浏览器 Cookie 为 `HttpOnly`、`SameSite=Lax`，固定七天到期；退出只撤销当前会话。当前邮箱只作为登录标识，不代表已经验证归属。

检查与生成：

```sh
make check
make generate
```

`make check` 将固定版本工具安装到忽略的 `.tools/bin/`，运行格式、vet、构建、单元测试和生成一致性检查。修改 OpenAPI、migration 或 SQL 查询后运行 `make generate`，并审查生成差异。

集成测试使用单独的测试库，先执行 `docker compose exec -T postgres createdb -U playpair playpair_test`，再运行：

```sh
export TEST_DATABASE_URL='postgres://playpair:playpair_dev@127.0.0.1:55432/playpair_test?sslmode=disable'
make test-integration
go test -race ./...
make test-smoke
```

测试库已有时只需设置连接串；集成测试缺少连接串会失败。`make test-smoke` 构建并启动真实进程，检查 readiness 后发送 `SIGTERM`。

## 项目架构

这是单 Go module 服务。请求经 `internal/api` 的 Gin 路由进入，数据库查询经 `internal/db` 的 sqlc 生成代码访问 PostgreSQL。`internal/users` 管理账号身份，`internal/credentials` 管理注册登录与密码凭据，`internal/session` 管理会话，`internal/profile` 管理展示资料；其他业务模块只接收认证后的账号身份。

`users` 仅保存账号 ID 和创建时间；`password_credentials` 保存登录邮箱、密码哈希与更新时间；`profiles` 保存显示名、可选昵称和独立版本。注册在同一事务中创建账号、凭据、资料和 session。会话验证只读取 `sessions` 与 `users`；现有注册、登录和 `/me` 响应使用独立读取投影组合邮箱与显示名，不在认证查询中读取资料或密码。

`00003_account_modules.sql` 从已合并的 00002 迁移现有数据，保留账号、密码哈希和会话。发现缺失密码凭据的账号时迁移会拒绝执行，需先修复数据；不要重置开发库绕过检查。曾运行过未合并旧版 00002 的数据库仍需先核对其 schema 与已合并版本一致。

| 位置                | 职责                                        |
| ------------------- | ------------------------------------------- |
| `cmd/playpair-api/` | 进程入口、信号处理和退出状态                |
| `internal/app/`     | 装配 HTTP 与数据库、管理启动和有界关闭      |
| `internal/api/`     | Gin 路由、探针、request ID 和 HTTP 错误映射 |
| `internal/users/`、`internal/credentials/` | 账号身份、注册登录及密码凭据 |
| `internal/session/` | 会话签发、认证、到期与撤销 |
| `internal/profile/` | 个性化展示资料 |
| `internal/config/`  | 启动时解析并校验环境配置                    |
| `internal/db/`      | PostgreSQL 连接池、健康查询和 sqlc 生成代码 |
| `api/`、`db/`       | HTTP 契约、迁移和 SQL 查询的源文件          |

`Makefile` 是本地命令入口，`.github/workflows/pr.yml` 执行基础检查，`.github/workflows/main.yml` 执行数据库、race 和 smoke 检查。后续业务包按实施计划直接放在 `internal/` 下，由 `internal/app` 装配；生成类型不进入业务逻辑。

## 文档索引

- [实施计划](docs/implementation_plan.md)
- Proposal 文档
  - [Proposal v1 源文件](docs/proposal/Proposal_PlayPair_v1.tex)（[PDF](docs/proposal/Proposal_PlayPair_v1.pdf)）
  - [Proposal v2 历史稿](docs/proposal/Proposal_PlayPair_v2.docx)
  - [Proposal v3 源文件](docs/proposal/Proposal_PlayPair_v3.tex)（[PDF](docs/proposal/Proposal_PlayPair_v3.pdf)）
  - [Proposal v4 源文件](docs/proposal/Proposal_PlayPair_v4.tex)（[PDF](docs/proposal/Proposal_PlayPair_v4.pdf)）
- 课程要求原始资料
  - [JC2001 小组项目说明与提交要求](<docs/course-requirement/JC2001-GroupProjectDescription-and-Requirements-2026-27.pdf>)
  - [项目提案模板](docs/course-requirement/Proposal_Template.pdf)
  - [小组工作量档案](<docs/course-requirement/Group workload profile (Introduction to software engineering).docx>)
  - [项目与期末考试日期及成绩构成](<docs/course-requirement/Important_Dates_for_Group_Project_and_Final_Examination_and_Marks_Breakdown.pdf>)
- 课程要求中文整理
  - [小组项目说明与提交要求](<docs/course-requirement-translation/01-JC2001小组项目说明与提交要求-2026-27.md>)
  - [项目提案模板](docs/course-requirement-translation/02-项目提案模板.md)
  - [小组工作量档案](docs/course-requirement-translation/03-小组工作量档案.md)
  - [项目与期末考试重要日期及成绩构成](docs/course-requirement-translation/04-小组项目与期末考试重要日期及成绩构成.md)
- [仓库 AI 治理](AGENTS.md)
- [本仓库独有治理](docs/governance/repository.md)
- [AI 工作流治理](docs/governance/workflow.md)
- [通用工程约束](docs/governance/engineering.md)

## 权威来源

HTTP 契约以 `api/openapi.yaml` 为权威来源；数据库 schema 以 `db/migrations/`、查询以 `db/queries/` 为权威来源。`internal/api/generated/` 和 `internal/db/generated/` 都是生成代码，不得手工修改。
