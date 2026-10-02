# PlayPair

面向已有朋友圈，综合成员时间、硬性约束和软偏好，生成群体聚会方案的 AI 辅助决策系统。

## 常用指令

需要 Go 1.27.1、Docker Compose 和 Make。启动本地 PostgreSQL、执行迁移并运行 API：

```sh
make db-up
make migrate-up
make run
```

服务默认监听 `:8080`，本地数据库连接为 `postgres://playpair:playpair_dev@127.0.0.1:55432/playpair?sslmode=disable`。使用以下命令检查服务并停止数据库：

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
make db-down
```

API 启动不会自动执行迁移。直接运行二进制时需要设置 `DATABASE_URL`；本地 HTTP 开发时设置 `AUTH_COOKIE_SECURE=false`。其余配置项及默认值见 [`internal/config/`](internal/config/)。

提交前运行：

```sh
make check
make generate
```

需要数据库的集成、race 和 smoke 检查见 [`Makefile`](Makefile) 及 [`scripts/`](scripts/)。

## 项目架构

这是一个 Go module 服务：HTTP/API 层依赖 `internal/` 下的业务模块，业务模块通过 `internal/db/` 访问 PostgreSQL；`cmd/playpair-api/` 和 `internal/app/` 负责进程装配与生命周期管理。

- [`cmd/playpair-api/`](cmd/playpair-api/)：进程入口、信号处理和退出状态。
- [`internal/app/`](internal/app/)：装配 HTTP 与数据库资源，管理启动和关闭。
- [`internal/api/`](internal/api/)：路由、请求边界、认证中间件和 HTTP 错误映射。
- [`internal/users/`](internal/users/)：账号身份。
- [`internal/credentials/`](internal/credentials/)：注册、登录和密码凭据。
- [`internal/session/`](internal/session/)：会话签发、验证、到期和撤销。
- [`internal/profile/`](internal/profile/)：个人展示资料。
- [`internal/preference/`](internal/preference/)：偏好及其版本。
- [`internal/group/`](internal/group/)：小组和成员关系。
- [`internal/outing/`](internal/outing/)：两轮邀请、本次硬约束和基于已确定安排的运行时状态。
- [`internal/config/`](internal/config/)：启动配置解析和校验。
- [`internal/db/`](internal/db/)：数据库连接、查询 adapter 和生成代码。
- [`api/`](api/)：HTTP 契约及其生成配置。
- [`db/`](db/)：数据库迁移和 SQL 查询源文件。

Outing 的日期和空闲时段使用创建时指定的 IANA 时区；服务端返回该时区。
归档状态由安排的开始时间计算，不持久化，也不需要定时归档任务。
展示使用应用时钟，写入截止由数据库在取得锁后检查；部署时应保持应用与数据库时钟同步。
已确定安排目前只有数据库结构，方案确认的应用入口由后续工作交付。

## 文档索引

- [路线图](ROADMAP.md)
- [变更记录](CHANGELOG.md)
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
- [本仓库独有治理](AGENTS.md#本仓库独有治理)
- [通用工程约束](AGENTS.md#通用工程约束)

## 权威来源

- HTTP 契约：[`api/openapi.yaml`](api/openapi.yaml)。
- 数据库 schema：[`db/migrations/`](db/migrations/)。
- SQL 查询：[`db/queries/`](db/queries/)。
- 生成代码：[`internal/api/generated/`](internal/api/generated/) 和 [`internal/db/generated/`](internal/db/generated/)，不得手工修改。
