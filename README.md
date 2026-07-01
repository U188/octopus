<div align="center">

<img src="web/public/logo.svg" alt="Octopus Logo" width="120" height="120">

### Octopus

**U188 custom edition for LLM API aggregation, site sync, and load balancing**

 English | [简体中文](README_zh.md) | [Getting Started](USAGE.md)

</div>

> This repository is the `U188/octopus` custom edition. It focuses on site synchronization, Codex/Claude/Responses streaming compatibility, projected site keys, and production deployment stability.


## ✨ Features

- 🔀 **Multi-Channel Aggregation** - Connect multiple LLM provider channels with unified management
- 🔑 **Multi-Key Support** - Support multiple API keys for a single channel
- ⚡ **Smart Selection** - Multiple endpoints per channel, smart selection of the endpoint with the shortest delay
- ⚖️ **Load Balancing** - Automatic request distribution for stable and efficient service
- 🔄 **Protocol Conversion** - Seamless conversion between OpenAI Chat / OpenAI Responses / Anthropic API formats
- 💰 **Price Sync** - Automatic model pricing updates
- 🔃 **Model Sync** - Automatic synchronization of available model lists with channels
- 📊 **Analytics** - Comprehensive request statistics, token consumption, and cost tracking
- 🎨 **Elegant UI** - Clean and beautiful web management panel
- 🗄️ **Multi-Database Support** - Support for SQLite, MySQL, PostgreSQL

> 📖 **First time using Octopus?** Check out the **[Getting Started Guide](USAGE.md)** for a complete walkthrough from deployment to client integration — get up and running in 5 minutes.


## 🚀 Installation

Octopus can be installed with Docker, a prebuilt release binary, or a source build. Docker is recommended for production deployments. Use the source workflow when developing or modifying the project.

### 🐳 Option 1: Docker Installation (Recommended)

**Requirements:**

- Docker 20+
- Docker Compose v2 (`docker compose`)
- Port `8080` available

**1. Create a data directory**

Linux / macOS:

```bash
mkdir -p ./octopus-data
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force .\octopus-data
```

**2. Run with Docker**

Linux / macOS:

```bash
docker run -d \
  --name octopus \
  --restart unless-stopped \
  -p 8080:8080 \
  -v "$(pwd)/octopus-data:/app/data" \
  ghcr.io/u188/octopus
```

Windows PowerShell:

```powershell
docker run -d `
  --name octopus `
  --restart unless-stopped `
  -p 8080:8080 `
  -v "${PWD}\octopus-data:/app/data" `
  ghcr.io/u188/octopus
```

**3. Or run with Docker Compose**

Create `docker-compose.yml`:

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

Start:

```bash
docker compose up -d
```

View logs:

```bash
docker logs -f octopus
```

Stop:

```bash
docker compose down
```

Upgrade:

```bash
docker compose pull
docker compose up -d
```

Open `http://localhost:8080` after the service starts.

### 📦 Option 2: Release Binary Installation

**Requirements:**

- A release package matching your operating system and CPU architecture
- Port `8080` available

**1. Download and extract**

Download the matching archive from [Releases](https://github.com/U188/octopus/releases), for example Linux AMD64, Windows AMD64, or macOS ARM64.

**2. Create a data directory**

```bash
mkdir -p data
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force .\data
```

**3. Start the service**

Linux / macOS:

```bash
chmod +x ./octopus
./octopus start
```

Windows PowerShell:

```powershell
.\octopus.exe start
```

Open `http://localhost:8080`.

### 🛠️ Option 3: Build from Source

**Requirements:**

- Go 1.25.0 or newer
- Node.js 20 or newer
- Corepack / pnpm
- Git

**1. Clone the repository**

```bash
git clone https://github.com/U188/octopus.git
cd octopus
```

**2. Install and build the frontend**

Linux / macOS:

```bash
cd web
corepack enable
corepack pnpm install
corepack pnpm build
cd ..
rm -rf static/out
mv web/out static/out
```

Windows PowerShell:

```powershell
cd web
corepack enable
corepack pnpm install
corepack pnpm build
cd ..
Remove-Item -Recurse -Force .\static\out -ErrorAction SilentlyContinue
Move-Item .\web\out .\static\out
```

> The frontend uses Next.js static export. The generated files must be placed in `static/out` so Go can embed them into the backend binary.

**3. Build and run the backend**

Linux / macOS:

```bash
go mod download
go build -o octopus .
./octopus start
```

Windows PowerShell:

```powershell
go mod download
go build -o octopus.exe .
.\octopus.exe start
```

Open `http://localhost:8080`.

### 💻 Development Mode

During development, run the backend and frontend separately. The backend listens on `8080`; the Next.js dev server listens on `3000`.

**Terminal 1: backend**

```bash
go run main.go start
```

**Terminal 2: frontend**

Linux / macOS:

```bash
cd web
corepack enable
corepack pnpm install
NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" corepack pnpm dev
```

Windows PowerShell:

```powershell
cd web
corepack enable
corepack pnpm install
$env:NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080"
corepack pnpm dev
```

Open `http://localhost:3000`.

### 🔐 Default Credentials

After first launch, visit http://localhost:8080 and log in to the management panel with:

- **Username**: `admin`
- **Password**: `admin`

> ⚠️ **Security Notice**: Please change the default password immediately after first login.

### 📝 Configuration File

The configuration file is located at `data/config.json` by default and is automatically generated on first startup.

**Complete Configuration Example:**

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
    "level": "info"
  }
}
```

**Configuration Options:**

| Option | Description | Default |
|--------|-------------|---------|
| `server.host` | Listen address | `0.0.0.0` |
| `server.port` | Server port | `8080` |
| `database.type` | Database type | `sqlite` |
| `database.path` | Database connection string | `data/data.db` |
| `log.level` | Log level | `info` |

**Database Configuration:**

Three database types are supported:

| Type | `database.type` | `database.path` Format |
|------|-----------------|-----------------------|
| SQLite | `sqlite` | `data/data.db` |
| MySQL | `mysql` | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `postgres` | `postgresql://user:password@host:port/dbname?sslmode=disable` |

**MySQL Configuration Example:**

```json
{
  "database": {
    "type": "mysql",
    "path": "root:password@tcp(127.0.0.1:3306)/octopus"
  }
}
```

**PostgreSQL Configuration Example:**

```json
{
  "database": {
    "type": "postgres",
    "path": "postgresql://user:password@localhost:5432/octopus?sslmode=disable"
  }
}
```

> 💡 **Tip**: MySQL and PostgreSQL require manual database creation. The application will automatically create the table structure.

### 🌐 Environment Variables

All configuration options can be overridden via environment variables using the format `OCTOPUS_` + configuration path (joined with `_`):

| Environment Variable | Configuration Option |
|---------------------|---------------------|
| `OCTOPUS_SERVER_PORT` | `server.port` |
| `OCTOPUS_SERVER_HOST` | `server.host` |
| `OCTOPUS_DATABASE_TYPE` | `database.type` |
| `OCTOPUS_DATABASE_PATH` | `database.path` |
| `OCTOPUS_LOG_LEVEL` | `log.level` |
| `OCTOPUS_GITHUB_PAT` | For rate limiting when getting the latest version (optional) |
| `OCTOPUS_RELAY_MAX_SSE_EVENT_SIZE` | Maximum SSE event size (optional) |
| `OCTOPUS_IMAGES_BODY_MEMORY_THRESHOLD_MB` | Images request body in-memory threshold. If exceeded, it will be spooled to a temporary file (optional, default 16) |
| `OCTOPUS_IMAGES_BODY_MAX_MB` | Images request body maximum size. Requests above this limit are rejected (optional, default 256) |
| `OCTOPUS_IMAGES_BODY_TMP_DIR` | Images request body temporary directory (optional, default `./cache`) |
| `OCTOPUS_IMAGES_BODY_TMP_CLEANUP_HOURS` | Startup cleanup threshold for temporary files (optional, default 24) |

## 📸 Screenshots

### 🖥️ Desktop

<div align="center">
<table>
<tr>
<td align="center"><b>Dashboard</b></td>
<td align="center"><b>Channel Management</b></td>
<td align="center"><b>Group Management</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="Dashboard" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="Channel" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="Group" width="400"></td>
</tr>
<tr>
<td align="center"><b>Price Management</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="Price Management" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="Logs" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="Settings" width="400"></td>
</tr>
</table>
</div>

### 📱 Mobile

<div align="center">
<table>
<tr>
<td align="center"><b>Home</b></td>
<td align="center"><b>Channel</b></td>
<td align="center"><b>Group</b></td>
<td align="center"><b>Price</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="Mobile Home" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="Mobile Channel" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="Mobile Group" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="Mobile Price" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="Mobile Logs" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="Mobile Settings" width="140"></td>
</tr>
</table>
</div>


## 📖 Documentation

### 📡 Channel Management

Channels are the basic configuration units for connecting to LLM providers.

**Base URL Guide:**

The program automatically appends API paths based on channel type. You only need to provide the base URL:

| Channel Type | Auto-appended Path | Base URL | Full Request URL Example |
|--------------|-------------------|----------|--------------------------|
| OpenAI Chat | `/chat/completions` | `https://api.openai.com/v1` | `https://api.openai.com/v1/chat/completions` |
| OpenAI Responses | `/responses` | `https://api.openai.com/v1` | `https://api.openai.com/v1/responses` |
| OpenAI Images | `/images/generations`, `/images/edits`, `/images/variations` | `https://api.openai.com/v1` | `https://api.openai.com/v1/images/generations` |
| Anthropic | `/messages` | `https://api.anthropic.com/v1` | `https://api.anthropic.com/v1/messages` |
| Gemini | `/models/:model:generateContent` | `https://generativelanguage.googleapis.com/v1beta` | `https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent` |

> 💡 **Tip**: No need to include specific API endpoint paths in the Base URL - the program handles this automatically.

---

### 📁 Group Management

Groups aggregate multiple channels into a unified external model name.

**Core Concepts:**

- **Group name** is the model name exposed by the program
- When calling the API, set the `model` parameter to the group name

**Load Balancing Modes:**

| Mode | Description |
|------|-------------|
| 🔄 **Round Robin** | Cycles through channels sequentially for each request |
| 🎲 **Random** | Randomly selects an available channel for each request |
| 🛡️ **Failover** | Prioritizes high-priority channels, switches to lower priority only on failure |
| ⚖️ **Weighted** | Distributes requests based on configured channel weights |

> 💡 **Example**: Create a group named `gpt-4o`, add multiple providers' GPT-4o channels to it, then access all channels via a unified `model: gpt-4o`.

---

### 💰 Price Management

Manage model pricing information in the system.

**Data Sources:**

- The system periodically syncs model pricing data from [models.dev](https://github.com/sst/models.dev)
- When creating a channel, if the channel contains models not in models.dev, the system automatically creates pricing information for those models on this page, so this page displays models that haven't had their prices fetched from upstream, allowing users to set prices manually
- Manual creation of models that exist in models.dev is also supported for custom pricing

**Price Priority:**

| Priority | Source | Description |
|:--------:|--------|-------------|
| 🥇 High | This Page | Prices set by user in price management page |
| 🥈 Low | models.dev | Auto-synced default prices |

> 💡 **Tip**: To override a model's default price, simply set a custom price for it in the price management page.

---

### ⚙️ Settings

Global system configuration.

**Statistics Save Interval (minutes):**

Since the program handles numerous statistics, writing to the database on every request would impact read/write performance. The program uses this strategy:

- Statistics are first stored in **memory**
- Periodically **batch-written** to the database at the configured interval

> ⚠️ **Important**: When exiting the program, use proper shutdown methods (like `Ctrl+C` or sending `SIGTERM` signal) to ensure in-memory statistics are correctly written to the database. **Do NOT use `kill -9` or other forced termination methods**, as this may result in statistics data loss.

---

## 🔌 Client Integration

### OpenAI SDK

```python
from openai import OpenAI
import os

client = OpenAI(   
    base_url="http://127.0.0.1:8080/v1",   
    api_key="sk-octopus-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg", 
)
completion = client.chat.completions.create(
    model="octopus-openai",  # Use the correct group name
    messages = [
        {"role": "user", "content": "Hello"},
    ],
)
print(completion.choices[0].message.content)
```

### Claude Code

Edit `~/.claude/settings.json`

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

Edit `~/.codex/config.toml`

```toml
model = "octopus-codex" # Use the correct group name

model_provider = "octopus"

[model_providers.octopus]
name = "octopus"
base_url = "http://127.0.0.1:8080/v1"
```

Edit `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": "sk-octopus-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg"
}
```

---

## 🔀 Custom Edition Notes

This edition keeps Octopus' multi-channel aggregation foundation while maintaining custom site-account sync, test-conversation streaming, protocol compatibility, and projected-channel management. Repository links, releases, Docker images, and online updates point to `U188/octopus`.

### 🏗️ New subsystems

- **🌐 Site Management & Site Sync** — full new resource layer (backend `sitesync/` + dedicated frontend modules). Manages aggregator-site accounts: scheduled sync, check-in, balance / today's income, per-site pricing, archive/restore, AnyRouter, route probing, `sub2api`, and projected site channels.
- **🔌 WebSocket relay** — upstream WS connection pool with health backoff, client-facing WS, DB-backed response affinity, and opt-in OpenAI Responses passthrough for Codex tools.
- **🖼️ OpenAI Images API forwarding** with body cache.
- **🩹 Transformer overhaul** — native StreamEvent pipeline across all adapters, Anthropic patching layer, role-alternation normalization, plus a long tail of cross-format fidelity fixes.

### 🛠️ Reworked

- **Channel module** — tabbed Site/Manual layout; group editor preserves channel metadata.
- **Relay core** — route learning, retry, cancel propagation, Responses compact proxy, log filtering by channel ID.
- **Auth** — JWT secret persisted in DB (rotation-safe), no longer derived from credentials.
- **Backup**, **logs** (`Item.tsx` rewrite), and **home charts** redesigned.

### 🧬 Recent focus

- Codex test conversations disable tool injection and support Responses SSE streaming.
- Cline/OpenAI Chat test conversations support streaming SSE parsing, including nested `data:` payloads.
- Site account saving separates `API Key` and `Access Token`: API keys are used for chat, access tokens are used for check-in / management sync.
- Site token projection prunes stale same-group same-name keys so invalid tokens do not revive after sync.
- Deployment checks that the systemd `octopus` process owns port 8080, avoiding stale `go run` processes.

> Add an upstream remote locally if historical comparison is needed. This repository no longer depends on upstream Git or release URLs by default.

---

## 🤝 Acknowledgments

- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) - The LLM API adaptation module in this project is directly derived from this repository
- 📊 [sst/models.dev](https://github.com/sst/models.dev) - AI model database providing model pricing data

## 🔗 Friend Links

- 🐧 [LinuxDO](https://linux.do) - A community for tech enthusiasts
