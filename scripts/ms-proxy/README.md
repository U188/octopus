# ModelScope 部署包装（strip-proxy）

## 问题

Octopus 在 `internal/server/middleware/security_headers.go` 里**硬编码**了两个安全响应头：

```text
X-Frame-Options: DENY
Content-Security-Policy: ... frame-ancestors 'none'; ...
```

这是标准的防点击劫持配置，本身没错。但**魔搭创空间（ModelScope Studio）是用 iframe 把应用嵌进 studio 页面显示的**，浏览器看到 `DENY` 就直接拒绝渲染 → studio 页面只剩空壳（浅灰底 + 文档占位图标），看起来像"部署失败"，其实应用跑得好好的。

雪上加霜的是，魔搭网关（阿里 WAF）**禁止顶层导航直连 `*.ms.fun`**：

```text
Sec-Fetch-Dest: document  →  302  https://www.modelscope.ai/studios/<user>/<studio>?mode=full
Sec-Fetch-Dest: iframe    →  200  （正常）
Sec-Fetch-Dest: <其它 11 种取值>  →  200  （正常）
```

也就是说：

| 访问方式 | 结果 |
|---|---|
| 浏览器地址栏直接敲 `xxx.ms.fun` | **302 弹回 studio 页**（网关拦截，改不了） |
| studio 页里的 iframe | 请求拿到 200，但 `X-Frame-Options: DENY` 让浏览器拒绝渲染 |

两头夹死，所以**唯一出路是解决 `X-Frame-Options`**。

## 方案

不改上游应用逻辑，在它前面加一层**只改响应头**的反向代理：

```text
ModelScope :7860
      │
      ▼
  strip-proxy           ← 剥掉 X-Frame-Options，把 CSP 的 frame-ancestors 改成白名单
      │
      ▼
   Octopus :8080        ← 绑定 127.0.0.1，保证所有请求都经过代理
```

**为什么用代理而不是直接改 Go 源码：**

- 上游镜像 `ghcr.io/u188/octopus` 是**预编译的**，改源码必须自己跑完整前端构建（pnpm + Next.js，`static/out` 被 gitignore，构建脚本 22 KB），链路长、易失败。
- 代理是**纯 stdlib、零依赖、~200 行**，`golang:alpine` 里几秒编译完。
- 上游升级只需改 `ARG OCTOPUS_IMAGE`，代理不动。

**顺带**：`security_headers.go` 也加了环境变量开关（`OCTOPUS_ALLOW_EMBED` / `OCTOPUS_FRAME_ANCESTORS`），默认行为**保持 `DENY` 不变**，谁想从源码构建可以直接用。

## 文件

| 文件 | 作用 |
|---|---|
| `main.go` | 反向代理本体。剥 `X-Frame-Options`、重写 CSP `frame-ancestors`、注入 `X-Forwarded-Proto: https` |
| `start-ms.sh` | 容器入口。先起 Octopus（loopback:8080），再起代理（:7860），转发 SIGTERM，打印一次性管理员密码 |
| `go.mod` | 独立 module，保证在最小 `golang:alpine` 里零下载编译 |

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `UPSTREAM` | `http://127.0.0.1:8080` | Octopus 内部地址 |
| `LISTEN` | `:7860` | 对外监听端口（必须是魔搭要求的 7860） |
| `FRAME_ANCESTORS` | `https://www.modelscope.ai https://*.modelscope.ai https://*.modelscope.cn` | CSP 白名单，空格分隔 |

`X-Frame-Options` **一律剥除**，不设替代值 —— 它不支持通配符语法，留着就无法放行任意子域；实际访问控制交给支持通配符的 CSP `frame-ancestors`。

## 验证

真实二进制端到端测试（真跑 Octopus + 真跑代理）：

```text
PASS  直连仍有 X-Frame-Options: DENY
PASS  直连 CSP 仍是 frame-ancestors 'none'
PASS  代理已剥除 X-Frame-Options
PASS  代理 CSP 放行 modelscope.ai
PASS  代理 CSP 已去掉 'none'
PASS  代理返回真实 Octopus 页面
PASS  代理与直连页面字节数一致
PASS  后端 API 经代理正常应答 401
```

关键点：**直连内部端口仍然锁死**（安全性没被削弱），只有经代理的对外流量被放行。

本地复现：

```sh
go build -o strip-proxy ./scripts/ms-proxy
UPSTREAM=http://127.0.0.1:8080 LISTEN=:7860 ./strip-proxy
```

## 部署

1. 推送本仓库（`Dockerfile` 已改为多阶段构建，会自动编译并安装代理）。
2. 魔搭创空间重新构建/部署。
3. studio 页面里应该就能看到 Octopus 登录页了。

**注意**：`ms_deploy.json` 里的 `OCTOPUS_SERVER_PORT` 已从 `7860` 改为 `8080` —— 否则 Octopus 会和代理抢同一个端口，容器起不来。
