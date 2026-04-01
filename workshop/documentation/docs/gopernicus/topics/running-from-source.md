---
sidebar_position: 8
title: Running from Source
---

# Running from Source

If you want to contribute to Gopernicus, test unreleased changes, or init projects from your own fork, you can run the CLI directly from source.

## Fork and Clone

Fork both repositories on GitHub, then clone them into the same parent directory:

```bash
mkdir -p ~/code/gopernicus && cd ~/code/gopernicus

# Framework (the Go module with core, bridge, infrastructure, etc.)
git clone https://github.com/<your-username>/gopernicus.git gopernicus

# CLI (the gopernicus command)
git clone https://github.com/<your-username>/gopernicus-cli.git gopernicus-cli
```

## Set Up a Go Workspace (optional)

A [Go workspace](https://go.dev/doc/tutorial/workspaces) lets the CLI resolve the framework module locally without publishing:

```bash
cd ~/code/gopernicus
go work init ./gopernicus ./gopernicus-cli
```

This creates a `go.work` file that tells Go to use your local copies instead of fetching from the module proxy.

## Shell Aliases

Add these to your `~/.bashrc`, `~/.zshrc`, or equivalent:

```bash
# Run the CLI from source, using your local framework checkout.
# GOPERNICUS_DEV_SOURCE tells `gopernicus init` to copy files from your
# local framework instead of downloading from the module cache.
gopernicus-local(){
  GOPERNICUS_DEV_SOURCE=~/code/gopernicus/gopernicus \
  GOWORK=~/code/gopernicus/go.work \
  GOTOOLCHAIN=local \
    go run ~/code/gopernicus/gopernicus-cli "$@"
}

# Run the CLI from source without the dev source override.
# Uses the published framework module but your local CLI code.
gopernicus(){
  GOWORK=~/code/gopernicus/go.work \
    go run ~/code/gopernicus/gopernicus-cli "$@"
}
```

Reload your shell (`source ~/.zshrc`) and verify:

```bash
gopernicus-local version
```

## Environment Variables

| Variable | Purpose |
|---|---|
| `GOPERNICUS_DEV_SOURCE` | Path to a local framework checkout. When set, `gopernicus init` copies migrations, repositories, bridges, and documentation from this directory instead of fetching from the Go module cache. |
| `GOWORK` | Path to a `go.work` file. Tells Go to resolve modules from the workspace rather than the module proxy. |
| `GOTOOLCHAIN=local` | Forces Go to use your locally installed toolchain instead of auto-downloading a newer one. Useful when working offline or pinning a specific Go version. |

## Typical Workflow

1. Make changes to the framework (`gopernicus/`) or CLI (`gopernicus-cli/`)
2. Test with `gopernicus-local init testapp` — your local changes are used
3. When ready, open PRs against both repos as needed
