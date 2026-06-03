package config

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
)

func TestAddSelectionAppendsAndUndoes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	orig := "" +
		"[[sources]]\n" +
		"name = \"personal\"\n" +
		"location = \"/src/personal\"\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	sel := core.Selection{
		SourceName: "personal", PluginName: "mine", ComponentID: "skill/bark-pro",
		Agents: []core.AgentID{core.AgentClaudeCode},
	}
	undo, err := AddSelection(path, sel)
	if err != nil {
		t.Fatalf("AddSelection: %v", err)
	}

	// The file still parses and now selects the component, and the source block
	// is preserved.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg.Sources) != 1 || len(cfg.Selections) != 1 {
		t.Fatalf("config = %+v", cfg)
	}
	got := cfg.Selections[0]
	if got.SourceName != "personal" || got.PluginName != "mine" || got.ComponentID != "skill/bark-pro" {
		t.Fatalf("selection = %+v", got)
	}

	if err := undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != orig {
		t.Fatalf("undo did not restore the file:\n%s", after)
	}
}

func TestAddSelectionRejectsInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("[[sources]]\nname=\"p\"\nlocation=\"/p\"\n"), 0o644)

	// A selection referencing an undeclared source must be rejected (Parse of the
	// updated file fails the cross-check), and the file left untouched.
	bad := core.Selection{SourceName: "ghost", PluginName: "*", ComponentID: "*", Agents: []core.AgentID{core.AgentClaudeCode}}
	if _, err := AddSelection(path, bad); err == nil {
		t.Fatal("AddSelection with an undeclared source should fail")
	}
}
