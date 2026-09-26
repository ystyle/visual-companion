# visual-companion

[English](README.md) | **简体中文**

一个 MCP server，给编码 agent 一个浏览器标签页，用来在头脑风暴过程中展示
mockup、架构图和视觉选项。

**agent 看不见这个页面。** 它推送一段 HTML 片段，你在浏览器里看、点选，
agent 在下一轮读取你的点击。

## 为什么要做这个

这是 [superpowers](https://github.com/obra/superpowers) 里 brainstorming
视觉伴侣的 Go 重写版，动机只有一个：**安装端零依赖**。它以一个静态二进制
分发，浏览器资产全部内嵌，所以目标机器不需要 Node、不需要任何运行时、也不
需要构建链——复制一个文件就能跑。

原版用 `nohup`、pid 文件、owner-PID 看门狗和一堆平台相关的后台化技巧来解决
"让浏览器活过 agent 的回合"这件事。改用 MCP 之后，进程生命周期由宿主负责，
上面这一整套都不需要了。

## 安装

**把下面这段直接粘给你的 agent。** 这才是这个工具本来该有的安装方式——MCP
server 的安装者通常就是 agent 自己。

> 帮我安装 visual-companion 这个 MCP server。
>
> 它是一个静态单文件二进制，通过 stdio 讲 MCP——不需要 Node、不需要任何运行时、
> 不需要编译。执行这条命令安装：
>
> ```
> curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
> ```
>
> 它会识别平台、校验 SHA256、装到 `~/.local/bin`，并清掉 macOS 的隔离标记。
> 如果 `~/.local/bin` 不在我的 `PATH` 里，请加到我的 shell 配置里。如果下载失败，
> 脚本会打印手动安装的备用地址——用它。
>
> 然后把它注册成你自己的 MCP server，名字叫 `visual-companion`，命令就是
> `visual-companion`、不带任何参数（它会自动发现我的工作区）。用你添加 MCP
> server 的常规方式即可——Claude Code 是
> `claude mcp add visual-companion -- visual-companion`，其他宿主就改我的 MCP
> 配置 JSON。注册完确认一下，并验证 `visual-companion --version` 能跑。
>
> 先别开始头脑风暴。只要告诉我装好了，并提醒我把 `.visual-companion/` 加进
> 项目的 `.gitignore`。

同一段话的更完整版本（含各种失败情况的处置）在
[docs/agent-install.md](docs/agent-install.md)——如果上面这段让 agent 卡住了，
把那个文件指给它。

### 自己装

```bash
curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
```

脚本会识别平台、**默认校验 SHA256**、装到 `~/.local/bin`，并清掉 macOS 的
隔离标记。想先读一遍也可以（很短），或者覆盖默认行为：

```bash
# 换安装位置，或指定版本
curl -fsSL .../install.sh | INSTALL_DIR=~/.local/share/bin VERSION=1.2.3 bash
```

`VERIFY=0` 会跳过校验。只在你用的镜像站不带 `SHA256SUMS` 时才这么做。

### 手动安装

从 releases 下载对应平台的二进制，丢进 `PATH`。安装到此结束。

| 平台 | 产物 |
|---|---|
| macOS（Apple Silicon） | `visual-companion-darwin-arm64` |
| macOS（Intel） | `visual-companion-darwin-amd64` |
| Linux（x86-64） | `visual-companion-linux-amd64` |
| Linux（arm64） | `visual-companion-linux-arm64` |
| Windows（x86-64） | `visual-companion-windows-amd64.exe` |

```bash
chmod +x visual-companion-darwin-arm64
mv visual-companion-darwin-arm64 ~/.local/bin/visual-companion
```

每个 release 都会发布 `SHA256SUMS`，在下载目录里用 `sha256sum -c SHA256SUMS`
即可校验。

macOS 上，从网络下载的未签名二进制会被 Gatekeeper 隔离，需要清除一次：

```bash
xattr -d com.apple.quarantine ~/.local/bin/visual-companion
```

Windows 首次运行会弹 SmartScreen 提示，点"仍要运行"即可。要彻底消除这两个
提示需要代码签名证书。

## 配置 agent

本 server 通过 stdio 讲 MCP。在你的宿主里注册它：

```json
{
  "mcpServers": {
    "visual-companion": {
      "command": "visual-companion"
    }
  }
}
```

**不需要填任何路径。** mockup 存在哪里，是在你启动伴侣的那一刻，根据客户端通过
MCP [roots](https://modelcontextprotocol.io/docs/concepts/roots) 上报的工作区
决定的——就是那个告诉 server "你可以碰哪些目录"的机制：

| 情况 | 会话落在哪里 |
|---|---|
| 客户端上报了工作区（dsh、opencode、Claude Code 及多数宿主） | `<工作区>/.visual-companion/` |
| 客户端不支持 roots | server 的工作目录，前提是它像个项目 |
| 两者都不可用 | 临时目录——mockup 不会在会话结束后保留 |
| 传了 `--project-dir` | 该目录，永远优先 |

这一点对**服务形态的 agent 很关键**：它们在workspace 出现之前就把 MCP server
拉起来了。此时进程的工作目录是启动器随手给的——经常是 `/` 或 `$HOME`。所以
路径不在进程启动时捕获，而是推迟到 `start_companion` 调用时。`list_sessions`
会报告最终选了哪个目录、以及为什么。

记得把 `.visual-companion/` 加进 `.gitignore`。

Claude Code：

```bash
claude mcp add visual-companion -- visual-companion
```

想显式固定位置也可以——适合脚本化部署，或客户端没实现 roots 的情况：

```bash
claude mcp add visual-companion -- visual-companion --project-dir /absolute/path/to/project
```

其他参数：

| 参数 | 默认值 | 用途 |
|---|---|---|
| `--project-dir`、`--session-dir` | 自动发现 | 把会话固定到该目录 |
| `--no-roots` | 关闭 | 不向客户端询问工作区 |
| `--host` | `127.0.0.1` | 绑定网卡；容器里用 `0.0.0.0` |
| `--url-host` | 由 `--host` 推导 | 展示给用户的 URL 里的主机名 |
| `--open` | 关闭 | 会话启动时自动打开浏览器 |
| `--version` | | 打印版本并退出 |

## agent 怎么用它

四个工具：

- **`start_companion`** —— 启动会话，返回 URL，并报告 mockup 会存在哪里。
  URL 带一个 per-session 密钥（`?key=…`），任何不带它的请求都会被拒绝。
  这能挡住误开的浏览器标签页，也能挡住同一网络上的其他机器读取你的屏幕内容
  或注入点击。
- **`push_screen`** —— 把一段 HTML 片段发布为某个命名设计的**新版本**。传
  `design: "dashboard-layout"`，每次修订都复用同一个名字：于是得到 v1、v2……
  不会覆盖任何东西，早先的轮次都留在磁盘上可供比较。
- **`get_events`** —— 读取当前屏幕推送以来的交互。对同一选项的重复点击会折叠成
  计数：一个犹豫的用户产出的是 "b x30"，而不是三十行；而每一次改变主意都被保留
  为序列：`a -> b x12` 一个 token 就说清了"来回摇摆了十二次"。
- **`list_sessions`** —— 列出活跃会话及其 URL、目录、待读交互数。只读，
  不会消耗点击。

行为契约——什么时候该提议用浏览器、哪些问题该上屏哪些该留在终端——写在
server 的 MCP `instructions` 里，所以它跟着工具走，而不是躺在某个需要 agent
自己去读的 skill 文件里。要点：

> 只在真正需要时临时提议，绝不提前提。内容是**视觉性**的（mockup、布局、架构图）
> 就用浏览器；答案是**文字**的（需求、范围、权衡）就留在终端。一个关于 UI 的
> 问题并不自动等于一个视觉问题。

## 设计与版本

给每个正在设计的东西起个名字，修订时用同一个名字再推一次：

```jsonc
// push_screen { design: "dashboard-layout", html: "..." }  -> dashboard-layout v1
// push_screen { design: "dashboard-layout", html: "..." }  -> dashboard-layout v2
// push_screen { design: "mobile-nav",       html: "..." }  -> mobile-nav v1
```

版本号出现在三个地方，所以你和 agent 永远在讨论同一轮：

- **浏览器里** —— 标题栏上的徽章：`dashboard-layout v2`
- **磁盘上** —— 目录名：`.visual-companion/<会话>/content/dashboard-layout/v2/screen.html`
- **工具返回值里** —— `design` 与 `version`

想并排比较的两个**不同方案**是两个 design，而不是同一个 design 的两个版本。
版本表示"同一个东西的修订"。

`list_sessions` 会报告每个设计及其最新版本，agent 不用猜就能接着往下做。

## 编写屏幕内容

默认写**内容片段**——不要 `<html>`、不要 CSS、不要 `<script>`。server 会自动
套进一个带主题的外壳，并提供你需要的 CSS 类：

```html
<h2>哪种布局更好？</h2>
<p class="subtitle">考虑可读性和视觉层级</p>

<div class="options">
  <div class="option" data-choice="a" onclick="toggleSelect(this)">
    <div class="letter">A</div>
    <div class="content">
      <h3>单栏</h3>
      <p>干净、专注的阅读体验</p>
    </div>
  </div>
  <div class="option" data-choice="b" onclick="toggleSelect(this)">
    <div class="letter">B</div>
    <div class="content">
      <h3>双栏</h3>
      <p>侧边导航加主内容区</p>
    </div>
  </div>
</div>
```

可用类：`.options`（加 `data-multiselect` 支持多选）、`.cards`、`.card`、
`.mockup`、`.mockup-header`、`.mockup-body`、`.split`、`.pros-cons`、
`.placeholder`、`.mock-nav`、`.mock-sidebar`、`.mock-content`、`.mock-button`、
`.mock-input`、`.section`、`.label`、`.subtitle`。

任何带 `data-choice` 的元素都会上报点击。以 `<!DOCTYPE` 或 `<html` 开头的文件
会被原样返回而不套壳，用于需要完全控制页面的场景。

修订就用同一个 design 名再推一次——版本号自增，上一轮原封不动。**不要原地修改
屏幕文件**：浏览器按 mtime 取最新文件，而覆盖写不一定会被可靠地识别为变化。

## 工作原理

```
agent ──MCP stdio──▶ visual-companion ──HTTP──▶ 浏览器标签页
                          │  ▲
      push_screen ────────┘  └──── GET / 按 mtime 返回最新屏幕
      get_events  ◀── state/events (JSONL) ◀── POST /events
                          │
   start_companion ───────┴──▶ 决定写到哪里：
                                --project-dir > 客户端 roots > 工作目录 > 临时目录

   磁盘上：  .visual-companion/<会话>/content/<设计>/v<N>/screen.html
                                          └ 共享素材放 assets/
```

- **位置是每个会话解析一次，不是进程启动时定死。** 见上面的表格。roots 查询
  发生在 `start_companion` 内部，因为那是工作区第一次被确认存在的时刻。
- **屏幕检测**每 250ms 轮询内容目录。用轮询而不是 inotify/fsnotify，是为了
  只用标准库并且各平台行为一致；250ms 相比人读一张 mockup 的时间可以忽略。
- **实时刷新**用 Server-Sent Events，不是 WebSocket。服务端到浏览器只有一条
  消息（`reload`），SSE 用 `net/http` 就够了，不需要帧编解码——而且浏览器的
  `EventSource` 自带重连。每 15 秒一次心跳注释帧，防止中间层掐断连接。
- **点击事件落盘**，不只是放内存，所以 agent 重启后点击还在。推送新屏幕会清空
  该文件：一个已经定下来的选择，它的陈旧点击不该泄漏到下一轮。
- **资产用 `go:embed` 内嵌**，所以二进制本身就是完整产品。

## 开发

```bash
go test ./...              # 单元、HTTP、总线、内存版 MCP，以及 e2e
go test -race ./...
go test -short ./...       # 跳过需要编译并拉起子进程的 e2e 测试
```

e2e 测试会编译真实二进制并通过真实 stdio 连接驱动它，因为内存版测试无法证明
**打包出来的产物**能工作。

一次构建全部发布产物加 `SHA256SUMS`：

```bash
VERSION=1.2.3 scripts/build-release.sh
```

该脚本是**产物命名的唯一来源**——一行安装法和 release workflow 都依赖它。
产物为静态链接，不依赖任何动态库。

## 发版

发版由 tag 驱动。CI 会先用你本地会跑的同一套检查把关（gofmt、`go vet`、
`go test -race`），然后构建五个二进制、断言静态链接、连同 `SHA256SUMS`
一起发布：

```bash
git tag v1.0.0
git push origin v1.0.0
```

`.github/workflows/ci.yml` 会在每次 push 和 PR 时于 Linux、macOS、Windows
三平台跑测试套件。

## 许可与致谢

浏览器资产（`assets/frame-template.html`、`assets/helper.js`）、CSS 类词汇表、
工具语义以及提示词指引，改编自 Jesse Vincent 的
[superpowers](https://github.com/obra/superpowers)，MIT 许可。
详见 [LICENSE](LICENSE)。
