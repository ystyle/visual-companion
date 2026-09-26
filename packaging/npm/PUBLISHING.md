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

Check which token you are actually using before blaming anything else. This
machine demonstrates both failure modes at once:

- `~/.npmrc` holds a token scoped to `registry.npmjs.org` that has since been
  revoked. `npm whoami` against the official registry answers `{}` or 401.
- The valid token lives in the `NPM_TOKEN` environment variable, exported from
  `~/.zshrc`. **npm does not read `NPM_TOKEN` on its own** — an env var is not
  an `.npmrc` entry, so `npm whoami` fails even though a working token is right
  there in the environment.

Two consequences worth remembering:

1. A non-interactive shell does not source `~/.zshrc`. `zsh -c 'echo $NPM_TOKEN'`
   prints nothing; `zsh -ic` (interactive) does. That difference cost more time
   here than the actual publish did.
2. The registry file and the environment variable can disagree. Check both:

```bash
zsh -ic 'npm whoami --registry=https://registry.npmjs.org/'                 # uses ~/.npmrc
zsh -ic 'curl -sS -H "Authorization: Bearer $NPM_TOKEN" \
  https://registry.npmjs.org/-/whoami'                                      # uses the env var
```

The second returning `{"username":"..."}` while the first returns 401 means the
env var is the good one.

Also note that `~/.npmrc` may point `registry` at a mirror (npmmirror here), so
`whoami` and `publish` would go somewhere your npmjs token means nothing. Always
pass the registry explicitly.

Publishing with the environment token, without writing it to disk anywhere
`git` can see:

```bash
cd packaging/npm
printf "//registry.npmjs.org/:_authToken=%s\n" "$NPM_TOKEN" > .npmrc   # gitignored
chmod 600 .npmrc
npm publish
rm -f .npmrc
```

Or mint a fresh granular token with *Read and write* permission at
<https://www.npmjs.com/settings/~/tokens> and fix `~/.npmrc` properly.

## A 404 on PUT does not mean the name is taken

`npm publish` answers

```
404 Not Found - PUT https://registry.npmjs.org/<name>
```

both when the token is rejected and when the package genuinely does not exist
yet. npm deliberately does not distinguish them. If a GET of the same name also
404s, the name is free and the problem is the token.

## A successful publish is not immediately readable

The write path and the read path are different services. `npm publish` returned
`200` for the PUT while `GET /visual-companion` kept answering `404` for roughly
twenty seconds afterwards. Check the log for the PUT status before concluding a
publish failed:

```bash
grep "http fetch PUT" ~/.npm/_logs/*-debug-0.log | tail -1
```

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
