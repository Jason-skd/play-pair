# PlayPair 后端实施计划（Proposal v4）

## 1. 文档定位

本文只描述 PlayPair 后端仓库的技术实施路径。它服务于 `docs/proposal/Proposal_PlayPair_v4.tex` 中已经确定的 Alpha 和 Beta 交付，不重复课程规划、用户研究安排、成员分工和报告写作计划。

Proposal v4 是产品范围和用户可见行为的上位来源；本文把其中的行为转换为后端模块、数据、接口、事务和验证任务。本文中的“建议”表示后端落地所需、但 Proposal v4 没有唯一规定的技术选择；在开始对应实现前必须确认这些选择。

当前仓库已经建立 Go 服务、数据库迁移、OpenAPI 契约和生成检查。下面描述完整后端目标；具体完成状态以当前代码、测试和 Issue 为准。

## 2. 后端目标与边界

### 2.1 Alpha 必须交付的后端能力

1. 用户注册、登录和当前用户识别。
2. 创建小组、使用小组码加入小组，以及小组成员关系。
3. 创建 outing，邀请小组成员，并记录接受或拒绝。
4. 参与成员提交本次 outing 可用时间和已确认硬约束；软偏好从其持续维护的 profile 读取。
5. 从固定的 SCNU 附近活动/地点目录构造候选。
6. 在 Go 后端执行时间、预算、无障碍、明确排除项等硬约束过滤。
7. 通过统一的 `DecisionProvider` 对每个可行候选进行结构化评分。
8. 对可行候选排序，返回最多三个完整的“时间—活动—地点”方案。
9. 保存一次决策所使用的输入快照、目录版本、约束结果、提供者结果和最终排名。
10. 由发起人确认一个方案，并在 `My Outings` 中读取已确认 outing。
11. 让邀请状态和确认后的出席状态分开记录。

### 2.2 Beta 必须追加的后端能力

1. 根据最长共同可用窗口，通过独立的活动推荐 LLM 提出所需时长不超过窗口上限的目录活动。
2. 通过独立的 LLM API 为每个展示的排名方案生成基于决策记录的推荐文本。
3. 活动推荐或推荐文本服务失败时返回明确状态，并回退到 Alpha 候选列表界面，不能静默当作成功。
4. 记录成员投票、投票完成结果及其与 outing/候选方案的关系。
5. 将 `My Outings` 按 outing 日期提供 upcoming 和 historical 视图。
6. 对生成文本执行证据一致性检查，阻止文本声称目录、约束、偏好或成员事实中不存在的内容。

### 2.3 后端不负责的事项

- 前端草稿的长期保存；Alpha 的草稿由前端负责。
- 地图、Places API、联网搜索、个人旅行距离计算和自由生成地点。
- 自动重新规划、自动预订、支付、聊天、通知和开放式 Agent 循环。
- 让评分提供者创建候选、验证地点事实或覆盖硬约束。
- 在 Alpha 中提供模型内部推理或偏好级自然语言解释。
- 把 Beta 的推荐文本当作新的业务事实写回决策输入。

## 3. 建议的总体技术路径

### 3.1 服务形态

采用一个 Go 模块化单体服务，先完成一条可测试的 HTTP API 到领域用例再到持久化的调用链。Alpha 和 Beta 共用同一进程和同一领域模型，不拆分 Python 服务或独立微服务。

建议的目录责任如下：

```text
cmd/playpair-api/           进程入口；只调用 internal/app，不承载业务逻辑
internal/app/                组合根、生命周期和依赖装配
internal/api/                Gin router、HTTP middleware、handler 和协议映射
internal/users/              账号身份
internal/credentials/        登录邮箱、密码凭据、注册与登录用例
internal/session/            会话签发、校验和撤销
internal/profile/            显示名、昵称与独立资料版本
internal/preference/         软偏好、硬约束默认值与独立偏好版本
internal/group/              小组、成员关系；domain/logic/dto/ports
internal/outing/             outing、邀请、成员输入；domain/logic/dto/ports
internal/catalog/            固定目录及版本；domain/logic/dto/ports
internal/decision/           候选、运行、评分和排序；domain/logic/dto/ports
internal/voting/             投票、平票辅助和确认；domain/logic/dto/ports
internal/db/                 PostgreSQL/sqlc 生成代码和 repository adapter
internal/jev/                Jev provider adapter
internal/llm/                活动推荐、推荐文本和普通 LLM adapter
internal/config/             typed 配置解析
internal/errs/               仅放跨边界错误分类/错误码，不放业务规则或万能 helper
internal/testutil/            测试 fixture 和外部依赖替代实现
db/migrations/               goose 管理的数据库 schema 变更
db/queries/                  sqlc 查询的唯一权威 SQL 源文件
api/openapi.yaml             对外 HTTP 契约的唯一权威来源
sqlc.yaml                    sqlc 生成配置和 schema/query 路径
```

依赖方向固定为：

```text
internal/api (Gin) -> feature logic -> feature domain
                         -> feature ports <- internal/db, internal/jev, internal/llm
internal/app (composition root) -> all concrete adapters and feature services
```

所有业务功能块直接位于 `internal/` 下；不再使用 `internal/modules`、`internal/adapters` 或 `internal/platform` 作为第二层总目录。每个业务包内部按文件承担 domain、logic/application、DTO 和 ports/protocol；只有确实需要独立编译边界时才再拆子包。`internal/api` 是 Gin 的唯一入站 adapter，外部能力分别由 `internal/db`、`internal/jev` 和 `internal/llm` 隔离。DTO 属于协议边界，不能进入 domain；sqlc 生成类型不能进入 feature logic/domain；provider client 类型不能进入业务用例。接口由消费者定义，具体 adapter 由 `internal/app` 注入。

这里的“包”不是新的 `go.mod`。本仓库使用一个 Go module；Go 官方建议使用 `internal/` 隔离不对外承诺的支持包。只有需要独立版本、独立构建或独立部署时才拆分 Go module。Gin、PostgreSQL/sqlc、goose 和 provider client 只在对应边界出现；标准库 `net/http` 不需要再包成一个泛化 client 模块。上述 flat 结构是本项目的可读性和所有权选择，不冒充 Go 语言强制规则。

### 3.2 已确定的实施基线

| 事项 | 路径 | 边界 |
|---|---|---|
| 入站 HTTP API | Gin 处理路由、JSON、认证中间件和错误映射 | Gin 是服务端框架，不承担对外 API 客户端调用 |
| 对外 API 调用 | 优先使用 Go `net/http.Client`；若 Jev/LLM 供应商提供适配当前接口、维护可靠的官方 Go SDK，再按实际能力评估采用 | provider adapter 负责请求、超时、取消、响应校验和错误转换；无需为了发 HTTP 请求引入另一个服务端框架 |
| 数据访问 | PostgreSQL + sqlc | SQL 查询写在 `db/queries/`，由 sqlc 生成有类型的 Go 调用；事务和业务编排仍由应用层控制 |
| schema 变更 | goose CLI/tool + 版本化 SQL migrations | goose 负责迁移，不作为业务层 repository 依赖；先写 migration，再根据最终 schema/查询运行 sqlc；生成代码不手工编辑 |
| 时间 | 写请求带 IANA 时区名和本地相对时间；逻辑层解析为 UTC 瞬时点；数据库只保存瞬时点；GET 带客户端时区用于响应转换 | 用户界面不展示时区设置；数据库不保存 outing 固定时区 |

HTTP 契约采用 `api/openapi.yaml` 作为唯一公共权威源；生成的 transport types/server binding 只能通过生成命令更新，feature 包仍使用自己的 application command/result。认证采用同源服务端 Cookie Session，API 使用 `/api/v1` 前缀；本地集成测试使用独立 PostgreSQL 测试库，不另造第二份 HTTP schema。

### 3.3 时间与时区契约

浏览器可以通过 `Intl.DateTimeFormat().resolvedOptions().timeZone` 取得当前环境的 IANA 时区名，例如 `Asia/Shanghai`。浏览器不会在普通 HTTP 请求中自动发送可靠的时区字段；`Accept-Language` 是语言偏好，单独的 UTC 偏移也无法表达夏令时规则。前端请求封装应自动读取这个值，用户无需设置或看到时区配置。

写请求发送“本地相对时间 + IANA 时区”，逻辑层使用该时区解析成本次事件的真实 UTC 瞬时点，然后只把瞬时点写入数据库。数据库不保存 outing 创建时的时区，也不把服务器时区当作业务默认值。GET 请求通过 `X-Client-Timezone` header 发送当前浏览器的 IANA 时区，后端在响应 DTO 生成阶段把已保存的瞬时点转换成该时区的本地时间。只有包含本地化时间字段或按本地日期分组的响应才要求该 header；不需要本地化的资源可以返回 UTC 规范表示。依赖该表示的 GET 响应应设置 `Vary: X-Client-Timezone`；缺少 header 时返回明确错误，不偷偷使用服务器时区。

请求体的最小示例（最终字段以 `api/openapi.yaml` 为准）如下：

```json
{
  "timeZone": "Asia/Shanghai",
  "availableWindows": [
    {
      "startLocal": "2026-10-12T19:00:00",
      "endLocal": "2026-10-12T22:00:00"
    }
  ]
}
```

前端在组装写请求时自动读取 `Intl.DateTimeFormat().resolvedOptions().timeZone`，将结果和本地日期时间写入 schema；用户不需要看到或填写该字段。后端验证 IANA 时区名、本地时间格式、时间区间和夏令时边界，并在逻辑层完成转换。对不存在的本地时刻直接拒绝；对夏令时回拨造成的歧义时刻，首版也拒绝，除非协议增加明确的 UTC offset 选择。PostgreSQL 推荐使用 `TIMESTAMPTZ`：它保存归一化后的瞬时点，不保存原始时区；sqlc 生成的 Go 类型也只代表该瞬时点。这里不建议使用 `timestamp without time zone`，因为数据库无法阻止调用方把本地时间误写进去。若未来要支持未绑定具体日期的“每周某时段”规则，必须另建本地时间规则模型；当前 Alpha 的具体 outing 不需要它。

Alpha 地点集中在 SCNU 附近，但这里不从地点推断浏览器时区。若前端无法读取有效时区，涉及时间的写请求应返回明确校验错误；离线演示可以由前端明确配置 `Asia/Shanghai`，并在演示说明中标识这一固定假设。

### 3.4 出站 API 客户端选型

Gin 解决入站请求的路由和响应，不是通用的出站 API 客户端。provider adapter 应持有可复用的 `*http.Client`，从 `context.Context` 获得取消信号，并明确设置连接、请求、响应读取和总超时；不能每次调用临时创建 client，也不能把重试交给 Gin。

推荐按以下顺序选择：

1. 默认使用标准库 `net/http`。PlayPair 的 Jev/LLM adapter 只需构造 JSON、设置认证头、检查状态码、限制响应体、解码结构和映射错误，标准库已经能完整表达这些边界，依赖面最小。
2. 如果供应商没有 OpenAPI/官方 SDK，且多个 provider 都需要大量相同的 JSON、header、query 和响应处理，可以评估 `resty/v2` 作为薄客户端封装。它只改善调用书写，不替代 adapter 的超时、错误、证据校验和日志责任；不要同时再包一层 Gin 风格客户端抽象。
3. 只有在确实需要可配置退避、重试计数和重试钩子时，才评估 `hashicorp/go-retryablehttp`。重试必须按 provider 错误分类，限制次数，并确认请求体可安全重放；不能对未知副作用请求盲目重试。
4. 如果供应商提供稳定的 OpenAPI 文档，优先评估 `ogen` 或 `oapi-codegen` 生成类型安全 client。没有稳定规范时不提前引入生成器，也不手写一套伪 SDK。

因此，当前实施计划不把出站依赖锁死为某个第三方库：先以 `net/http` 完成 `internal/jev` 和 `internal/llm` adapter；只有重复代码或供应商规范证明需要时，再为出站边界增加一个依赖。无论采用哪种 client，业务层只依赖 feature 自己的 provider port，不依赖第三方 client 类型。

### 3.5 Go 实践门禁

当前仓库还没有代码，下面这些不是对现有实现的缺陷报告，而是开始编码前必须加入设计的门禁：

1. **模块边界和组合根。** 每个 feature logic 只依赖本包定义的最小 port；`cmd/playpair-api` 不创建业务对象，`internal/app` 统一装配生命周期、配置和 adapter。禁止在 `init` 或全局变量中注册依赖。Go 官方资料支持使用 `internal/` 隔离不对外承诺的包，但并不规定 Clean Architecture 的目录名称；这里的 flat feature layout 是本项目选择。
2. **聚合根和事务。** 聚合边界必须按下面第 4.3 节的所有权实现；repository 方法按用例需要定义，不提供让上层随意拼装半个聚合的通用 CRUD。一个事务只负责一个明确的状态转换和其不变量；外部网络调用永远不在该事务内。
3. **DTO 映射和协议隔离。** `internal/api` 的 HTTP DTO 负责协议格式校验并映射为 feature application command；response DTO 由 application result 映射生成。Gin 的 `*gin.Context`、HTTP 状态码、sqlc 类型和数据库错误不能穿过 adapter 边界。
4. **错误模型。** domain/application 定义可判别的业务错误（未认证、无权限、输入无效、状态冲突、无可行候选、provider 失败等）；adapter 用 `%w` 保留根因，调用方用 `errors.Is/As` 判断身份；HTTP 层集中做一次错误到状态码/错误体的映射。禁止用字符串比较错误，也禁止把 provider 原始响应直接返回给用户。
5. **不可变快照和版本。** `PreferenceProfile`、接受邀请时授权的硬约束、目录版本、provider 配置和每次 `DecisionRun` 的输入都要有版本或快照。profile 更新不能修改历史决策；任何重算都创建新运行。
6. **时间、金额和 ID 值对象。** 领域层使用 UTC `time.Time` 表示真实瞬时点；本地时间和 IANA 时区只在输入/输出 adapter 解析。金额使用整数最小货币单位和明确币种，禁止 `float64`；实体 ID 使用有语义的类型，禁止在业务层到处传裸字符串。
7. **外部调用生命周期。** 所有 provider 调用接收 `context.Context`，使用共享且有界的 HTTP client，设置超时、响应体上限、取消和受控重试。fallback 是 application 层的显式 policy，不藏在某个 client 的重试实现里；数据库事务不跨越外部网络调用。
8. **长运行决策。** `DecisionRun` 必须有明确的状态和超时边界。Alpha 首版使用带 request context 和总 deadline 的有界同步调用；成功才返回排名，超时返回可判别的 timeout/dependency failure，并保留已提交的运行记录。只有在引入有持久化队列、worker、租约、重试和幂等键后，才允许改为 `202 + run ID`；不要在 handler 中启动无归属 goroutine，也不要用固定 sleep 等待结果。
9. **配置和进程生命周期。** 环境变量只在 bootstrap 解析为不可变 typed config；依赖通过构造函数注入。`http.Server`、数据库池和 provider client 由统一的启动/关闭流程管理，收到取消信号后执行有界 graceful shutdown。
10. **认证和安全。** 认证方式、密码哈希、session cookie、CSRF 和登录限流在实现认证边界前各写一条设计记录；cookie、凭据和 Gin context 只在 `internal/api`/认证 adapter 处理，领域模块只接收已认证的 actor 和授权结果。安全机制不能通过把 HTTP 类型传入领域层实现。
11. **可观测性。** 使用结构化日志（优先 `log/slog`）记录 request ID、run ID、provider/model、延迟和失败类别；禁止记录密码、session、provider key、完整用户偏好和原始 LLM 敏感请求。provider fallback 和 Alpha UI 回退必须可检索。
12. **契约和测试。** HTTP 契约、数据库 migration、sqlc query 和 provider schema 各有唯一权威来源。单元测试验证纯 domain/application 规则，PostgreSQL 集成测试验证真实约束和事务，`httptest` 验证 Gin adapter，provider fixture 验证超时/fallback/非法响应；不要用 mock 替代数据库事务语义。

## 4. 领域模型和状态

字段名是实现建议，最终以数据库 schema 和 API 契约为准。

### 4.1 核心实体

- `User`：账号身份和创建时间，不包含登录邮箱、展示资料、密码或会话状态。
- `Profile`：显示名、可选昵称和独立版本；与 preference 分别维护。
- `PasswordCredential`：与用户一对一的密码凭据，独立保存登录邮箱、密码哈希和更新时间。注册在同一事务中创建用户、凭据、资料和会话；密码登录联表读取凭据，session 验证只查询会话和用户。
- `PreferenceProfile`：用户持续维护的软偏好、可作为 outing 接受快捷填入项的个人硬约束默认值。软偏好不要求每次接受邀请时重填；硬约束默认值只有在接受具体邀请时向用户展示并经明确确认后，才成为该 outing 的输入。推荐文本从独立 Profile 获取展示资料，昵称/外号优先，未配置时使用显示名。
- `Group`：小组标识、名称、加入码及创建者。
- `GroupMembership`：用户与小组的关系、角色（initiator/member）、状态和加入时间。
- `Outing`：小组下的活动协调实例、创建者、目标日期范围、Alpha 中发起者选择的活动时长、状态和确认方案；时间字段以数据库中的 UTC 瞬时点表示。Beta 的活动时长可以由每个候选的目录活动建议，不能假设所有候选共享一个时长。
- `Invitation`：outing 对某个小组成员的邀请状态、响应时间，以及接受时确认的硬约束输入快照。
- `MemberInput`：某个参与成员针对一次 outing 的可用时间、已授权硬约束、当前软偏好快照和提交版本。软偏好来自用户 profile，不要求重复填写。
- `CatalogVersion`：固定目录的版本、来源说明和核验时间。
- `CatalogItem`：活动或地点、地址、价格、开放时间、标签、无障碍和其他限制。
- `DecisionRun`：一次候选构造和评分运行的输入快照、最长共同可用窗口、目录版本、活动推荐/评分 provider 标识、状态和时间。
- `Candidate`：一次运行中的完整时间—活动—地点组合及其活动时长、约束检查结果、provider 评分和排名。
- `Recommendation`：Beta 中针对候选生成的文本、证据引用、模型版本、校验状态和失败信息。
- `Vote`：成员对候选的投票及投票版本；不覆盖邀请响应或最终确认状态。
- `Attendance`：确认后成员对实际出席的记录，与邀请响应分离。

决策和推荐需要保存不可变运行快照；后续成员修改输入时开启新的 `DecisionRun`，不要原地覆盖已经用于解释或投票的历史结果。

### 4.2 建议的状态转换

状态名称需在实现前确认，但必须先形成显式状态机：

```text
Outing: draft -> inviting -> collecting_input -> ready -> confirmed
                                  \-> cancelled
DecisionRun: requested -> filtering -> scoring -> ranked
                         \-> failed | timed_out
ActivityPlan: requested -> generated -> validated
                           \-> failed -> alpha_fallback
RecommendationText: requested -> generated -> verified
                                  \-> failed -> alpha_fallback
Invitation: pending -> accepted | declined
Attendance: unknown -> attending | absent
VoteSession: open -> closed
Vote: draft -> submitted | withdrawn
TieBreak: not_needed | requested -> scored -> initiator_confirmed
```

任何状态转换都由对应的 application use case 执行并在事务中校验。不能依靠前端隐藏按钮来保证顺序。

### 4.3 聚合边界和事务所有权

聚合不是实体清单，而是需要在一次事务中共同维护不变量的最小边界。首版按以下边界实现：

| 聚合根 | 负责的不变量 | 典型事务 |
|---|---|---|
| `Group` | 加入码唯一、成员角色和有效状态 | 建组、入组、角色变更 |
| `Outing` | 发起者、邀请生命周期、输入提交资格和 outing 状态 | 创建 outing、响应邀请、确认成员输入、取消 outing |
| `DecisionRun` | 运行快照、候选约束结果、provider 尝试和排名不可变 | 创建运行快照；每次外部尝试完成后的状态写回 |
| `VoteSession` | 目标运行、预期投票成员、关闭规则和原始选票 | 提交/替换选票、关闭投票、记录平票辅助 |
| `Attendance` | 确认后的出席状态 | 更新出席，不回写邀请状态 |

`Invitation` 和 `MemberInput` 是 `Outing` 的子资源，不能被另一个用例绕过 outing 权限单独修改；`Candidate` 和 `RecommendationText` 是 `DecisionRun` 的子资源。跨聚合只保存语义化 ID，在应用层按用例读取，不把一个聚合的内部结构作为另一个聚合的参数。外部 provider 调用分为“提交快照/状态”和“写回结果”两个数据库事务，中间不持有数据库事务。

## 5. Alpha 决策链路

### 5.1 输入收集和快照

1. 校验用户属于 outing 所在小组，且只允许有效邀请对象提交该 outing 的输入。
2. 接受邀请时读取 `PreferenceProfile` 的硬约束默认值，将其展示给用户；用户必须明确确认默认值或修改后确认。服务端只把确认后的值写入该 outing，不能因 profile 默认值存在而静默授权。
3. 确定本次运行的参与成员集合：使用所有已接受邀请、已确认本次 outing 硬约束且提交完整可用时间的成员。成员的软偏好从当前 profile 读取，不要求在接受邀请时重复填写。
4. 校验各成员输入的 IANA 时区和本地日期时间，在该请求的时区下解析，再规范化为 UTC 时间点和结构化约束值。
5. 将参与成员集合、每个成员的已授权硬约束、软偏好 profile 版本、目录版本和算法配置复制到 `DecisionRun` 快照中。决策运行开始后 profile 更新不改变本次运行；新的偏好需要新的运行才能生效。
6. 输入不完整时返回可区分的业务错误，不调用 provider。

### 5.2 候选构造和硬约束过滤

候选构造必须完全由 Go 后端完成：

1. 根据参与成员的可用时间产生共同窗口；候选开始时间按 30 分钟粒度枚举，并计算最长共同可用窗口。
2. 从固定 `CatalogVersion` 读取活动/地点及其已核验的典型活动时长和开放时间。
3. Alpha 使用发起者选择的统一活动时长。Beta 将最长共同可用窗口和目录活动候选交给活动推荐 LLM，要求推荐活动的所需时长小于或等于该窗口上限；LLM 只能选择目录中的活动，不能生成新活动或新时长事实。
4. Go 根据每个活动的实际时长、开放时间和每个共同窗口，筛选可用的时间—活动—地点组合；活动推荐失败时进入 Alpha 版由发起者选择活动时长的界面。
5. 对预算、开放时间、无障碍、明确排除项和其他已确认硬约束逐项检查。
6. 为每个候选保存逐项约束结果、活动时长和失败原因；只把所有硬约束通过的候选交给 `DecisionProvider`（Alpha 的 Jev/LLM/Mock 评分 provider）。

没有可行候选时返回可处理的领域结果，并明确要求用户调整输入；不返回 provider 生成的替代地点，也不静默放宽约束。候选数量上限和组合裁剪顺序属于容量策略，由 typed config 给出有界、可复现的默认值；裁剪不能改变硬约束语义，最低可行候选数不足时直接返回无可行候选。

### 5.3 评分、排序和 Top-3

`DecisionProvider` 只接收结构化候选、参与成员软偏好和允许使用的目录字段，并返回与候选 ID 一一对应的结构化结果。provider 返回值至少需要包含：provider 名称、模型/版本、候选分数、可选的概率或置信度、请求耗时和错误状态。

排序由 Go 的 `RankingEngine` 完成。它负责：

- 验证 provider 是否为每个可行候选返回了结果；
- 处理分数缺失、重复候选、非法范围和超时；
- 按固定、可复现的规则处理并列；
- 生成最终排名和最多三个展示方案；
- 将使用的权重、阈值和配置版本写入 `DecisionRun`。

Jev 是 Proposal v4 的目标评分 provider；普通 LLM provider 和 Mock provider 必须实现同一评分接口。活动推荐 LLM 使用单独的 `ActivityRecommendationProvider` 接口，不能把活动推荐和候选评分混成一个不透明调用。评分 provider 不得创建候选或修改约束结果。provider fallback 链由 typed config 注入 application service；client 内部的同一 provider 重试不能改变 provider 选择。按配置链逐个尝试，主 provider 失败后对同一批候选重新执行整次评分，再使用下一个 provider；不把一次运行中不同 provider 的部分结果混合排序。所有 provider 都失败时，`DecisionRun` 进入 `failed`，不产生看似成功的 Top-3。每次尝试带同一个 run ID 和唯一 attempt ID，重试写回必须幂等。

每次运行记录 provider 尝试顺序、失败原因、最终采用的 provider、模型/系列和版本。前端在结果附近用小字标注实际来源，例如 `Scored by Jev` 或 `Scored by LLM: ds`；切换发生时也能从决策记录中追溯。Provider 返回的公共评分 schema 保持一致，模型标签是来源信息，不改变排序契约。

### 5.4 确认和 My Outings

只有 outing 发起人可以确认一个当前决策运行中的方案。确认操作需要重新检查：候选仍属于该 outing、目录项仍有效、决策运行未被替换、outing 尚未确认或取消。成功后在同一事务中写入 outing 的确认方案和确认时间。

`My Outings` 只读取用户有权限看到的已确认 outing。Alpha 可先提供一个合并列表；Beta 增加按 outing 日期划分 upcoming/historical 的查询和排序。出席变化写入 `Attendance`，不能改写 `Invitation`。

## 6. Beta 推荐文本和投票

### 6.1 Beta 活动推荐和 RecommendationProvider

Beta 的活动推荐和推荐文本是两个不同阶段：

1. `ActivityRecommendationProvider` 根据最长共同可用窗口和固定目录，提出活动及其目录时长上限内的时长建议；Go 重新校验目录、时长、开放时间和成员硬约束，再构造可行候选。
2. `DecisionProvider`（目标为 Jev）对这些完整可行候选进行评分和排序。
3. `RecommendationProvider` 再针对展示的候选生成证据约束的自然语言文本。

活动推荐或推荐文本服务失败时，后端保留已经生成的可行候选和排名；前端回退到 Alpha 版候选列表界面，允许发起者继续确认，不把推荐文本当成确认的硬前置条件。失败状态、实际 provider 和回退原因仍写入记录。

推荐文本 provider 与评分 provider 分离。它接收一个候选的结构化证据包，证据包只包含：

- 候选的时间、活动、地点和目录版本；
- 通过的硬约束摘要；
- 允许披露的软偏好匹配和未满足项；
- 候选排名及可公开的比较信息；
- 可选的成员昵称/外号或显示名，以及对应偏好证据。

应用层决定哪些成员信息可以进入证据包；用户配置的昵称/外号优先于显示名。LLM 只能决定是否在文字中使用被允许的成员信息。生成后必须执行结构化事实校验，并记录校验失败原因。无法证明的句子应使推荐进入失败或回退 Alpha 界面，而不是直接展示。

推荐文本 API 是 Beta 推荐路径的必需依赖。API 超时、限流、认证失败、格式错误和证据校验失败都要映射为可观察的服务状态，并保留根因供日志和决策记录使用。

### 6.2 投票

Beta 使用可排序投票。发起人参与投票，但不要求所有已接受成员都提交选票。发起人可以在认为信息足够时主动确认或关闭投票；如果所有预期投票成员都已提交，投票阶段也标记为完成。全员完成不自动替发起人确认，仍保留发起人确认边界。未投票成员记录为未提交，不伪造为同意或弃权票。投票关闭后，若存在并列候选，系统只在并列候选之间请求 AI 辅助决策，并记录实际 provider、模型和输入证据；AI 不能自动确认，最终仍由发起人拍板。

排序投票的具体聚合算法（例如名次积分或即时决选）仍需在 Beta 设计时固定，因为相同选票在不同算法下可能产生不同赢家。投票模型应保存原始名次，不能只保存聚合结果，以便重算和解释。

## 7. HTTP API 摘要

以下是便于前后端拆分的最小接口集合。它是 OpenAPI 文件的阅读摘要；路径、字段、状态码、错误体和本地时间字段必须以 `api/openapi.yaml` 为唯一权威，并通过固定生成命令更新 transport binding，不能同时手工维护另一份“草案契约”：

| 能力 | 接口摘要 | 主要后端责任 |
|---|---|---|
| 认证 | `POST /api/v1/auth/register`、`/login`、`/logout`、`GET /me` | 身份、session、错误映射 |
| 偏好 | `GET /preferences`、`PUT /preferences` | 维护软偏好和可用于接受邀请快捷填入的硬约束默认值 |
| 小组 | `POST /groups`、`POST /groups/{id}/join`、`GET /groups/{id}` | 加入码、成员权限、角色 |
| outing | `POST /groups/{id}/outings`、`GET /outings/{id}` | 创建、读取、状态检查和发起者选择活动时长 |
| 邀请 | `POST /outings/{id}/invitations/{member}/respond` | 接受/拒绝及幂等性 |
| 输入 | `PUT /outings/{id}/input` | 接受时确认硬约束默认值、提交本次 outing 可用时间和修改后的硬约束 |
| 决策 | `POST /outings/{id}/decisions`、`GET /decisions/{id}` | Alpha 首版有界同步；运行、状态、Top-3 和记录摘要 |
| 确认 | `POST /outings/{id}/confirm` | 发起人授权、并发检查、事务提交 |
| 我的 outing | `GET /me/outings?view=upcoming\|historical` | 权限过滤和日期视图 |
| Beta 推荐 | `GET /candidates/{id}/recommendation` | 推荐文本状态和证据校验状态 |
| Beta 投票 | `POST /outings/{id}/votes`、`POST /outings/{id}/votes/close`、`GET /outings/{id}/votes` | 排序选票、发起人提前关闭、AI 并列辅助和最终结果 |

所有写接口应使用明确的资源版本或状态检查和幂等键（适用于可能重试的创建/运行请求），重复请求不能产生重复邀请、重复确认或重复投票。错误响应使用稳定的错误码，至少区分未认证、无权限、输入无效、状态冲突、无可行候选、provider 失败、超时和内部依赖失败。Alpha 决策成功才返回结果；请求 deadline 到期不伪装成已接受的后台任务。

## 8. 持久化、事务和并发

- schema 由 goose migrations 唯一维护；外键、唯一键、非空、枚举/检查约束放在数据库能表达的边界。查询由 `db/queries/` 维护并交给 sqlc 生成 Go 代码，生成产物不手工修改。goose 只作为迁移工具或受控启动步骤，不进入 feature logic 的运行时依赖。
- 创建 outing、生成决策运行、确认方案和提交投票分别定义事务边界。
- `internal/db` 负责 sqlc 生成代码、连接池和按 feature port 实现的 adapter；feature 不导入 sqlc 包，也不暴露通用 `Repository`。事务能力通过明确的 adapter 方法或 transaction callback 暴露，不能让 application 层直接拼接 SQL。
- 决策运行保存输入和配置快照，确保目录或成员输入变化后仍能复查历史结果。
- 决策运行保存每个参与成员的硬约束授权结果、软偏好 profile 版本和快照内容；profile 后续更新不改写历史运行。
- 确认方案使用乐观版本号或等价的并发检查，避免两个请求同时确认不同候选。
- 所有数据库时间比较使用保存的 UTC 瞬时点；响应层才根据 `X-Client-Timezone` 生成用户本地时间和日期分组。
- Provider 自动切换产生的每次尝试、失败原因和最终模型来源都写入决策记录；结果 DTO 只展示安全的 provider/model 标签。
- 排序投票保存原始名次、提交者和关闭原因；AI 并列辅助和发起人最终确认分别保存，不能用聚合赢家覆盖原始选票。
- provider 调用不应长时间占用数据库事务；先提交运行状态和快照，再在有界超时内调用外部服务，最后以状态更新事务写回结果。请求取消或 deadline 到期后，用独立且有界的 cleanup context 将运行标记为 `timed_out`/`failed`，不能留下永久 `scoring` 状态。
- 外部调用需要 context 取消、超时、有限重试策略和脱敏日志。重试不能造成重复决策记录或重复推荐文本。
- 密码、session、provider 凭据和原始 LLM 请求中的敏感字段不得进入普通业务日志。

## 9. 验证计划

### 9.1 单元和领域测试

- 时间窗口交集、跨午夜和时区规范化。
- 预算、开放时间、无障碍和排除项逐项过滤。
- 缺失输入、空参与集合和无可行候选。
- profile 硬约束默认值必须经过明确确认；未确认的成员不能进入决策，软偏好 profile 更新后只影响新的决策运行。
- 排序并列、非法 provider 响应、分数缺失和配置版本。
- provider 自动切换、实际模型标签和所有 provider 失败。
- 所有状态转换、权限检查和重复请求幂等性。

### 9.2 存储和组件测试

使用真实 PostgreSQL 验证 goose migration、sqlc 查询、外键、唯一性、事务回滚、版本检查和查询过滤。使用可控 provider fixture 验证 Jev/LLM/Mock 的相同契约、超时、限流、错误和返回格式校验。

### 9.3 HTTP 和端到端测试

至少覆盖一条完整 Alpha 路径：注册/登录 -> 建组/入组 -> 创建 outing -> 邀请响应 -> 提交输入 -> 生成决策 -> 查看最多三个可行方案 -> 发起人确认 -> `My Outings`。

另需覆盖：

- 任何展示候选都不违反参与成员硬约束；
- 无共同可行时间和无可行目录项；
- provider 不可用、超时、返回非法结构；
- Beta 活动推荐只选择目录活动，活动时长不超过最长共同可用窗口，并能按 30 分钟粒度筛出实际时间段；活动推荐失败回退到 Alpha 界面；
- 非成员、非发起人和已过期状态的访问；
- Beta 推荐 API 失败和证据校验失败；
- 投票重复提交、发起人提前关闭、部分成员未投票、排序聚合、平票 AI 辅助和发起人最终确认；
- upcoming/historical 日期边界。

### 9.4 Go 工具链门禁

每个实现阶段至少运行 `gofmt`、`go vet ./...`、`go test ./...` 和 `go test -race ./...`（若该阶段引入并发 worker）；sqlc 生成检查、migration 校验和 OpenAPI 生成检查必须在 CI 中使用同一版本工具。静态分析工具可作为 CI 增强项，但不能用未锁定版本的本地结果替代构建和测试。

## 10. 分阶段实施顺序

### 阶段 A：契约和骨架

交付 Go module、OpenAPI 权威文件及其可重复的生成命令、Gin API 骨架、配置加载、HTTP 错误格式、数据库连接、goose migration、sqlc 配置与查询、认证边界、领域错误和依赖装配。此阶段不接外部 AI。

完成证据：服务能在干净数据库启动；migration 可重复执行；认证和健康检查有 focused tests；README 的命令和文档索引与实际文件一致。

### 阶段 B：Alpha 业务链路

按以下依赖顺序实现：

1. User、PreferenceProfile、Group、Membership 和权限；
2. Outing、Invitation、硬约束确认和响应幂等性；
3. MemberInput 规范化和完整性校验；
4. CatalogVersion/CatalogItem 导入和读取；
5. 候选构造与硬约束过滤；
6. DecisionProvider 接口、Mock provider、自动切换、排序和决策记录；
7. Alpha 结果读取、发起人确认和 `My Outings`；
8. Jev provider 适配器及其真实服务测试替代方案。

阶段出口是 Proposal v4 Objective 2 的完整 Alpha 流程，不以单独的 provider 调用成功作为完成标准。

### 阶段 C：Beta 推荐文本、投票和视图

1. `ActivityRecommendationProvider`、最长共同可用窗口输入、目录活动时长校验和候选筛选；
2. 固定推荐证据包、昵称/外号优先的允许披露字段；
3. `RecommendationProvider`、LLM API 适配器、失败状态、Alpha 界面回退和证据校验；
4. 投票 schema、接口和确定后的完成规则；
5. `My Outings` upcoming/historical 查询；
6. Beta 端到端和服务失败测试。

阶段出口是 Proposal v4 Objective 3 的完整推荐文本和投票路径。

### 阶段 D：冻结和评价支持

冻结 schema、目录版本、provider 配置和演示数据；补齐决策记录导出/读取、测试证据、日志脱敏、部署说明和用户手册所需的运行命令。规则基线、Jev 和普通 LLM 的比较属于评价工作，不应反过来改变 Alpha 核心链路。

## 11. 决策状态

### 11.1 已由当前讨论确定的 Alpha 规则

- 一次决策使用所有已接受本次邀请、明确确认本次 outing 硬约束并提交完整可用时间的成员。
- 用户的软偏好属于持续维护的 `PreferenceProfile`，不要求每次接受邀请时重填；决策开始时读取并快照参与成员当前 profile。
- 用户 profile 中的硬约束只是接受邀请时的快捷填入默认值。界面必须展示默认值并取得明确确认；未经确认不得进入 outing 分析。接受后可修改本次 outing 的硬约束，不改写 profile 默认值。
- 成员可用时间使用 `[startLocal, endLocal)`；候选开始时间粒度为 30 分钟。
- Alpha 活动时长由发起者选择；Beta 的活动推荐 LLM 可直接为目录活动提出不超过最长共同可用窗口的时长，Go 校验后进入候选构造。
- Beta 先计算最长共同可用窗口，再由 LLM 推荐时长不超过窗口上限的目录活动；Go 筛选实际可用时间段后交给 Jev 评分。
- 活动推荐或推荐文本失败时保留可行候选，回退到 Alpha 版候选列表界面，仍允许发起者确认。
- 推荐文本可以引用成员姓名；如果 profile 配置了昵称/外号，优先使用昵称/外号。
- Alpha 硬约束采用人均预算、无障碍要求、明确排除项和目录开放时间；首版预算使用人民币。
- Provider 允许自动切换。切换按 provider 链重跑整批候选，不混合不同 provider 的部分分数；结果显示实际使用的 provider/model 小标签，并在决策记录保存尝试和失败原因。

### 11.2 仍需在 Beta 前确定的事项

**排序投票的聚合算法。** 已确定成员可以提交排序选票、发起人参与、非所有成员必须投票、发起人可以提前关闭投票、平票交给 AI 辅助且最终由发起人确认。仍需选择名次积分（如 Borda）或即时决选等聚合方法，因为它们可能对同一组选票产生不同赢家。后端会保存原始排序，避免只能看到聚合结果。

**认证会话形态（已确定）。** 首版通过统一对外 origin 提供网页和 `/api`，使用服务端 Cookie Session。Cookie 使用 HttpOnly、Secure 和 SameSite 防护，会话固定七天并可由退出撤销；写请求执行跨源检查和自定义请求头校验，登录与注册限流。认证机制只存在于 API/认证 adapter，不渗透到 feature domain。

### 11.3 可按工程默认值处理的事项

最低参与人数默认设为 2；稳定错误码、provider 链配置、目录项 `Asia/Shanghai` 时区、夏令时无效/歧义本地时间拒绝、认证和权限的常规状态检查均可由工程默认处理，并在对应设计记录中留下依据。session cookie 与 bearer token 仍按上面的认证决策记录确定。推荐文本失败回退 Alpha 界面且仍可确认；全员投票完成只关闭投票阶段，不自动替发起人确认。若后续产品需求要求单人 outing、跨城市场所或自动接受 LLM 时长，再单独扩大契约。

每项 Beta 决策在实现前形成简短 API/schema 设计记录，写明触发场景、采用方案、关键代价和验证例。能够由已确认边界推导的低风险细节由后端直接处理。

## 12. 依据与判定层级

本计划把可查的外部事实和本项目选择分开：

- **Go 官方模块布局**（[Organizing a Go module](https://go.dev/doc/modules/layout)）说明 `internal/` 用于不希望被其他模块依赖的支持包，并以目录形成 package；它没有规定必须使用某种领域驱动目录。本计划的 `internal/<feature>` flat 结构是所有权和可读性选择。
- **Go 官方编码和错误资料**（[Effective Go](https://go.dev/doc/effective_go)、[Code Review Comments](https://go.dev/wiki/CodeReviewComments)、[Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors)）支持小接口、明确错误链、避免字符串比较错误和保持包边界清晰。它们不要求使用某个 web framework 或 repository 模式。
- **`context` 与 HTTP 生命周期**（[`context` package](https://pkg.go.dev/context)、[`http.Server.Shutdown`](https://pkg.go.dev/net/http#Server.Shutdown)）支持把请求取消和 deadline 传入下游，并在进程退出时做有界 graceful shutdown；因此 provider 调用不能脱离请求/worker context，服务也不能靠无归属 goroutine 收尾。
- **数据库与 sqlc**（[Go database guide](https://go.dev/doc/database)、[sqlc transactions](https://docs.sqlc.dev/en/latest/howto/transactions.html)）支持使用显式事务边界，并通过生成的类型安全查询减少手写扫描/参数错误；它们不要求把 sqlc 类型暴露给领域层。
- **goose 官方说明**（[goose README](https://github.com/pressly/goose)）把 goose 定义为数据库迁移工具，因此本计划把 migration 作为 schema 权威来源，而不是业务 runtime abstraction。
- **Gin 官方文档**（[Gin documentation](https://gin-gonic.com/en/docs/)）说明 Gin 是 HTTP web framework；本计划只把它放在 `internal/api`，出站 provider 调用使用 `net/http.Client`。
- **PostgreSQL 日期时间文档**（[Date/Time Types](https://www.postgresql.org/docs/current/datatype-datetime.html)）说明 `timestamp with time zone` 按瞬时点处理而不保留原始时区；因此写入边界解析 IANA 时区，持久化统一保存瞬时点，展示边界再按请求时区转换。

下列内容是基于 PlayPair 约束的工程推荐，不声称是 Go 官方强制规则：flat feature 包、聚合边界、快照不可变、provider 整批 fallback、OpenAPI-first、金额使用最小货币单位、Alpha 同步执行和 Beta 的回退状态。若 Proposal 或外部服务契约改变，应更新本计划相应的所有权、状态机和验证证据，而不是继续沿用旧设计。
