# CLAUDE.md

本文件定义 Octopus 仓库的 AI 协作规则。Claude Code、Codex 及其他 AI agent 在本仓库工作时均应遵守。

## 1. 项目定位

Octopus 是一个 LLM API 聚合、协议转换与负载均衡服务：

- Go 后端基于 Gin、GORM、Viper，提供管理 API、代理入口、站点同步和后台任务。
- Next.js 前端提供静态导出的管理面板，并通过 `static/` 嵌入 Go 二进制。
- 支持 OpenAI Chat / Responses、Anthropic Messages、Gemini 等协议及流式转换。
- 支持渠道分组、负载均衡、熔断、站点账号同步、渠道投影、统计和 Telegram 管理。

关键业务约束：**分组名称就是下游客户端请求中的外部模型名**。修改分组、模型可见性、渠道投影或协议转换时，必须同时检查调用方和兼容路径。

## 2. 技术栈与目录

### 后端

- Go 版本：`go.mod` 声明的 Go 1.25。
- 启动入口：`main.go` → `cmd/start.go`。
- 启动顺序：配置 → 数据库 → 业务缓存 → 用户初始化 → HTTP 服务 → Relay/后台任务/Telegram Bot。

| 目录 | 职责 |
|---|---|
| `internal/conf/` | Viper 配置、版本和构建信息 |
| `internal/db/` | GORM 初始化、迁移、数据库兼容层 |
| `internal/model/` | 数据模型、请求结构和枚举 |
| `internal/op/` | 业务逻辑、缓存、事务和服务层 |
| `internal/server/` | Gin Server、Handler、中间件；路由统一通过 `NewGroupRouter(path).Use(mw).AddRoute(route)` 链式注册 |
| `internal/relay/` | 请求转发、负载均衡、熔断、SSE/WS、指标记录 |
| `internal/transformer/` | OpenAI、Anthropic、Gemini 协议转换 |
| `internal/sitesync/` | 站点账号、Key、模型同步与渠道投影 |
| `internal/grouphealth/` | 分组健康探测与汇总 |
| `internal/task/` | 定时任务和后台调度 |
| `internal/tgbot/` | Telegram Bot 管理入口 |
| `internal/claudemode/` | Claude Code 客户端指纹常量 |
| `internal/codexmode/` | Codex CLI 客户端指纹常量 |
| `internal/utils/log/` | Zap 日志封装，包名为 `log` |

### 前端

- Next.js 16、React 19、TypeScript、Tailwind CSS 4。
- Zustand 管理客户端状态，TanStack React Query 管理服务端数据。
- `web/src/route/config.tsx` 定义管理面板的自定义 SPA 路由；不要误认为页面完全依赖 Next.js 文件路由。
- API hooks 位于 `web/src/api/endpoints/`。
- 多语言文件位于 `web/public/locale/{en,zh_hans,zh_hant}.json`。
- `web/next.config.ts` 使用静态导出，生产构建输出到 `web/out/`。

## 3. 变更分级与确认边界

本仓库当前没有内置提案目录或自动化提案工具。需要方案审批时，在对话中给出范围、设计、兼容性、风险和验证计划，等待用户明确确认后再实现；如果用户指定提案文件位置，再按指定位置落盘。

### L0：可直接实施

- 恢复既有预期行为的 bug 修复。
- 文案、注释、格式、低风险样式调整。
- 非破坏性配置调整和依赖补丁升级。
- 已有行为的测试补充。

实施前仍需定位根因，完成与风险相称的验证。

### L1：先给简要计划，再实施

- 多文件联动修改。
- 中等规模功能开发。
- 局部重构或局部协议兼容调整。
- 站点同步、渠道投影、Relay 流式行为等核心模块中的非破坏性修改。

### L2：必须先获用户批准

- 新模块、新公共接口或新外部能力。
- 跨模块重构、依赖反转或核心抽象重塑。
- 数据库 Schema、数据迁移或接口契约的破坏性变更。
- 认证、权限、安全、限流、扣费策略调整。
- 可能改变生产行为的大规模性能优化。
- 需要引入新依赖或明显扩大用户已确认范围的改动。

无法确定级别时，按更高一级处理。

## 4. 工作方法

### 基本纪律

- **证据优先**：判断必须来自代码、配置、日志、测试、命令输出或可复现现象。
- **先理解后修改**：非琐碎任务先读取相关实现、调用方、模型和测试。
- **保持范围**：不顺手修复无关问题，不覆盖用户已有改动。
- **不假装验证**：没有运行的测试、构建、页面或接口不能写成已验证。
- **中文沟通**：计划、进度、风险和交付说明默认使用中文。

### 推荐流程

1. 用 `git status --short` 确认工作区状态。
2. 用 `rg` / `rg --files` 定位入口、调用方、模型、设置项和测试。
3. 明确根因、边界条件、副作用和回归点。
4. 对 L1/L2 任务先说明计划；需求存在多种合理解释时先向用户澄清。
5. 按既定方案实施，避免在未重新分析前反复叠补丁。
6. 运行与风险相称的验证并检查 `git diff --check`。
7. 交付时说明改动、验证结果、未验证项和遗留风险。

### 卡住时

- 同类方案连续失败两次后，停止叠补丁，回到调用链和根因分析。
- 运行时问题可临时加日志：后端使用 `internal/utils/log` 的 `log.Infof`、`log.Warnf`、`log.Errorw` 等接口；前端可临时使用 `console.log`，但提交前必须清理调试输出。
- 连续三次仍无法确认方向时，向用户说明现象、已排除项、当前阻塞和可选下一步。

## 5. 必须维护的项目约束

### 服务层边界

- Handler 应调用 `internal/op/`，不要直接在 Handler 中拼装复杂 GORM 查询或复制业务规则。
- 涉及缓存的模型必须通过对应 Op 更新，避免数据库已变更但内存缓存未刷新。
- 部分运行时状态（如 `ChannelKey`）采用内存优先的 write-behind 缓存：`op.ChannelKeyUpdate` 只写内存不落库，统一由 `op.SaveCache`（在 `cmd/start.go` 注册为关机钩子）批量写回数据库。不要假设 Op 更新即时持久化，也不要绕过缓存直接用 GORM 读这类状态；新增退出路径时必须确认 `SaveCache` 会被执行。
- 事务性修改要确认缓存更新发生在事务成功之后。

### 分组与模型可见性

- 下游请求的 `model` 对应分组名称，不直接等同于某个上游模型名。
- 修改分组成员、自动分组或模型过滤时，应检查：模型列表、API Key 可选模型、Relay 候选渠道、前端展示和健康检查。
- 站点投影渠道可能因缺少可用 Key、同步失败或用户配置而隐藏/暂停；不要仅凭数据库中存在 Channel 就判断其可用。

### 站点同步与渠道投影

- `Site`、`SiteAccount`、`SiteToken`、`SiteModel`、`SiteChannelBinding` 与投影出的 `Channel` 存在联动。
- 修改同步结果落库、Key 完成、归档/恢复、模型增删或投影开关时，要检查投影刷新、暂停恢复、分组成员和缓存一致性。
- 用户手工配置与系统投影状态应保持可区分，禁止在同步时无条件覆盖用户设置。

### Relay 与协议转换

- 先确认 inbound 格式、internal request、outbound 格式及是否为同协议直通，再修改转换逻辑。
- SSE、WebSocket、Responses compact、Claude/Codex 模式均有独立兼容路径；修复其中一条路径时必须检查是否影响其他路径。
- 请求头处理需过滤 hop-by-hop 头和敏感认证信息；日志中不得记录原始 API Key、Authorization、Cookie 等秘密。
- 流式修复应覆盖正常完成、上游错误、中途断流、重试及客户端取消等边界。

### 数据库与迁移

- 支持 SQLite、MySQL 和 PostgreSQL；不要只写在单一数据库方言可用的查询。
- Schema 或迁移变更属于 L2，必须先批准，并评估旧数据、重复执行、回滚和多数据库兼容性。
- SQLite 相关代码还需关注单写连接、索引创建和优雅关机行为。

### 前端

- API 类型、React Query hooks 和组件消费方应同步修改。
- 新增或修改用户可见文案时，优先检查三套 locale；当前组件若使用硬编码文案，应保持既有风格或在任务范围内完成国际化。
- 静态导出环境不能依赖仅在 Next.js 服务端运行时可用的能力。
- 复杂图表、虚拟列表、拖拽、响应式布局或交互状态修改，需要真实浏览器验证并检查控制台错误。

## 6. 开发命令

所有命令默认从仓库根目录执行。

### 后端

```bash
go run main.go start
go run main.go start --config path/to/config.json
go test ./...
go build ./...
```

默认监听 `0.0.0.0:8080`。配置默认写入 `data/config.json`，SQLite 默认路径为 `data/data.db`；环境变量使用 `OCTOPUS_` 前缀和下划线层级，例如 `OCTOPUS_SERVER_PORT`。

### 前端

```bash
cd web
pnpm install --frozen-lockfile
pnpm dev
NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" pnpm dev
pnpm lint
pnpm build
```

`pnpm build` 输出到 `web/out/`，不会自动更新 Go 嵌入目录 `static/out/`。

### 发布构建

```bash
bash scripts/build.sh build linux x86_64
bash scripts/build.sh release
```

`scripts/build.sh` 会安装前端依赖、清理前端输出、执行 `go mod tidy`、运行价格更新脚本、替换 `static/out/` 并写入 `build/` 产物。它可能修改工作区，除非任务明确涉及打包或发布，否则不要把它当作普通验证命令；运行后必须检查 `git status` 和 `git diff`。

### Docker

```bash
docker compose up -d
```

## 7. 验证矩阵

| 改动类型 | 最低验证 |
|---|---|
| Go 纯逻辑 | 相关包测试；风险较高时 `go test ./...` |
| Handler / Op / 接口 | 相关单元测试 + 接口或调用路径冒烟 |
| Relay / Transformer / 流式 | 相关包测试，覆盖目标协议和错误路径；必要时真实 SSE/WS 冒烟 |
| 站点同步 / 投影 | `internal/sitesync`、相关 `internal/op` 测试及投影结果检查 |
| 前端类型或组件 | `pnpm lint` + `pnpm build` |
| 前端复杂交互 | 上述检查 + 真实浏览器关键路径验证 |
| 配置 / 构建脚本 | 配置解析或目标构建命令，并检查生成文件和工作区差异 |
| 数据库变更 | 多数据库影响分析、迁移测试、读写验证和回滚评估 |

验证失败时，必须说明失败命令、关键错误、初步原因和下一步，不得只写“未通过”。

## 8. CI、提交与发布

### CI

- 推送到 `main` 或针对 `main` 的 PR 会触发 `.github/workflows/ci.yml`。
- 后端任务运行 `go test ./...`。
- 前端任务运行冻结依赖安装和 `pnpm lint`；本地交付前仍应按改动风险补跑 `pnpm build`。

### Git 操作边界

以下操作必须由用户明确要求：

- `git commit`、`git push`。
- 创建、移动或删除标签。
- `git reset`、`git rebase`、force push。
- 创建 GitHub Release 或触发影响外部系统的发布操作。

可以直接执行只读 Git 命令，以及用户已授权范围内的 `git add`。提交时只暂存当前任务文件，不得夹带工作区中的用户改动。

### 提交信息

公开提交统一使用中文 Conventional Commits，并使用三段式正文说明背景、改动和验证。例如：

```text
fix(relay): 修复 Responses 流中断处理

背景：说明可复现问题和影响范围。

改动：说明核心实现和兼容性处理。

验证：列出实际运行并通过的命令或冒烟路径。
```

### Release

- 版本使用 `v<major>.<minor>.<patch>` 标签。
- 推送 `v*` 标签会触发 `.github/workflows/release.yml`，执行跨平台构建、上传 artifact，并发布 GitHub Release。
- 未经用户明确要求不得自行决定版本号或推送标签。
- 发布后应检查 Actions 结论、Release 是否为非草稿/非预发布，以及压缩包和 `sha256sums.txt` 是否齐全。

## 9. 安全与禁止事项

- 不输出、提交或记录 API Key、JWT、Cookie、数据库密码及其他秘密。
- 不把真实生产数据复制进测试夹具、日志或提交。
- 不删除核心文件、不批量移动目录、不执行破坏性 Git 命令，除非用户明确授权。
- 不引入新依赖、修改认证/权限/限流策略或数据库 Schema，除非已完成 L2 方案确认。
- 不为了让测试通过而削弱断言、静默吞错或绕过既有业务规则。
- 不把构建生成物、临时抓包、调试日志或本地配置误加入提交。
