# botfile

**Dotfiles for your coding agents.** botfile manages the config and context your
AI coding agents load (skills, memories) the same way dotfiles manage the rest of
your tools: you keep components in git repositories, and botfile symlinks them
into each agent's native directories.

It is a symlink farm in the lineage of [GNU Stow](https://www.gnu.org/software/stow/)
and [Tuckr](https://github.com/RaphGL/Tuckr), with three things they don't have:

- **Multiple sources.** Compose a public team repo of shared standards with a
  private personal repo of your own components.
- **Fan-out across agents.** One source maps to many agents; you pick which agent
  gets which components.
- **Agent-native, cross-platform.** botfile knows where each agent looks
  (`~/.claude/skills/`, `~/.agents/skills/`, ...) and runs on Linux, macOS, and
  Windows.

No magic: botfile only ever creates and removes its own symlinks. It never
rewrites your files, never injects hooks, and never clobbers anything it does not
own.

> Status: early, and built forward from [`MANIFESTO.md`](MANIFESTO.md). The
> engine (plan and sync) works end to end; the interactive TUI and some agents
> are still to come.

## How it works

botfile is **declarative**: you describe the desired state in `config.toml`, and
botfile reconciles the filesystem to match it, in two phases (like Stow): compute
a plan, then apply it.

```
config.toml + your source repos
   -> scan sources into components
   -> project selections onto agents
   -> read the current filesystem
   -> reconcile  =>  a plan (create / replace / remove symlinks)
   -> apply
```

`botfile plan` shows exactly what `sync` would do and touches nothing. `botfile
sync` applies it, and is idempotent: run it again and nothing changes. It is safe
by construction: it removes only a symlink it created and still owns, reports a
conflict instead of overwriting a file you authored, and refuses to apply a plan
built from a malformed source or config.

## Quickstart

Clone your source repositories yourself (botfile does not clone; that is git's
job):

```sh
git clone <your-team-repo>     ~/src/agent-standards
git clone <your-personal-repo> ~/src/my-botfile
```

Write `~/.config/botfile/config.toml`:

```toml
[[sources]]
name = "team"
location = "~/src/agent-standards"

[[sources]]
name = "personal"
location = "~/src/my-botfile"

# Everything from the team source to two agents.
[[selections]]
source = "team"
agents = ["claude-code", "codex-cli"]

# Your personal components to claude-code only.
[[selections]]
source = "personal"
agents = ["claude-code"]
```

A source is a directory tree with the grammar `<plugin>/<kind>/<component>`: a
skill is a directory with a `SKILL.md`, a memory is a `<name>.md` file.

```
agent-standards/
  standards/
    skills/code-review/SKILL.md
    memories/go-conventions.md
```

Then:

```sh
botfile plan    # preview, touches nothing
botfile sync    # create the symlinks
```

See [docs/example-flow.md](docs/example-flow.md) for the full walkthrough,
including fan-out, precedence, and orphan cleanup.

## Supported agents

| Agent            | skills | memory |
|------------------|:------:|:------:|
| `claude-code`    |   yes  |  yes   |
| `codex-cli`      |   yes  |   -    |
| `copilot-cli`    |   yes  |   -    |
| `copilot-vscode` | pending | pending |
| `opencode`       | pending | pending |
| `pi.dev`         | pending | pending |

botfile supports a component only where the agent discovers it natively (or via a
small, structured registration), so it stays a plain symlink farm. Where an agent
cannot take a component kind, that selection is reported and skipped, never an
error. See [docs/config-reference.md](docs/config-reference.md#support-matrix) for
the exact install paths.

## Build

botfile is written in Go (module `codeberg.org/botfile/botfile`). From a checkout:

```sh
go build -o botfile ./cmd/botfile
go test ./...
```

On Windows, symlink creation requires Developer Mode.

The repository ships a pre-commit hook that runs `gofmt`, `go vet`, `go build`,
and `go test` before each commit (the same checks a CI job would). Enable it once
per clone:

```sh
git config core.hooksPath .githooks
```

## Documentation

- [docs/example-flow.md](docs/example-flow.md): a complete team + personal
  walkthrough.
- [docs/config-reference.md](docs/config-reference.md): the `config.toml` schema
  and support matrix.
- [docs/source-layout.md](docs/source-layout.md): the source directory grammar.
- [MANIFESTO.md](MANIFESTO.md): what botfile is and the principles it holds to.

## Design

botfile is functional, following the Elm pattern: a pure core (validated domain
types, a total `reconcile` planner) with side effects pushed to the edges (a
filesystem port and an effect interpreter), driven by an immutable
`Model`/`Msg`/`Update` reducer. The recurring engineering patterns are catalogued
in [reviews/patterns.md](reviews/patterns.md).

## Comparison

- **GNU Stow / Tuckr**: single-source symlink farms with no notion of agents.
  botfile keeps the symlink-farm model and adds multiple sources, per-agent
  fan-out, and agent-native install paths. (Tuckr also adds hooks and secrets;
  botfile deliberately does less.)
- **chezmoi / YADM**: manage one dotfiles repo, and clone and template it for
  you. botfile manages many sources, does not clone (you use git directly), and
  does not template; it is a mechanism, not a package manager.
