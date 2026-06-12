package main

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/guide"
)

// bootstrapSkillPath is the packaged bootstrap-botfile skill, the example
// source's hand-carried copy of the operator guide.
const bootstrapSkillPath = "../../examples/botfile_example/botfile/skills/bootstrap-botfile/SKILL.md"

// TestBootstrapSkillCarriesTheGuide pins the packaged skill to the generated
// guide: every line `botfile guide --format markdown` renders (under
// documentation-style "~" paths) must appear verbatim in the skill, so a
// matrix, command-table, or copy change cannot silently leave the packaged
// copy stale. The skill is a superset (frontmatter, an intro paragraph),
// which is why the invariant is line-subset rather than equality.
func TestBootstrapSkillCarriesTheGuide(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(bootstrapSkillPath)
	if err != nil {
		t.Fatalf("read packaged skill: %v", err)
	}
	skillLines := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		skillLines[strings.TrimRight(line, " \t")] = true
	}

	// "~" home and the conventional config path yield the documentation-style
	// paths the skill carries; no env overrides, matching a fresh machine.
	g := guide.Build("~/.config/botfile/config.toml", "~", func(string) string { return "" }, commandDocs())
	var buf bytes.Buffer
	guide.RenderMarkdown(&buf, g)

	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if line == "" {
			continue
		}
		if !skillLines[line] {
			t.Errorf("packaged skill is stale, missing guide line:\n  %s\nupdate %s to match `botfile guide --format markdown`", line, bootstrapSkillPath)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan rendered guide: %v", err)
	}
}
