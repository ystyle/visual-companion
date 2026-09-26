# visual-companion (placeholder)

**This package intentionally contains no code.** It exists so the name is not
taken by something unrelated before the real npm packaging lands.

The actual program is an MCP stdio server that gives a coding agent a browser
tab for showing mockups, diagrams, and visual options during a brainstorming
session. The agent pushes HTML fragments; you look and click; the agent reads
the clicks back on its next turn.

## Install the real thing

It ships as a **single static binary** with the browser assets embedded — no
Node, no npm, no runtime. The installer detects your platform, verifies the
SHA256, installs to `~/.local/bin`, and clears the macOS quarantine flag:

```bash
curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
```

Or download the artifact for your platform from the
[releases page](https://github.com/ystyle/visual-companion/releases).

## Configure your agent

```bash
claude mcp add visual-companion -- visual-companion
```

No path argument is needed: the server discovers your workspace over MCP roots
when a companion starts.

See the [project README](https://github.com/ystyle/visual-companion#readme) for
screen-authoring conventions, the design/version scheme, and how it works.

## License

MIT. The browser assets are adapted from
[Superpowers](https://github.com/obra/superpowers) by Jesse Vincent.
