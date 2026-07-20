# AGENTS.md

本文件是 Codex、Claude Code 及其他 AI agent 在 Octopus 仓库中的统一入口。

## 开始工作前

1. 完整阅读 [CLAUDE.md](./CLAUDE.md)，其中包含项目架构、变更边界、验证策略和发布规则。
2. 确认当前目录是仓库根目录，并先运行 `git status --short`；工作区中的既有改动默认属于用户，不得擅自覆盖、回退或提交。
3. 使用 `rg` / `rg --files` 定位代码，先理解调用链和数据流，再修改实现。
4. 面向用户的计划、进度、风险和交付说明默认使用中文。

## 仓库环境

- 后端：Go 1.25，入口为 `main.go`，主要代码位于 `internal/`。
- 前端：Next.js 16 + React 19，包管理器为 pnpm，代码位于 `web/`。
- CI：推送或向 `main` 提交 PR 时运行 `.github/workflows/ci.yml`。
- Release：推送 `v*` 标签时运行 `.github/workflows/release.yml`，构建并发布多平台压缩包。
- Shell 命令按 POSIX/Linux 环境编写；使用工具前以当前 `PATH` 和仓库声明为准，不要假设存在未配置的命令或任务入口。

## 常用验证

```bash
go test ./...
go build ./...

cd web
pnpm install --frozen-lockfile
pnpm lint
pnpm build
```

具体任务应按 [CLAUDE.md](./CLAUDE.md) 中的验证矩阵选择必要检查，不要把未运行的验证描述为已通过。
