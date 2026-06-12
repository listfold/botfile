// Package guide builds botfile's self-describing operator guide: a single
// structured document rendered to text, markdown, or JSON. It is what makes
// botfile legible to an AI agent without any installed skill (the post-install
// half of discovery): an agent that finds botfile on PATH can run `botfile
// help` or `botfile guide --format json` and learn the model, the config, and
// the safe workflow well enough to drive botfile.
//
// The guide mirrors internal/output's "one model, several renderers" shape so
// the human and machine forms cannot disagree, and keeps its wording in copy.go
// the same way. The agent matrix half is built from agent.Default(), the same
// canonical table the projection trusts, so the per-agent locations cannot
// drift from where botfile actually installs. The command list is passed in by
// the cli, which owns the canonical command table.
package guide

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
)

// SchemaVersion is the JSON guide's contract version, bumped when the shape
// changes, so an agent can parse it stably (as the output report does).
// 2 added the scope section (user scope only, fan-out, selection depth).
// 3 added the command kind (agents gain a commands location).
const SchemaVersion = 3

// CommandDoc is one CLI verb as the guide presents it. The cli builds these
// from its canonical command table and passes them in, so the guide cannot
// drift from what the parser accepts.
type CommandDoc struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Usage   string `json:"usage"`
}

// Term is one model concept and its definition.
type Term struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// Step is one step of the safe operating workflow, in order.
type Step struct {
	Command string `json:"command"`
	Detail  string `json:"detail"`
}

// AgentDoc is one agent and where botfile installs each kind for it. Derived
// from the matrix with a "~" home, so paths read as documentation
// (~/.claude/skills/<name>/) rather than this machine's absolute home.
type AgentDoc struct {
	ID           string `json:"id"`
	Skills       string `json:"skills,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Commands     string `json:"commands,omitempty"`
}

// Guide is the whole operator guide as a value: the single source the three
// renderers walk.
type Guide struct {
	SchemaVersion int          `json:"schemaVersion"`
	Tagline       string       `json:"tagline"`
	Model         []Term       `json:"model"`
	ConfigPath    string       `json:"configPath"`
	ConfigExample string       `json:"configExample"`
	Scope         []string     `json:"scope"`
	Workflow      []Step       `json:"workflow"`
	Commands      []CommandDoc `json:"commands"`
	Agents        []AgentDoc   `json:"agents"`
	JSONGuidance  []string     `json:"jsonGuidance"`
}

// Build assembles the guide from the canonical agent matrix and the command
// docs the cli supplies. configPath is shown as the config location; pass the
// resolved path, or a conventional fallback if it could not be resolved.
//
// home and getenv resolve the agent install locations exactly as a normal run
// does (agent roots honor overrides like CLAUDE_CONFIG_DIR, CODEX_HOME, and
// COPILOT_HOME), so the self-description stays accurate when an agent home is
// relocated. The env read is injected here, at the boundary, keeping this
// testable; pass os.UserHomeDir + os.Getenv from the cli. A "~" home (the cli's
// fallback when the real home is unresolvable) yields documentation-style
// paths. The content (Tagline, model terms, workflow, json guidance, config
// example) lives in copy.go.
func Build(configPath, home string, getenv func(string) string, commands []CommandDoc) Guide {
	return Guide{
		SchemaVersion: SchemaVersion,
		Tagline:       Tagline,
		Model:         modelTerms,
		ConfigPath:    configPath,
		ConfigExample: minimalConfig,
		Scope:         scopeNotes,
		Workflow:      workflowSteps,
		Commands:      commands,
		Agents:        agentDocs(home, getenv),
		JSONGuidance:  jsonGuidance,
	}
}

// agentDocs derives each agent's install locations from the default matrix,
// resolved against the given home and env lookup so overrides are honored. The
// home prefix is abbreviated back to "~" for readability (the leaf is already a
// "<name>" placeholder), while a relocated root outside home shows in full. The
// order is core.KnownAgents, the canonical agent order.
func agentDocs(home string, getenv func(string) string) []AgentDoc {
	set := agent.Default()
	roots := set.ResolveRoots(home, getenv)
	docs := make([]AgentDoc, 0, len(core.KnownAgents))
	for _, id := range core.KnownAgents {
		ag, ok := set.Lookup(id)
		if !ok {
			continue
		}
		d := AgentDoc{ID: string(id)}
		if root, ok := roots.For(id, core.KindSkill); ok {
			if ns, ok := ag.Namespace(root, core.KindSkill); ok {
				d.Skills = abbreviateHome(ns, home) + string(filepath.Separator) + "<name>" + string(filepath.Separator)
			}
		}
		if root, ok := roots.For(id, core.KindInstruction); ok {
			// Target resolves the leaf: a fixed singleton (AGENTS.md) ignores the
			// placeholder name; a drop-in file uses it (<name>.md).
			if t, ok := ag.Target(root, core.KindInstruction, "<name>"); ok {
				d.Instructions = abbreviateHome(t, home)
			}
		}
		if root, ok := roots.For(id, core.KindCommand); ok {
			if t, ok := ag.Target(root, core.KindCommand, "<name>"); ok {
				d.Commands = abbreviateHome(t, home)
			}
		}
		docs = append(docs, d)
	}
	return docs
}

// abbreviateHome replaces a leading home directory with "~" for display. A path
// outside home (a relocated root from an override) is returned unchanged, so its
// real location shows. When home is empty or already "~", there is nothing to
// abbreviate.
func abbreviateHome(path, home string) string {
	if home == "" || home == "~" {
		return path
	}
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + path[len(prefix):]
	}
	return path
}

// RenderJSON writes the guide as one indented JSON object plus a newline. The
// Guide json tags are the wire contract; this is a direct marshal.
func RenderJSON(w io.Writer, g Guide) error {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// RenderText writes the human-readable guide: section headers and aligned
// columns, no markdown syntax. Templates live in copy.go.
func RenderText(w io.Writer, g Guide) {
	fmt.Fprintf(w, txtTitle, g.Tagline)

	fmt.Fprintln(w, txtModelHdr)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, t := range g.Model {
		fmt.Fprintf(tw, txtRow2, t.Name, t.Definition)
	}
	tw.Flush()

	fmt.Fprintf(w, txtConfigHdr, g.ConfigPath)
	for _, line := range splitLines(g.ConfigExample) {
		fmt.Fprintf(w, txtConfigRow, line)
	}

	fmt.Fprintln(w, txtScopeHdr)
	for _, line := range g.Scope {
		fmt.Fprintf(w, txtScopeRow, line)
	}

	fmt.Fprintln(w, txtWorkflowHdr)
	for i, s := range g.Workflow {
		fmt.Fprintf(w, txtWorkflowRow, i+1, s.Command, s.Detail)
	}

	fmt.Fprintln(w, txtCommandsHdr)
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, c := range g.Commands {
		fmt.Fprintf(tw, txtRow2, c.Usage, c.Summary)
	}
	tw.Flush()

	fmt.Fprintln(w, txtAgentsHdr)
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, txtAgentsHead)
	for _, a := range g.Agents {
		fmt.Fprintf(tw, txtRow4, a.ID, cell(a.Skills), cell(a.Instructions), cell(a.Commands))
	}
	tw.Flush()

	fmt.Fprintln(w, txtJSONHdr)
	for _, line := range g.JSONGuidance {
		fmt.Fprintf(w, txtJSONRow, line)
	}
}

// RenderMarkdown writes the guide as markdown: headings, a fenced config
// example, an ordered workflow, and tables for commands and agents. Templates
// live in copy.go.
func RenderMarkdown(w io.Writer, g Guide) {
	fmt.Fprintf(w, mdTitle, g.Tagline)

	fmt.Fprint(w, mdModelHdr)
	for _, t := range g.Model {
		fmt.Fprintf(w, mdModelRow, t.Name, t.Definition)
	}

	fmt.Fprintf(w, mdConfig, g.ConfigPath, g.ConfigExample)

	fmt.Fprint(w, mdScopeHdr)
	for _, line := range g.Scope {
		fmt.Fprintf(w, mdScopeRow, line)
	}

	fmt.Fprint(w, mdWorkflowHdr)
	for i, s := range g.Workflow {
		fmt.Fprintf(w, mdWorkflowRow, i+1, s.Command, s.Detail)
	}

	fmt.Fprint(w, mdCommandsHdr)
	fmt.Fprint(w, mdCommandsHead)
	for _, c := range g.Commands {
		fmt.Fprintf(w, mdCommandRow, c.Usage, c.Summary)
	}

	fmt.Fprint(w, mdAgentsHdr)
	fmt.Fprint(w, mdAgentsHead)
	for _, a := range g.Agents {
		fmt.Fprintf(w, mdAgentRow, a.ID, cell(a.Skills), cell(a.Instructions), cell(a.Commands))
	}

	fmt.Fprint(w, mdJSONHdr)
	for _, line := range g.JSONGuidance {
		fmt.Fprintf(w, mdJSONRow, line)
	}
}

// cell renders an empty location as a single dash so columns stay readable.
func cell(s string) string {
	if s == "" {
		return emptyCell
	}
	return s
}

// splitLines splits a multi-line constant into lines for indented text output.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
