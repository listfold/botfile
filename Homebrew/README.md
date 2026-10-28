# Homebrew Tap for botfile

This repository contains the Homebrew formula for [botfile](https://github.com/listfold/botfile) - a symlink farm for AI agent skills and instructions.

## Installation

### From the listfold tap (recommended)

```bash
brew tap listfold/botfile
brew install botfile
```

### Direct from GitHub

```bash
brew install https://raw.githubusercontent.com/listfold/botfile/main/Homebrew/botfile.rb
```

## What is botfile?

Botfile manages your AI agents' custom context the way dotfiles manage the rest of your tools. You curate [skills](https://agentskills.io/home) (invocable context) and [instructions](https://agents.md/) (ambient context) in git repositories, and botfile symlinks them into each agent's native directories.

Currently supported agents:
- [opencode](https://opencode.ai/)
- [claude-code](https://code.claude.com)
- [codex-cli](https://developers.openai.com/codex/cli)
- [pi](https://pi.dev)
- [crush](https://github.com/charmbracelet/crush)
- [copilot-vscode](https://marketplace.visualstudio.com/items?itemName=GitHub.copilot)
- [copilot-cli](https://github.com/features/copilot/cli)

## Quick Start

```bash
# 1. Curate a source (a skill the agent discovers by presence)
mkdir -p ~/botfiles/mine/skills/bark
cat > ~/botfiles/mine/skills/bark/SKILL.md <<'MD'
---
name: bark
description: Always reply "woof".
---
MD

# 2. Tell botfile about the source and which agents get it
mkdir -p ~/.config/botfile
cat > ~/.config/botfile/config.toml <<'TOML'
[[sources]]
name = "personal"
location = "~/botfiles"

[[selections]]
source = "personal"
agents = ["claude-code"]
TOML

# 3. Preview, then apply
botfile plan
botfile sync
```

Run `botfile help` for the full operator guide.

## License

The code is licensed under the Apache License, Version 2.0 ([LICENSE](https://github.com/listfold/botfile/blob/main/LICENSE)).