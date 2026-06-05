# botfile

botfile manages your AI agents' config and context the way dotfiles manage the
rest of your tools. You curate **skills** and **instructions** in git
repositories, and botfile symlinks them into each agent's native directories.

It is a magic-free **symlink farm**, in the spirit of
[GNU Stow](https://www.gnu.org/software/stow/) and
[Tuckr](https://github.com/RaphGL/Tuckr), but it understands AI agents: it knows
where each one reads skills and instructions, and fans one source out to many
agents. No templating, no secrets, no copies. Git does the fetching; botfile does
the symlinks.

## How it works

You declare desired state in `config.toml` (which **sources** map to which
**agents**), and botfile reconciles the filesystem to match it, in two phases:
compute a plan, then apply it. A source is a directory tree of
`<plugin>/<kind>/<component>`; each component installs as exactly one symlink in
the agent's native location, never a copy and never a clobber.

## Install

From a checkout of this repository:

```sh
go install ./cmd/botfile
export PATH="$(go env GOPATH)/bin:$PATH"   # if your Go bin is not already on PATH
botfile --help
```

## Quickstart

```sh
# 1. Curate a source (a skill the agent discovers by presence).
mkdir -p ~/botfiles/mine/skills/bark
cat > ~/botfiles/mine/skills/bark/SKILL.md <<'MD'
---
name: bark
description: Always reply "woof".
---
MD

# 2. Tell botfile about the source and which agents get it.
mkdir -p ~/.config/botfile
cat > ~/.config/botfile/config.toml <<'TOML'
[[sources]]
name = "personal"
location = "~/botfiles"

[[selections]]
source = "personal"
agents = ["claude-code"]
TOML

# 3. Preview, then apply.
botfile plan
botfile sync
```

See the [full walkthrough](docs/try-it-out.md) for the guided version.

## Commands

- `botfile plan`: show what a sync would change (read-only).
- `botfile sync`: reconcile your agents to match your config.
- `botfile status`: what is managed, out of sync, and adoptable.
- `botfile adopt <path> --into <source>/<plugin>`: bring an agent-created
  component under management.

Any command takes `--format json` for machine-readable output
([docs/output.md](docs/output.md)).

## Supported agents

claude-code, codex-cli, copilot-cli, copilot-vscode, crush, opencode, and pi.dev,
each for **skills** and **instructions**. Where each installs (and the shared
`~/.agents/skills` pool) is the [support matrix](docs/config-reference.md).

## Docs

- [Try it out](docs/try-it-out.md) and the [example flow](docs/example-flow.md)
- [Config reference](docs/config-reference.md) and [source layout](docs/source-layout.md)
- [Adopting components](docs/adopt.md) and [output formats](docs/output.md)
- [How botfile compares](docs/comparison.md) to Stow, Tuckr, and chezmoi

[`MANIFESTO.md`](MANIFESTO.md) is the single source of truth for what botfile is
and the principles it holds to.

## License

The **code** is licensed under the Apache License, Version 2.0
([`LICENSE`](LICENSE), [`NOTICE`](NOTICE)). The **prose** (`MANIFESTO.md` and
`docs/`) is licensed under Creative Commons Attribution 4.0
([`LICENSE-docs.md`](LICENSE-docs.md)). Copyright 2026 Iain Maitland.
