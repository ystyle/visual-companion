# visual-companion

**English** | [简体中文](README.zh-CN.md)

An MCP server that gives a coding agent a browser tab for showing mockups,
diagrams, and visual options during a brainstorming session.

The agent never sees the page. It pushes an HTML fragment; you look at it in a
browser and click; the agent reads your clicks back on its next turn.

## Why this exists

This is a Go reimplementation of the brainstorming visual companion from
[superpowers](https://github.com/obra/superpowers), built for one reason:
**install-side zero dependency**. It ships as a single static binary with the
browser assets embedded, so a machine needs no Node, no runtime, and no
build toolchain — copy one file and run it.

Upstream solves "keep the browser alive between the agent's turns" with
`nohup`, pid files, an owner-PID watchdog, and per-platform backgrounding
quirks. Speaking MCP instead means the host owns the process lifetime, so all
of that disappears.

## Install

**Paste this into your agent.** It is the install path this tool was designed
for: an MCP server's installer is usually an agent anyway.

> Install the visual-companion MCP server for me.
>
> It's a single static binary that speaks MCP over stdio — no Node, no runtime,
> nothing to compile. Run this to install it:
>
> ```
> curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
> ```
>
> That detects the platform, verifies the SHA256, installs to `~/.local/bin`,
> and clears the macOS quarantine flag. If `~/.local/bin` isn't on my `PATH`,
> add it to my shell profile. If the download fails, the script prints the
> manual fallback URL — use it.
>
> Then register it with yourself as an MCP server named `visual-companion`,
> running the `visual-companion` binary with no arguments (it auto-discovers my
> workspace). Use whatever mechanism you have for adding MCP servers — for
> Claude Code that's `claude mcp add visual-companion -- visual-companion`,
> otherwise edit my MCP config JSON. Then confirm it's registered and that
> `visual-companion --version` runs.
>
> Don't start a brainstorm yet. Just tell me it's ready, and remind me to add
> `.visual-companion/` to my project's `.gitignore`.

A longer, failure-aware version of the same thing lives in
[docs/agent-install.md](docs/agent-install.md) — point your agent at that file
if the paste above leaves it guessing.

### Doing it yourself

```bash
curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
```

The script detects your platform, **verifies the SHA256 by default**, installs
to `~/.local/bin`, and clears the macOS quarantine flag. Read it first if you
like (it is short), or override the defaults:

```bash
# pick a location, or pin a version
curl -fsSL .../install.sh | INSTALL_DIR=~/.local/share/bin VERSION=1.2.3 bash
```

`VERIFY=0` skips the checksum check. Only do that if you are installing from a
mirror that does not carry `SHA256SUMS`.

### Manual install

Download the artifact for your platform from the releases page and put it on
your `PATH`. That is the whole install.

| Platform | Artifact |
|---|---|
| macOS (Apple Silicon) | `visual-companion-darwin-arm64` |
| macOS (Intel) | `visual-companion-darwin-amd64` |
| Linux (x86-64) | `visual-companion-linux-amd64` |
| Linux (arm64) | `visual-companion-linux-arm64` |
| Windows (x86-64) | `visual-companion-windows-amd64.exe` |

```bash
chmod +x visual-companion-darwin-arm64
mv visual-companion-darwin-arm64 ~/.local/bin/visual-companion
```

Each release publishes `SHA256SUMS`; verify with
`sha256sum -c SHA256SUMS` from the download directory.

On macOS, a downloaded unsigned binary is quarantined by Gatekeeper. Clear it
once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/visual-companion
```

## Configure your agent

The server speaks MCP over stdio. Register it with your host:

```json
{
  "mcpServers": {
    "visual-companion": {
      "command": "visual-companion"
    }
  }
}
```

**No path is required.** Where mockups are stored is worked out when you start a
companion, from the workspace your client reports over MCP
[roots](https://modelcontextprotocol.io/docs/concepts/roots) — the same
mechanism that tells a server which directories it may touch:

| Situation | Where sessions go |
|---|---|
| Client reports a workspace (dsh, opencode, Claude Code, and most hosts) | `<workspace>/.visual-companion/` |
| No roots support | the server's working directory, if it looks like a project |
| Neither is usable | a temp directory — mockups won't outlive the session |
| `--project-dir` given | that directory, always wins |

This matters for service-shaped agents, which start the MCP server **before** a
workspace exists. A path captured at process launch would be whatever directory
the launcher happened to use — often `/` or `$HOME` — so nothing is captured
until `start_companion` runs. `list_sessions` reports which directory was chosen
and why.

Add `.visual-companion/` to your `.gitignore`.

For Claude Code:

```bash
claude mcp add visual-companion -- visual-companion
```

Pin the location explicitly if you prefer — useful for scripted setups, or when
your client does not implement roots:

```bash
claude mcp add visual-companion -- visual-companion --project-dir /absolute/path/to/project
```

Other flags:

| Flag | Default | Purpose |
|---|---|---|
| `--project-dir`, `--session-dir` | auto-discovered | Pin sessions to this directory |
| `--no-roots` | off | Don't ask the client for its workspace |
| `--host` | `127.0.0.1` | Interface to bind; use `0.0.0.0` in a container |
| `--url-host` | derived from `--host` | Hostname to put in the URL shown to the user |
| `--open` | off | Open the browser automatically when a session starts |
| `--version` | | Print the version and exit |

## How the agent uses it

Four tools:

- **`start_companion`** — starts a session, returns the URL, and reports where
  its mockups will live. The URL carries a per-session key (`?key=…`); every
  request without it is rejected, which keeps a stray browser tab or another
  machine on the network from reading your screens or injecting clicks.
- **`push_screen`** — publishes an HTML fragment as a new version of a named
  design. Pass `design: "dashboard-layout"` and reuse the same name on every
  revision: you get v1, then v2, and so on. Nothing is overwritten, so earlier
  rounds stay on disk for comparison.
- **`get_events`** — reads the interactions recorded since the current screen
  was pushed. Repeat clicks on the same choice collapse into a count, so an
  undecided user produces "b x30" rather than thirty lines, while every change
  of mind is preserved as a sequence: `a -> b x12` says "they oscillated twelve
  times" in one token.
- **`list_sessions`** — lists live sessions with their URL, directory, and how
  many interactions are waiting. Read-only: it does not consume clicks.

The behavioral contract — when to offer the browser at all, and which questions
belong on screen versus in the terminal — ships in the server's MCP
`instructions`, so it travels with the tool instead of living in a skill file
the agent has to go read. The short version:

> Offer it just in time, never upfront. Use the browser for content that *is*
> visual (mockups, layouts, diagrams); use the terminal for anything whose
> answer is words (requirements, scope, tradeoffs). A question about a UI topic
> is not automatically a visual question.

## Designs and versions

Give each thing you are designing a name, and revise it by pushing the same name
again:

```jsonc
// push_screen { design: "dashboard-layout", html: "..." }  -> dashboard-layout v1
// push_screen { design: "dashboard-layout", html: "..." }  -> dashboard-layout v2
// push_screen { design: "mobile-nav",       html: "..." }  -> mobile-nav v1
```

The version is visible three places, so you and the agent always agree on which
round you are discussing:

- **In the browser**, as a badge in the header — `dashboard-layout v2`
- **On disk**, as the directory — `.visual-companion/<session>/content/dashboard-layout/v2/screen.html`
- **In the tool result**, as `design` and `version`

Two alternatives you want to compare side by side are two *designs*, not two
versions of one. Versions are for "the same thing, revised".

`list_sessions` reports every design and its latest version, so the agent can
pick up where it left off without guessing.

## Authoring screens

Write content fragments by default — no `<html>`, no CSS, no `<script>`. The
server wraps them in a themed frame that provides the CSS classes you need:

```html
<h2>Which layout works better?</h2>
<p class="subtitle">Consider readability and visual hierarchy</p>

<div class="options">
  <div class="option" data-choice="a" onclick="toggleSelect(this)">
    <div class="letter">A</div>
    <div class="content">
      <h3>Single Column</h3>
      <p>Clean, focused reading experience</p>
    </div>
  </div>
  <div class="option" data-choice="b" onclick="toggleSelect(this)">
    <div class="letter">B</div>
    <div class="content">
      <h3>Two Column</h3>
      <p>Sidebar navigation with main content</p>
    </div>
  </div>
</div>
```

Add `data-multiselect` to a `.options` or `.cards` container to let the user tick
several choices. Each event from a multi-select container also carries the full
set ticked at that moment, because a click alone cannot express "I just
*unticked* that" — without it, `selected A, selected C, unticked C` reads
exactly like `selected C twice`.

Available classes: `.options` (+ `data-multiselect`), `.cards`, `.card`,
`.mockup`, `.mockup-header`, `.mockup-body`, `.split`, `.pros-cons`,
`.placeholder`, `.mock-nav`, `.mock-sidebar`, `.mock-content`, `.mock-button`,
`.mock-input`, `.section`, `.label`, `.subtitle`.

Anything carrying `data-choice` reports a click. A file starting with
`<!DOCTYPE` or `<html` is served as-is instead of being wrapped, for when you
need full control.

Revise by pushing the same design name again — the version increments and the
previous round stays untouched. Never edit a screen file in place: the browser
picks the newest file by mtime, and an overwrite does not reliably look like a
change.

## How it works

```
agent ──MCP stdio──▶ visual-companion ──HTTP──▶ browser tab
                          │  ▲
      push_screen ────────┘  └──── GET / serves newest screen by mtime
      get_events  ◀── state/events (JSONL) ◀── POST /events
                          │
   start_companion ───────┴──▶ resolves where to write:
                                --project-dir > client roots > cwd > temp

   on disk:  .visual-companion/<session>/content/<design>/v<N>/screen.html
                                          └ shared assets in assets/
```

- **Location is resolved per session, not per process.** See the table above;
  the roots query happens inside `start_companion` because that is the first
  moment the workspace is known to be real.
- **Screen detection** polls the content directory every 250 ms. Polling rather
  than inotify/fsnotify keeps the standard library sufficient and behaves the
  same on every platform; 250 ms is invisible next to a human reading a mockup.
- **Live reload** uses Server-Sent Events, not WebSocket. There is exactly one
  server-to-browser message (`reload`), so SSE does the job with `net/http` and
  no frame codec — and the browser's `EventSource` reconnects on its own. A
  heartbeat comment every 15 s keeps intermediaries from closing the stream.
- **Events are written to disk** as well as held in memory, so a click survives
  an agent restart. A new screen clears the file: stale clicks from a resolved
  choice must not leak into the next round.
- **Assets are embedded** with `go:embed`, so the binary is the whole product.

## Develop

```bash
go test ./...              # unit, HTTP, bus, MCP-over-memory, and e2e
go test -race ./...
go test -short ./...       # skips the build-and-spawn e2e tests
```

The e2e tests compile the binary and drive it over a real stdio connection,
because the in-memory tests cannot prove the shipped artifact works.

Build every release artifact plus `SHA256SUMS`:

```bash
VERSION=1.2.3 scripts/build-release.sh
```

That script is the single source of truth for artifact names — the install
one-liner and the release workflow both depend on them. The output is statically
linked with no dynamic library dependencies.

## Releasing

The release is driven by a tag. CI gates it on the same checks you would run
locally (gofmt, `go vet`, `go test -race`), then builds the five binaries,
asserts they are statically linked, and publishes them with `SHA256SUMS`:

```bash
git tag v1.0.0
git push origin v1.0.0
```

`.github/workflows/ci.yml` runs the test suite on Linux, macOS, and Windows for
every push and pull request.

## Credits

The browser assets (`assets/frame-template.html`, `assets/helper.js`), the CSS
class vocabulary, the tool semantics, and the prompt guidance are adapted from
[superpowers](https://github.com/obra/superpowers) by Jesse Vincent, MIT
licensed. See `LICENSE`.
