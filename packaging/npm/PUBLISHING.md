# Publishing the npm placeholder

`visual-companion` on npm is a **name reservation**, not a distribution channel.
The real program is a single static binary; see the project README.

This is deliberately not wired into CI. Re-publishing is a rare, manual act, and
a workflow that fires on every tag would burn versions on an empty package.

## Why a placeholder at all

The name is worth more than the work of claiming it. Anyone searching npm for a
"visual companion" or "MCP brainstorming" tool would otherwise find a squatter,
and the cost of reclaiming a name is far higher than the cost of holding it.

## One-time setup

The token in `~/.npmrc` must be able to publish the `visual-companion` package.
A stale token fails in a confusing way: `npm publish` reports

```
404 Not Found - PUT https://registry.npmjs.org/visual-companion
```

That is npm's response to a token it does not accept — it deliberately does not
distinguish "no such package" from "you may not create it", so a 404 on PUT
almost always means the token, not the name.

Check the token before blaming anything else:

```bash
npm whoami --registry=https://registry.npmjs.org/
```

`{}` or a 401 means the token is revoked or expired. Mint a new **granular
access token** with *Read and write* permission at
<https://www.npmjs.com/settings/~/tokens>, and put it in `~/.npmrc`:

```
//registry.npmjs.org/:_authToken=npm_xxxxxxxxxxxxxxxx
```

Note that `~/.npmrc` may point `registry` at a mirror (npmmirror, for instance).
`whoami` and `publish` then go to the mirror, which does not know about your
npmjs token. Always pass the registry explicitly, as the commands below do.

## Publishing

```bash
cd packaging/npm
npm version <x.y.z> --no-git-tag-version   # keep in step with the Git tag
npm publish --registry=https://registry.npmjs.org/
```

Then verify:

```bash
npm view visual-companion version --registry=https://registry.npmjs.org/
```

## Replacing the placeholder with a real package

If npm distribution is ever wanted, the pattern is one package per platform
holding the binary, plus this package as the entry point:

```
visual-companion                bin -> launcher.js, optionalDependencies on the below
visual-companion-darwin-arm64   the binary
visual-companion-linux-x64      the binary
...
```

The launcher picks the artifact for `process.platform` / `process.arch` and
spawns it. Weigh that against the point of the project first: it exists so the
target machine needs **no runtime**, and a machine without Node cannot use an
npm package. Users who do have Node are already served by
`scripts/install.sh` in one line.
