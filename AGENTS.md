# AGENTS.md

Guidance for AI coding agents working in this repository.
（中文要点见文末。）

## What this is

An MCP stdio server that gives a coding agent a browser tab for showing mockups
and reading back clicks. One static binary, no runtime dependencies.

The whole product is the binary: browser assets are embedded with `go:embed`,
so there is nothing to install alongside it. Anything that breaks that property
— a file read from disk at runtime, a new runtime dependency, an external asset
fetch — is a design regression, not a detail.

## Structure

```
main.go                       flag parsing, assets, MCP server wiring
internal/companion/
  server.go                   session lifecycle, screen polling, events, key
  browser.go                  HTTP routes, SSE stream, screen composition
  bus.go                      SSE fan-out
  registry.go                 multi-session registry, filename allocation
  mcp.go                      the three tools + user-facing guidance text
  launch.go                   per-platform browser launching
  roots.go                    where sessions live: flag > roots > cwd > temp
                              (design/version allocation lives in registry.go)
assets/
  frame-template.html         the frame the agent's fragments are wrapped in
  helper.js                   browser side: click capture, status, watchdog
scripts/
  build-release.sh            cross-compiles all artifacts + SHA256SUMS
  install.sh                  the user-facing one-liner installer
docs/
  agent-install.md            the copy-paste install prompt + failure modes
test/e2e/e2e_test.go          builds the binary and drives it over real stdio
.github/workflows/
  ci.yml                      test on linux/macos/windows, assert static link
  release.yml                 on tag: verify, build, publish
package.json                  declares the assets dir as CommonJS (see below)
```

## Commands

```bash
go test ./...          # everything
go test -race ./...    # required before you claim a change is safe
go vet ./... && gofmt -l .   # required to be clean
go test -short ./...   # skip the e2e tests that build and spawn a binary
VERSION=1.2.3 scripts/build-release.sh   # all release artifacts
```

If you touch concurrency (the bus, the poll loop, session state), run `-race`.
If you touch the tools, the assets, or startup, run the e2e suite — it is the
only thing that proves the shipped binary works, because everything else talks
to an in-memory transport.

`scripts/build-release.sh` is the single source of truth for artifact names.
`scripts/install.sh` and the release workflow both depend on those exact names,
so renaming an artifact means updating three places.

## Design decisions that look arbitrary but are not

**SSE, not WebSocket.** There is exactly one server-to-browser message
(`reload`). SSE covers it with `net/http` and no frame codec, and the browser's
`EventSource` reconnects on its own. The upstream Node implementation hand-rolled
RFC 6455 because Node's standard library has no WebSocket; Go's does not either,
but this design removes the need. Do not "upgrade" to WebSocket, and do not add
a WebSocket library. If you need a second server-to-browser message, add an SSE
event type.

**Polling, not fsnotify.** `os.ReadDir` every 250 ms keeps the standard library
sufficient and behaves identically on every platform. Do not add a file-watching
dependency to shave 250 ms off a human reading a mockup.

**No pid files, no watchdogs, no idle timeout.** The MCP host owns this
process's lifetime; that is the entire reason this rewrite exists. Adding
process supervision back means re-importing the problem the project was built to
delete.

**Events are cleared when a new screen arrives.** Stale clicks from an
already-resolved choice must not leak into the next round. Both the file and the
in-memory runs are reset — `s.recent` is what reads are served from. See
`Session.poll()`.

**Repeated clicks are collapsed; changes of mind are not.** `Session.recordEvent`
folds a repeated click on the same choice into a `Count` on the existing run, and
the file is rewritten so the audit trail agrees with what the agent was told.
`describeEvents` then folds an oscillating path (`a -> b x12`). Do not undo this
by storing one record per click: an undecided user generated hundreds of lines
the agent had to read to find the two that mattered. The compression must keep
three things visible — the true interaction count, every *change* of choice, and
the final selection — and `hysteresis_test.go` asserts each one.

**Session location is resolved per session, never at process launch.** The path
is decided inside `start_companion` (`resolveLocation`), in this order:
`--project-dir` > client MCP roots > working directory > temp. Service-shaped
hosts (dsh, opencode) boot this process *before* a workspace exists, so any path
captured at startup is the launcher's directory — often `/` or `$HOME`. Do not
"simplify" this by reading the flag or `os.Getwd()` once in `main`.
`usableWorkspace` deliberately rejects the filesystem root, a bare home
directory, and temp dirs.

**Roots have no reliable capability pre-check.** `ClientCapabilities.Roots` is a
value type that serializes as `"roots":{}` whether or not the client supports
roots, and `RootsV2` is tagged `json:"-"` so it never reaches the server. So
just call `ListRoots` with a timeout and treat any error as "no workspace to
report" — which is what the SDK's own internal root handling does too.

**Sessions always live under `<project>/.visual-companion/`.** One rule for
every source of the project directory, rather than a special case per source.

**Screens are versioned, never overwritten.**
`.visual-companion/<session>/content/<design>/v<N>/screen.html`. A design name
plus an incrementing version is the human's handle on "which round is this" —
it shows in the browser badge, in the path, and in the tool result. Allocation
(`Session.nextScreen`) is serialized by `versionMu` and derives the next number
from the highest existing version directory, so concurrent pushes cannot collide
and a version directory deleted by hand is the only way a number gets reused.
Do not "simplify" this into overwriting one file per design: the whole point is
that earlier rounds stay reviewable.

**Screen filenames are never reused.** The browser selects the newest file by
mtime, and an overwrite does not reliably look like a change. Allocation lives
in `Session.nextScreenPath`.

**The key gates everything.** Every route and the SSE stream require the
per-session key from `?key=` or the session cookie. If you add a route, call
`guard()` first. The key is compared in constant time, intentionally.

## Pitfalls that have already bitten

**`package.json` must keep `"type": "commonjs"`.** `assets/helper.js` is a
classic browser script that also does `module.exports` so Node can unit-test its
pure helpers. Inside a parent package that sets `"type": "module"`, that export
becomes a silent no-op and `require()` returns `{}` with no error. The file is
there solely to pin this directory's module type. `TestHelperPureFunctions`
asserts the exports are reachable; if it reports `{}`, this file was removed or
changed.

**Never wait on `currentScreen()` after writing a screen.** It rescans the
directory on demand, so it reports the freshly written file immediately —
before the poll loop has bumped the generation or cleared stale events.
`push_screen` therefore waits on `Generation()` advancing
(`waitForGeneration`). Getting this wrong means the agent reads the *previous*
screen's clicks on its next turn, which is silent and confusing rather than
loud.

**Do not populate `CallToolResult.StructuredContent`.** The SDK's
`ToolHandlerFor` fills it from the typed output value. Setting it by hand
double-encodes the result.

**Nothing may write to stdout.** The stdio transport owns it; a stray `print`
corrupts the JSON-RPC stream. Diagnostics go to the session's `state/server.log`
(`Session.logEvent`) or to stderr via `log`.

**`install.sh` verifies checksums by default.** `VERIFY=auto` is the default:
it checks SHA256 when `SHA256SUMS` is present and warns loudly when it is not.
`VERIFY=1` turns a missing checksums file into a hard error; `VERIFY=0` skips
the check entirely. Do not flip the default back to "verify only when asked" —
publishing checksums that nobody checks is theatre.

**The README install section is a prompt, not a command list.** It is written to
be pasted into an agent, because an MCP server's installer is usually an agent.
Keep it copy-pasteable as one block; keep the human-facing details below it.

**The version is injected at build time.** `main.go` defaults to `"dev"` and
`scripts/build-release.sh` sets it with
`-ldflags "-X main.version=$VERSION"`. The same string is sent to the MCP client
as the server version, so a release built without the flag will announce itself
as `dev`.

**Multi-select events carry the authoritative selection set.** `helper.js` sends
`selected: [...]` on every event from a `data-multiselect` container, and
`Event.Selected` must survive `recordEvent` — dropping it made tick and untick
look identical, so they merged into one run and the final set was lost.
`sameStrings` is part of the dedupe key for the same reason. In
`describeEvents`, a multi-select question reports "Currently ticked" and must
NOT also report the last click as "Currently selected": the last click may have
been the one that removed an option.

## `assets/` is a shared contract

`frame-template.html` and `helper.js` define the vocabulary the agent writes
against: the CSS class names, the `data-choice` attribute, `toggleSelect`, and
the `brainstorm` JS API. They are adapted from
[superpowers](https://github.com/obra/superpowers) and are the most
behaviorally-tuned part of this project — the class contract is what lets an
agent write a fragment with no CSS at all.

Treat them as a public API:

- Adding classes is fine. Renaming or removing them breaks every prompt,
  skill, and cached instruction that mentions them.
- Keep `helper.js`'s pure predicates (`isStale`, `shouldShowOverlay`) exported
  and testable; they encode the reconnect policy.
- The overlay and status pill exist because `EventSource` reconnects silently —
  without them a dead server looks like an idle page.

The same applies to the guidance text in `ServerInstructions` and the tool
descriptions in `mcp.go`. That text is the behavioral contract: when to offer
the browser at all, and which questions belong on screen versus in the terminal.
It was tuned against real sessions. Do not rewrite it for style.

## If you are asked to change behavior

Say what actually changed and what it costs. This project's value is mostly in
decisions that are invisible in the diff:

- deleting process supervision (safe, because MCP hosts own lifetime)
- choosing SSE over WebSocket (safe, because one message)
- clearing events per screen (required, or feedback cross-contaminates)
- embedding assets (required, or the install story dies)

A change that makes any of those "more conventional" is probably a regression.
Ask before making it.

## Licensing

MIT, with the superpowers attribution preserved in `LICENSE`. If you copy code
or assets in, keep the notices accurate. If you copy code or assets *out*, keep
the upstream copyright notice with them.

---

## 中文要点

- **`package.json` 里的 `"type": "commonjs"` 不能删**：否则 `helper.js` 的
  `module.exports` 会静默失效（父目录若声明 ESM）。测试报 `{}` 就是这个原因。
- **写完屏幕后不要等 `currentScreen()`**：它会自扫目录立刻返回，而轮询还没清
  空上一轮的点击。必须等 `Generation()` 递增，否则 agent 下一轮会读到旧点击。
- **stdout 归 MCP 传输所有**，任何打印都会破坏 JSON-RPC；诊断写
  `state/server.log` 或 stderr。
- **不要手动设置 `StructuredContent`**，SDK 会从类型化返回值自动填充。
- **SSE 和轮询是有意为之**，不要把 WebSocket、fsnotify、守护进程/看门狗加回来。
- **会话位置在每个会话解析一次，绝不在进程启动时捕获**：服务形态的宿主
  （dsh、opencode）在工作区存在之前就把进程拉起来了，启动时的目录往往是 `/`
  或 `$HOME`。顺序是 `--project-dir` > 客户端 roots > 工作目录 > 临时目录。
- **roots 没有可靠的能力预检**：`Roots` 是值类型（无论支不支持都序列化成
  `"roots":{}`），`RootsV2` 又是 `json:"-"`。所以直接带超时调用、失败即降级。
- **`assets/` 和 `mcp.go` 里的指引文本是对外契约**，可以加类名，不要改名或
  重写。它们定义了 agent 能用的词汇和"何时该提议用浏览器"的行为。
- **屏幕按 <design>/v<N>/ 版本化，绝不覆盖**：版本号是人对"这是第几轮"的抓手，
  同时出现在浏览器徽章、目录路径和工具返回值里。版本分配由 `versionMu` 串行化，
  取"现存最高版本 +1"。不要退化成每个设计只留一个文件——保留历史轮次才是重点。
- **会话目录名形如 `20260926-150405-a604`**（可排序、可读），不要退回纯数字 ID。
- 改动并发代码必须跑 `go test -race ./...`；改工具或资产必须跑 e2e。
