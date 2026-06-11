---
sidebar_position: 8
title: Running from Source
---

# Running from Source

If you want to contribute to Gopernicus, test unreleased changes, or init projects from your own fork, you can run the tool directly from a local checkout.

## Fork and Clone

Fork the repository on GitHub, then clone it:

```bash
git clone https://github.com/<your-username>/gopernicus.git ~/code/gopernicus
```

Everything lives in one module — the framework and the generator tool (`workshop/gopernicus`) version together.

## Bootstrapping Projects from Your Checkout

`GOPERNICUS_DEV_SOURCE` tells `init` to use your local framework instead of a published release. The scaffolded project gets a `replace` directive pointing at your checkout, so `go tool gopernicus` inside it also runs your local code:

```bash
GOPERNICUS_DEV_SOURCE=~/code/gopernicus \
  go run ~/code/gopernicus/workshop/gopernicus init testapp --no-interactive
```

A shell function makes this comfortable — add to your `~/.bashrc`, `~/.zshrc`, or equivalent:

```bash
# Run the gopernicus tool from your local checkout; projects it
# scaffolds link your local framework too.
gopernicus-local(){
  GOPERNICUS_DEV_SOURCE=~/code/gopernicus \
  GOTOOLCHAIN=local \
    go run ~/code/gopernicus/workshop/gopernicus "$@"
}
```

Reload your shell (`source ~/.zshrc`) and verify:

```bash
gopernicus-local version
```

## Environment Variables

| Variable | Purpose |
|---|---|
| `GOPERNICUS_DEV_SOURCE` | Path to a local framework checkout. When set, `init` copies migrations, repositories, bridges, and documentation from this directory instead of fetching from the Go module cache, and writes a `replace` directive into the new project. |
| `GOTOOLCHAIN=local` | Forces Go to use your locally installed toolchain instead of auto-downloading a newer one. Useful when working offline or pinning a specific Go version. |

## Typical Workflow

1. Make changes to the framework or the generator (same repo)
2. Test with `gopernicus-local init testapp` — your local changes are used everywhere, including `go tool gopernicus` inside the test project
3. When ready, open a PR
