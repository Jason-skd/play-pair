# PlayPair

面向已有朋友圈，综合成员时间、硬性约束和软偏好，生成群体聚会方案的 AI 辅助决策系统。

> [!NOTE]
> 当前仅有后端工程底座；业务功能尚未实现。

## 常用指令

需要 Go 1.27.1、Docker Compose、Make。运行本地服务：

```sh
make db-up
make migrate-up
make run
```

默认连接 `postgres://playpair:playpair_dev@127.0.0.1:55432/playpair?sslmode=disable`，服务监听 `:8080`。另一个终端访问 `curl http://127.0.0.1:8080/healthz` 和 `/readyz`。按 `Ctrl-C` 有界关闭。`make db-down` 停止数据库并保留数据卷。

配置来自环境变量：`DATABASE_URL`（必需，`make run` 用本地默认值）、`HTTP_ADDR`（`:8080`）、`DB_MAX_CONNS`（`4`）、`STARTUP_TIMEOUT`（`5s`）、`READY_TIMEOUT`（`1s`）、`SHUTDOWN_TIMEOUT`（`10s`）。API 启动不会运行迁移。

检查与生成：

```sh
make check
make generate
```

`make check` 安装固定版本工具到忽略的 `.tools/bin/`，运行格式、vet、构建、单元测试和生成一致性检查。`make generate` 只在修改 OpenAPI、migration 或 SQL 查询后运行，并审查生成差异。

集成测试使用单独的测试库，先执行 `docker compose exec -T postgres createdb -U playpair playpair_test`，再运行：

```sh
export TEST_DATABASE_URL='postgres://playpair:playpair_dev@127.0.0.1:55432/playpair_test?sslmode=disable'
make test-integration
go test -race ./...
make test-smoke
```

测试库已有时只需设置连接串；集成测试缺少连接串会失败。`make test-smoke` 构建并启动真实进程，检查 readiness 后发送 `SIGTERM`。

## 项目架构

`cmd/playpair-api` 负责进程信号；`internal/app` 负责装配与生命周期；`internal/api` 承担 Gin 协议与错误映射；`internal/config` 承担进程配置；`internal/db` 负责 PostgreSQL/sqlc 边界。

## 文档索引

- [实施计划](docs/implementation_plan.md)
- [后端底座设计记录](docs/design/backend-foundation.md)
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
