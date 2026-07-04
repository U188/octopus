<div align="center">

<img src="web/public/logo.svg" alt="Octopus Logo" width="120" height="120">

### Octopus

**U188 魔改版 LLM API 聚合、站点同步与负载均衡服务**

简体中文 | [English](README.md) | [使用指南](USAGE_zh.md)

</div>

> 当前仓库为 `U188/octopus` 自用魔改版，重点维护站点同步、Codex/Claude/Responses 流式兼容、站点 Key 投影与生产部署稳定性。


## ✨ 特性

- 🔀 **多渠道聚合** - 支持接入多个 LLM 供应商渠道，统一管理
- 🔑 **多Key支持** - 单渠道支持配置多 Key
- ⚡ **智能优选** - 单渠道多端点，智能选择延迟最小的端点请求
- ⚖️ **负载均衡** - 自动分配请求，确保服务稳定高效
- 🔄 **协议互转** - 支持 OpenAI Chat / OpenAI Responses / Anthropic 三种 API 格式互相转换
- 💰 **价格同步** - 自动更新模型价格
- 🔃 **模型同步** - 自动与渠道同步可用模型列表，省心省力
- 📊 **数据统计** - 全面的请求统计、Token 消耗、费用追踪
- 🎨 **优雅界面** - 简洁美观的 Web 管理面板
- 🗄️ **多数据库支持** - 支持 SQLite、MySQL、PostgreSQL

> 📖 **第一次使用？** 请先阅读 **[新手使用指南](USAGE_zh.md)**，覆盖从部署到接入客户端的完整流程，5 分钟快速上手。


## 🚀 安装与启动

Octopus 支持 Docker、Release 二进制文件和源码构建三种安装方式。生产部署优先推荐 Docker；需要修改代码或本地开发时使用源码方式。

### 🐳 方式一：Docker 安装（推荐）

**环境要求：**

- 已安装 Docker 20+
- 已安装 Docker Compose v2（使用 `docker compose` 命令）
- 服务器或本机 8080 端口未被占用

**1. 创建数据目录**

Linux / macOS：

```bash
mkdir -p ./octopus-data
```

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force .\octopus-data
```

**2. 使用 Docker 直接启动**

Linux / macOS：

```bash
docker run -d \
  --name octopus \
  --restart unless-stopped \
  -p 8080:8080 \
  -v "$(pwd)/octopus-data:/app/data" \
  ghcr.io/u188/octopus
```

Windows PowerShell：

```powershell
docker run -d `
  --name octopus `
  --restart unless-stopped `
  -p 8080:8080 `
  -v "${PWD}\octopus-data:/app/data" `
  ghcr.io/u188/octopus
```

**3. 或使用 Docker Compose 启动**

创建 `docker-compose.yml`：

```yaml
services:
  octopus:
    image: ghcr.io/u188/octopus
    container_name: octopus
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./octopus-data:/app/data
```

启动：

```bash
docker compose up -d
```

查看日志：

```bash
docker logs -f octopus
```

停止服务：

```bash
docker compose down
```

升级镜像：

```bash
docker compose pull
docker compose up -d
```

启动完成后访问 `http://localhost:8080`。

### 📦 方式二：Release 二进制安装

**环境要求：**

- 下载与系统匹配的 Release 包
- 8080 端口未被占用

**1. 下载并解压**

从 [Releases](https://github.com/U188/octopus/releases) 下载对应平台的压缩包，例如 Linux AMD64、Windows AMD64 或 macOS ARM64。

**2. 创建数据目录**

```bash
mkdir -p data
```

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force .\data
```

**3. 启动服务**

Linux / macOS：

```bash
chmod +x ./octopus
./octopus start
```

Windows PowerShell：

```powershell
.\octopus.exe start
```

启动后访问 `http://localhost:8080`。

### 🛠️ 方式三：源码构建安装

**环境要求：**

- Go 1.25.0 或更高版本
- Node.js 20 或更高版本
- Corepack / pnpm
- Git

**1. 克隆项目**

```bash
git clone https://github.com/U188/octopus.git
cd octopus
```

**2. 安装并构建前端**

Linux / macOS：

```bash
cd web
corepack enable
corepack pnpm install
corepack pnpm build
cd ..
rm -rf static/out
mv web/out static/out
```

Windows PowerShell：

```powershell
cd web
corepack enable
corepack pnpm install
corepack pnpm build
cd ..
Remove-Item -Recurse -Force .\static\out -ErrorAction SilentlyContinue
Move-Item .\web\out .\static\out
```

> 前端使用 Next.js 静态导出，构建产物必须放到 `static/out`，随后会被 Go 通过 `embed` 打进后端二进制。

**3. 构建后端**

Linux / macOS：

```bash
go mod download
go build -o octopus .
./octopus start
```

Windows PowerShell：

```powershell
go mod download
go build -o octopus.exe .
.\octopus.exe start
```

启动后访问 `http://localhost:8080`。

### 💻 本地开发模式

开发时建议前端和后端分开启动，前端访问 3000 端口，后端访问 8080 端口。

**终端 1：启动后端**

```bash
go run main.go start
```

**终端 2：启动前端**

Linux / macOS：

```bash
cd web
corepack enable
corepack pnpm install
NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" corepack pnpm dev
```

Windows PowerShell：

```powershell
cd web
corepack enable
corepack pnpm install
$env:NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080"
corepack pnpm dev
```

访问 `http://localhost:3000`。

### 🔐 默认账户

首次启动后，访问 http://localhost:8080 使用以下默认账户登录管理面板：

- **用户名**：`admin`
- **密码**：`admin`

> ⚠️ **安全提示**：请在首次登录后立即修改默认密码。

### 📝 配置文件

配置文件默认位于 `data/config.json`，首次启动时自动生成。

**完整配置示例：**

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  },
  "database": {
    "type": "sqlite",
    "path": "data/data.db"
  },
  "log": {
    "level": "info",
    "request_debug": {
      "enabled": false,
      "include_headers": true,
      "include_body": true,
      "max_body_bytes": 8192
    }
  }
}
```

**配置项说明：**

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `server.host` | 监听地址 | `0.0.0.0` |
| `server.port` | 服务端口 | `8080` |
| `database.type` | 数据库类型 | `sqlite` |
| `database.path` | 数据库连接地址 | `data/data.db` |
| `log.level` | 日志级别 | `info` |
| `log.request_debug.enabled` | 记录每个 HTTP 请求的调试日志（含脱敏 headers / 小请求体） | `false` |
| `log.request_debug.include_headers` | 请求调试日志是否包含 headers（敏感字段会脱敏） | `true` |
| `log.request_debug.include_body` | 请求调试日志是否包含小体积文本/JSON 请求体 | `true` |
| `log.request_debug.max_body_bytes` | 请求体调试记录上限，超出只记录省略原因 | `8192` |

**数据库配置：**

支持三种数据库：

| 类型 | `database.type` | `database.path` 格式 |
|------|-----------------|---------------------|
| SQLite | `sqlite` | `data/data.db` |
| MySQL | `mysql` | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `postgres` | `postgresql://user:password@host:port/dbname?sslmode=disable` |

**MySQL 配置示例：**

```json
{
  "database": {
    "type": "mysql",
    "path": "root:password@tcp(127.0.0.1:3306)/octopus"
  }
}
```

**PostgreSQL 配置示例：**

```json
{
  "database": {
    "type": "postgres",
    "path": "postgresql://user:password@localhost:5432/octopus?sslmode=disable"
  }
}
```

> 💡 **提示**：MySQL 和 PostgreSQL 需要先手动创建数据库，程序会自动创建表结构。

**环境变量：**

所有配置项均可通过环境变量覆盖，格式为 `OCTOPUS_` + 配置路径（用 `_` 连接）：

| 环境变量 | 对应配置项 |
|----------|-----------|
| `OCTOPUS_SERVER_PORT` | `server.port` |
| `OCTOPUS_SERVER_HOST` | `server.host` |
| `OCTOPUS_DATABASE_TYPE` | `database.type` |
| `OCTOPUS_DATABASE_PATH` | `database.path` |
| `OCTOPUS_LOG_LEVEL` | `log.level` |
| `OCTOPUS_LOG_REQUEST_DEBUG_ENABLED` | `log.request_debug.enabled` |
| `OCTOPUS_LOG_REQUEST_DEBUG_INCLUDE_HEADERS` | `log.request_debug.include_headers` |
| `OCTOPUS_LOG_REQUEST_DEBUG_INCLUDE_BODY` | `log.request_debug.include_body` |
| `OCTOPUS_LOG_REQUEST_DEBUG_MAX_BODY_BYTES` | `log.request_debug.max_body_bytes` |
| `OCTOPUS_GITHUB_PAT` | 用于获取最新版本时的速率限制(可选) |
| `OCTOPUS_RELAY_MAX_SSE_EVENT_SIZE` | 最大 SSE 事件大小(可选) |
| `OCTOPUS_IMAGES_BODY_MEMORY_THRESHOLD_MB` | Images 请求体内存缓存阈值，超过阈值会落盘临时文件(可选，默认 16) |
| `OCTOPUS_IMAGES_BODY_MAX_MB` | Images 请求体最大大小限制，超过限制将拒绝请求(可选，默认 256) |
| `OCTOPUS_IMAGES_BODY_TMP_DIR` | Images 请求体临时文件目录(可选，默认 `./cache`) |
| `OCTOPUS_IMAGES_BODY_TMP_CLEANUP_HOURS` | 启动时清理临时文件的时间阈值(可选，默认 24) |


## 📸 界面预览

### 🖥️ 桌面端

<div align="center">
<table>
<tr>
<td align="center"><b>首页</b></td>
<td align="center"><b>渠道</b></td>
<td align="center"><b>分组</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="首页" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="渠道" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="分组" width="400"></td>
</tr>
<tr>
<td align="center"><b>价格</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="价格" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="日志" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="设置" width="400"></td>
</tr>
</table>
</div>

### 📱 移动端

<div align="center">
<table>
<tr>
<td align="center"><b>首页</b></td>
<td align="center"><b>渠道</b></td>
<td align="center"><b>分组</b></td>
<td align="center"><b>价格</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="移动端首页" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="移动端渠道" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="移动端分组" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="移动端价格" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="移动端日志" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="移动端设置" width="140"></td>
</tr>
</table>
</div>


## 📖 功能说明

### 📡 渠道管理

渠道是连接 LLM 供应商的基础配置单元。

**Base URL 说明：**

程序会根据渠道类型自动补全 API 路径，您只需填写基础 URL 即可：

| 渠道类型 | 自动补全路径 | 填写 URL | 完整请求地址示例 |
|----------|-------------|----------|-----------------|
| OpenAI Chat | `/chat/completions` | `https://api.openai.com/v1` | `https://api.openai.com/v1/chat/completions` |
| OpenAI Responses | `/responses` | `https://api.openai.com/v1` | `https://api.openai.com/v1/responses` |
| OpenAI Images | `/images/generations`、`/images/edits`、`/images/variations` | `https://api.openai.com/v1` | `https://api.openai.com/v1/images/generations` |
| Anthropic | `/messages` | `https://api.anthropic.com/v1` | `https://api.anthropic.com/v1/messages` |
| Gemini | `/models/:model:generateContent` | `https://generativelanguage.googleapis.com/v1beta` | `https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent` |

> 💡 **提示**：填写 Base URL 时无需包含具体的 API 端点路径，程序会自动处理。

---

### 📁 分组管理

分组用于将多个渠道聚合为一个统一的对外模型名称。

**核心概念：**

- **分组名称** 即程序对外暴露的模型名称
- 调用 API 时，将请求中的 `model` 参数设置为分组名称即可

**负载均衡模式：**

| 模式 | 说明 |
|------|------|
| 🔄 **轮询** | 每次请求依次切换到下一个渠道 |
| 🎲 **随机** | 每次请求随机选择一个可用渠道 |
| 🛡️ **故障转移** | 优先使用高优先级渠道，仅当其故障时才切换到低优先级渠道 |
| ⚖️ **加权分配** | 根据渠道设置的权重比例分配请求 |

> 💡 **示例**：创建分组名称为 `gpt-4o`，将多个供应商的 GPT-4o 渠道加入该分组，即可通过统一的 `model: gpt-4o` 访问所有渠道。

---

### 💰 价格管理

管理系统中的模型价格信息。

**数据来源：**

- 系统会定期从 [models.dev](https://github.com/sst/models.dev) 同步更新模型价格数据
- 当创建渠道时，若渠道包含的模型不在 models.dev 中，系统会自动在此页面创建该模型的价格信息,所以此页面显示的是没有从上游获取到价格的模型，用户可以手动设置价格
- 也支持手动创建 models.dev 中已存在的模型，用于自定义价格

**价格优先级：**

| 优先级 | 来源 | 说明 |
|:------:|------|------|
| 🥇 高 | 本页面 | 用户在价格管理页面设置的价格 |
| 🥈 低 | models.dev | 自动同步的默认价格 |

> 💡 **提示**：如需覆盖某个模型的默认价格，只需在价格管理页面为其设置自定义价格即可。

---

### ⚙️ 设置

系统全局配置项。

**统计保存周期（分钟）：**

由于程序涉及大量统计项目，若每次请求都直接写入数据库会影响读写性能。因此程序采用以下策略：

- 统计数据先保存在 **内存** 中
- 按设定的周期 **定期批量写入** 数据库

> ⚠️ **重要提示**：退出程序时，请使用正常的关闭方式（如 `Ctrl+C` 或发送 `SIGTERM` 信号），以确保内存中的统计数据能正确写入数据库。**请勿使用 `kill -9` 等强制终止方式**，否则可能导致统计数据丢失。




## 🔌 客户端接入

### OpenAI SDK

```python
from openai import OpenAI
import os

client = OpenAI(   
    base_url="http://127.0.0.1:8080/v1",   
    api_key="sk-octopus-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg", 
)
completion = client.chat.completions.create(
    model="octopus-openai",  // 填写正确的分组名称
    messages = [
        {"role": "user", "content": "Hello"},
    ],
)
print(completion.choices[0].message.content)
```

### Claude Code

编辑 `~/.claude/settings.json`

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_AUTH_TOKEN": "sk-octopus-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "ANTHROPIC_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_SMALL_FAST_MODEL": "octopus-haiku-4-5",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "octopus-haiku-4-5"
  }
}
```

### Codex

编辑 `~/.codex/config.toml`

```toml
model = "octopus-codex" # 填写正确的分组名称

model_provider = "octopus"

[model_providers.octopus]
name = "octopus"
base_url = "http://127.0.0.1:8080/v1"
```
编辑 `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": "sk-octopus-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg"
}
```


---

## 🔀 魔改版维护说明

该版本以个人生产使用为目标，保留 Octopus 的多渠道聚合基础能力，同时强化站点账号同步、测试对话、流式协议和投影渠道管理。仓库、Release、Docker 镜像和在线更新均指向 `U188/octopus`。

### 🏗️ 新增子系统

- **🌐 站点管理 & 站点同步** —— 全新资源层（后端 `sitesync/` + 独立前端模块）。管理聚合站点账号：定时同步、自动签到、余额与今日收益、按站点价格、归档/恢复、AnyRouter、路由探测、`sub2api`，以及把账号物化为渠道的 projected site channel。
- **🔌 WebSocket relay** —— 上游 WS 连接池（带健康退避）、面向客户端的 WS、DB-backed response affinity，以及面向 Codex 工具的可选 OpenAI Responses 直通。
- **🖼️ OpenAI Images API 转发**（带 body 缓存）。
- **🩹 Transformer 大重构** —— 三大适配器统一原生 StreamEvent 流水线、Anthropic patching 层、role 交替规范化，及大量跨格式保真修复。

### 🛠️ 重做

- **渠道模块** —— Site / Manual Tab 切换；分组编辑器保留渠道元数据。
- **Relay 内核** —— 路由学习、重试、取消传播、Responses compact proxy、日志按 channel ID 过滤。
- **认证** —— JWT 密钥持久化到数据库（密码轮换更安全），不再从凭证派生。
- **备份**、**日志**（`Item.tsx` 重写）、**首页图表** 全部重做。

### 🧬 近期重点

- Codex 测试对话禁用工具注入，支持 Responses SSE 流式解析。
- Cline/OpenAI Chat 测试对话支持流式 SSE 解析，包括非标准嵌套 `data:` 场景。
- 站点账号保存区分 `API Key` 与 `Access Token`：API Key 用于对话，Access Token 用于签到/管理端同步。
- 站点 token 投影会清理同组同名的旧 Key，避免无效 token 反复复活。
- 服务部署时确保 systemd 进程独占 8080，避免旧 `go run` 进程抢占端口。

> 如果需要对比历史来源，可在本地自行添加上游 remote；本仓库默认不再依赖上游 Git 地址或发布地址。

---

## 🤝 致谢

- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) - 本项目的 LLM API 适配模块直接源自该仓库的实现
- 📊 [sst/models.dev](https://github.com/sst/models.dev) - AI 模型数据库，提供模型价格数据

## 🔗 友链

- 🐧 [LinuxDO](https://linux.do) - 真正的技术社区
