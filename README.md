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
- [`internal/outing/`](internal/outing/)：outing 访问权限、两轮响应、日期与安排、当前输入聚合及 Go 方案校验。
- [`internal/config/`](internal/config/)：启动配置解析和校验。
- [`internal/db/`](internal/db/)：数据库连接、查询 adapter 和生成代码。
- [`api/`](api/)：HTTP 契约及其生成配置。
- [`db/`](db/)：数据库迁移和 SQL 查询源文件。

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

## Outing 业务边界

组员可创建 outing；未响应时不存记录，读取时推导 pending。第一轮 Accept 显式填写预算、无障碍要求及空闲时间，Deny 清除这些输入。已有响应者离组后仍可访问，组织者离组后仍保留管理权。

组织者可修改日期和时长，时区固定。旧填写允许与新日期冲突：第一轮读取详情时返回动态 warnings，不持久化、不自动删除。成员可修改或忽略；时间聚合排除超出候选日期的区间，并按当前时长判断是否可用。第二轮继续展示第一轮填写，但不再返回该警告。

在创建 arrangement 前，组织者可读取当前决策输入聚合。参与者是已有第一轮 Accept 且输入完整的成员，不要求等待全组响应；离组但保留响应的成员仍参与。聚合使用最低每人整场预算、任一成员的无障碍要求、候选日期内的半开共同时间窗口和按 outing 时区计算的半小时开始时间。无参与输入、输入损坏、无共同时间和容量超限分别报告；聚合结果不持久化。软偏好（包括 `Dislikes`）不自动转为硬约束；显式排除项能力登记在 Beta [#28](https://github.com/Jason-skd/play-pair/issues/28)。

组织者手工填写 arrangement 后进入第二轮，成员只需 Accept/Deny；修改 arrangement 必须显式选择 `resetResponses`。打回通过清空第二轮选择保留记录，保证已离组成员仍能查看和重新响应。活动开始后只读。列表统一使用 `GET /api/v1/outings`（可按 `groupId` 过滤）；响应摘要统一查询两轮，可按阶段和状态过滤，私人填写仅本人和组织者可读。完整协议见 OpenAPI。

日期、第一轮响应及其空闲时间属于 outing；第二轮响应属于 arrangement。数据库删除 outing 时级联全部子数据，删除 arrangement 时只级联第二轮响应。用户和来源小组不是子数据，不级联删除；被引用的 user/group 目前禁止硬删除。成员关系不被 outing 外键引用，删除成员关系不删除 outing 数据。本轮不开放 outing/arrangement 删除、退组或账号注销 API。

权限、阶段和开始时间检查由 Go 事务执行，同一 outing 的命令通过行锁串行化，不使用 version 或幂等键。数据库用局部完整性 trigger 保证 Accept 至少有一段空闲、Deny 无空闲，并保留响应状态、字段完整性、主外键和唯一性约束；数值与日期范围、步长、文本长度和空闲段数量上限由 Go 校验。无自动邀请、阶段或不可修改 trigger。候选生成、推荐历史和完整 My Outings 产品视图留给后续 Issue；Go 校验契约供候选生成方调用，拒绝确定性冲突并列出待确认事项，不自动写入 arrangement。
