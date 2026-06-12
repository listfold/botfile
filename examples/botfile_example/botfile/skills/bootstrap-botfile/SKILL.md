---
name: bootstrap-botfile
description: Drive the botfile CLI to manage AI-agent skills, instructions, and commands. Use when asked to sync, plan, adopt, or check the status of agent components managed by botfile.
---

# botfile

botfile manages AI-agent skills, instructions, and commands as symlinks from source repositories you control.

This skill carries botfile's operator guide. The CLI prints the same guide: run `botfile guide` (or `botfile guide --format markdown|json`); it is also published at [botfile.org/agents.html](https://botfile.org/agents.html). If `botfile` is not on PATH, install it first (with the user's agreement) per [botfile.org](https://botfile.org/#install).

## Model

- **source**: A local directory, often a git checkout, holding curated components. botfile reads it in place; git does any fetching.
- **plugin**: A named bundle inside a source. Even a single-bundle source has an explicit plugin directory: `<source>/<plugin>/`.
- **component**: A typed artifact under a plugin. Kinds today: a skill (a directory with a SKILL.md), an instruction (a .md file), and a command (a .md file the agent exposes as a slash command).
- **selection**: A config rule mapping a source (and optionally one plugin or component) to one or more agents that should receive it.

## Config

Path: `~/.config/botfile/config.toml`

```toml
[[sources]]
name = "personal"
location = "~/botfiles/personal"

[[selections]]
source = "personal"
agents = ["claude-code"]
```

## Scope

- botfile operates at user scope only: the per-user paths under your home directory. It never writes into a project checkout (a repo's .claude/ or an in-repo AGENTS.md); project-scoped components belong to the project.
- Selections fan out: one component reaches every agent its selections name, one symlink per agent's native path, and agents reading the shared ~/.agents/skills pool are served by a single link. Symlinks, not copies, so an edit to the source is live through every agent at once.
- The kinds differ in invocation, which drives scoping care: an instruction is ambient (the harness injects it into every session), so it matters that instructions can be scoped to all, some, or one agent; a skill is model-invoked when relevant and a command is user-invoked (a slash command), so both cost nothing until used and scoping them tightly is rarely critical. Not every agent supports commands; the agents table shows a dash where a kind has no native surface.
- A selection picks any depth of source > plugin > component: omit plugin and component for the whole source, set plugin for one bundle, set both for a single component (component is `<kind>/<name>`, like `skill/review`).
- An omitted plugin or component is a wildcard; an unknown config key is rejected rather than ignored, so a typo cannot silently widen a selection.

## Workflow

Run in this order; only run `sync` after the user agrees.

1. **botfile status**: See what is managed, out of sync, and adoptable. Read-only, safe to run anytime.
2. **botfile plan**: Preview the exact symlinks a sync would create or remove. Read-only; changes nothing.
3. **confirm with the user**: Show the plan and get the user's agreement before changing anything on disk.
4. **botfile sync**: Apply the plan only after the user agrees: create and remove symlinks to match the config.
5. **botfile adopt <path> --into <source>/<plugin>**: If sync reports a conflict (a real file where botfile wants a link), adopt that file into a source instead of overwriting it. botfile never clobbers.

## Commands

| Command | Does |
|---|---|
| `botfile plan` | show what a sync would change |
| `botfile sync` | reconcile your agents to match your config |
| `botfile status` | show what is managed, out of sync, and adoptable |
| `botfile adopt <path> --into <source>/<plugin>` | bring an agent-created component under management |
| `botfile guide` | print this guide (text, markdown, or json) |
| `botfile version` | print the version |

## Agents

| Agent | Skills | Instructions | Commands |
|---|---|---|---|
| `claude-code` | `~/.claude/skills/<name>/` | `~/.claude/rules/<name>.md` | `~/.claude/commands/<name>.md` |
| `codex-cli` | `~/.agents/skills/<name>/` | `~/.codex/AGENTS.md` | `~/.codex/prompts/<name>.md` |
| `copilot-cli` | `~/.agents/skills/<name>/` | `~/.copilot/copilot-instructions.md` | - |
| `copilot-vscode` | `~/.agents/skills/<name>/` | `~/.copilot/instructions/<name>.instructions.md` | - |
| `crush` | `~/.agents/skills/<name>/` | `~/.config/crush/CRUSH.md` | - |
| `opencode` | `~/.agents/skills/<name>/` | `~/.config/opencode/AGENTS.md` | `~/.config/opencode/commands/<name>.md` |
| `pi.dev` | `~/.agents/skills/<name>/` | `~/.pi/agent/AGENTS.md` | `~/.pi/agent/prompts/<name>.md` |

## JSON for agents

- Every command accepts --format json. Prefer it: parse the structured report rather than scraping text.
- The JSON envelope carries schemaVersion, command, phase, outcome, exitCode, plus ops, notes, issues, and summary counts.
- exitCode is authoritative: 0 ok, 1 blocked (a conflict or broken config refused the change), 2 a usage or effect error.
- plan and status never modify anything; only sync and adopt change the filesystem.
