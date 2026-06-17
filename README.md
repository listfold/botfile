# botfile
 Botfile manages your AI agents' custom context the way dotfiles manage the rest of your tools. You curate [skills](https://agentskills.io/home) (invocable context) and [instructions](https://agents.md/) (ambient context) in git repositories, and botfile symlinks them into each agent's native directories.

Currently [claude-code](https://code.claude.com), [codex-cli](https://developers.openai.com/codex/cli), [pi](https://pi.dev), [crush](https://github.com/charmbracelet/crush), copilot-vscode, [copilot-cli](https://github.com/features/copilot/cli) are all supported.

## Intro
Botfile is a magic-free symlink farm, in the spirit of [GNU Stow](https://www.gnu.org/software/stow/manual/stow.html), but it understands AI agents (where `agent = harness + model`): it knows where each one reads skills and instructions, and fans one source out to all the agents on your device.

The fan-out is botfile's unique feature. It means that one source reaches multiple destinations. In this case, one agent skill, instruction or command can be made available all the agents on the user's device. If the skill is in a git repo, it can be shared across teams. If it's updated, botfile can sync the updated skill so everyone stays on the same page.

Botfile's goal is to bring some structure to the sometimes chaotic internals of popular agents by enabling users to extract, persist, share, and sync the skills https://agentskills.io/home, instructions https://agents.md/, and commands they use.

Botfile solves the problem of managing standard user-invokable context, like the skills and commands that a team shares, alongside private skills you want to use across your devices and agents.

Bofile also works in the same way with ambient context. The instructions, like AGENTS.md that are injected into the harness lifecycle.

Botfile works with all the major agent harnesses, and is quite opinionated about following the UNIX philosophy of doing one thing well. In botfile's case, it'll only ever be a symlink farm with support for the agent component kinds (skills, commands, and instructions so far) that fit its model.

If you:
- don't have skills or instructions at the user-scope, or

- don't find yourself wanting to share skills or instructions across agents, or

- don't share skills or instructions with a team.

Botfile is probably not for you...

If you'd like to try and start doing one, all or just some of these things it's a good place to start. 
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

Run `botfile help` for the full operator guide.

## Commands

- `botfile plan`: show what a sync would change (read-only).
- `botfile sync`: reconcile your agents to match your config.
- `botfile status`: what is managed, out of sync, and adoptable.
- `botfile adopt <path> --into <source>/<plugin>`: bring an agent-created
  component under management.

Any command takes `--format json` for machine-readable output.

## Supported agents

claude-code, codex-cli, copilot-cli, copilot-vscode, crush, opencode, and pi.dev,
each for **skills** and **instructions**. Where each installs (and the shared
`~/.agents/skills` pool) is shown by `botfile help`.

## Docs

`botfile help` (alias `botfile guide`) is the built-in operator guide; it accepts
`--format text|markdown|json` and loads no config, so it works on a fresh install.

## License

The code is licensed under the Apache License, Version 2.0
([`LICENSE`](LICENSE), [`NOTICE`](NOTICE)). Copyright 2026 Iain Maitland.
