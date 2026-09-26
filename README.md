# PlayPair

面向已有朋友圈，综合成员时间、硬性约束和软偏好，生成群体聚会方案的 AI 辅助决策系统。

> [!NOTE]
> 项目后端骨架正在按 Issue #3 建立；业务功能尚未实现。

## 常用指令

当前已建立 Go module、配置、HTTP 探针和 PostgreSQL 连接。完整的启动和检查命令在自动化检查单元补齐。

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
