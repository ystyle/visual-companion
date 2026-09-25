# Install visual-companion (instructions for a coding agent)

Everything below is written to be read and executed by an agent. A human can
paste the block in [Copy-paste prompt](#copy-paste-prompt) into their agent and
get a working companion without touching a terminal themselves.

Facts you need, so you do not have to guess:

- It is a **single static binary**. No Go, no Node, no runtime, nothing to
  compile. Copy one file and it runs.
- It is an **MCP server over stdio**. It belongs in the host's MCP server list,
  not in `PATH` alone.
- It **discovers the workspace over MCP roots** at `start_companion` time, so it
  normally needs **no arguments at all**. Do not invent a `--project-dir`; only
  pass one if the user explicitly asks for a fixed location.
- Install script: `https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh`
- Project page: `https://github.com/ystyle/visual-companion`

## Copy-paste prompt

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

## What the agent should end up doing

1. **Install the binary**

   ```bash
   curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
   ```

   The script **verifies the SHA256 by default** and installs to
   `~/.local/bin`. Useful overrides:

   | Variable | Effect |
   |---|---|
   | `INSTALL_DIR=/somewhere` | Install somewhere else |
   | `VERSION=1.2.3` | Pin a release instead of `latest` |
   | `VERIFY=1` | Make a *missing* `SHA256SUMS` a hard error too |
   | `VERIFY=0` | Skip the checksum check — only for mirrors without checksums |

2. **Make sure it is reachable**

   ```bash
   command -v visual-companion || echo "not on PATH"
   "$HOME/.local/bin/visual-companion" --version
   ```

   Expected output: `visual-companion vX.Y.Z`. If `~/.local/bin` is missing from
   `PATH`, append it to the user's shell profile (`~/.bashrc`, `~/.zshrc`, or
   `~/.config/fish/config.fish`) and tell them to open a new shell.

3. **Register it as an MCP server**

   Claude Code:

   ```bash
   claude mcp add visual-companion -- visual-companion
   ```

   Any other host — add this entry to its MCP config:

   ```json
   {
     "mcpServers": {
       "visual-companion": {
         "command": "visual-companion"
       }
     }
   }
   ```

   If `command` must be absolute, use the path the installer reported.

4. **Verify**

   ```bash
   claude mcp list        # Claude Code
   ```

   The server should appear. In a new session, its four tools
   (`start_companion`, `push_screen`, `get_events`, `list_sessions`) should be
   available. Until the host restarts, the tools will not be callable even
   though registration already succeeded — say so rather than debugging it.

5. **Do not start a brainstorm.** Installing is not the same as using. Tell the
   user it is ready and that the companion is offered when a question would be
   clearer shown than described.

## Failure modes and what they mean

| Symptom | Cause | What to do |
|---|---|---|
| `curl: (22) ... 404` | No release published yet, or a typo in the URL | Check `https://github.com/ystyle/visual-companion/releases` |
| `checksum mismatch` | Truncated or tampered download | Retry; if it repeats, report it rather than bypassing the check |
| `unsupported architecture` | No build for that CPU | Download manually from the releases page, or build from source |
| `command not found` after install | `~/.local/bin` not on `PATH` | Add it to the shell profile, open a new shell |
| Tools missing in the host | Host has not reloaded MCP servers | Restart the host or start a new session |
| macOS refuses to run it | Gatekeeper quarantine | `xattr -d com.apple.quarantine ~/.local/bin/visual-companion` (the installer normally does this) |
| Windows SmartScreen warning | Unsigned binary | "More info" → "Run anyway"; there is no signed build |

## Uninstall

```bash
claude mcp remove visual-companion         # Claude Code
rm -f ~/.local/bin/visual-companion        # the binary
rm -rf <project>/.visual-companion         # saved mockups, per project
```
