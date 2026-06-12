package core

import "testing"

func TestParseComponentID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		id       string
		wantRef  ComponentRef
		wantWild bool
		wantErr  bool
	}{
		{name: "wildcard", id: "*", wantWild: true},
		{name: "skill", id: "skill/go-style", wantRef: ComponentRef{Kind: KindSkill, Name: "go-style"}},
		{name: "instruction", id: "instruction/coding", wantRef: ComponentRef{Kind: KindInstruction, Name: "coding"}},
		{name: "command", id: "command/open-pr", wantRef: ComponentRef{Kind: KindCommand, Name: "open-pr"}},
		{name: "no slash", id: "skill", wantErr: true},
		{name: "unknown kind", id: "hook/pre", wantErr: true},
		{name: "empty name", id: "skill/", wantErr: true},
		{name: "nested name", id: "skill/a/b", wantErr: true},
		{name: "backslash name", id: `skill/a\b`, wantErr: true},
		{name: "leading space name", id: "skill/ name", wantErr: true},
		{name: "trailing space name", id: "skill/name ", wantErr: true},
		{name: "wildcard name", id: "skill/*", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref, wild, err := ParseComponentID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseComponentID(%q): want error, got ref=%v wild=%v", tt.id, ref, wild)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseComponentID(%q): unexpected error: %v", tt.id, err)
			}
			if wild != tt.wantWild {
				t.Errorf("ParseComponentID(%q) wildcard = %v, want %v", tt.id, wild, tt.wantWild)
			}
			if !tt.wantWild && ref != tt.wantRef {
				t.Errorf("ParseComponentID(%q) ref = %v, want %v", tt.id, ref, tt.wantRef)
			}
		})
	}
}

func TestComponentRefString(t *testing.T) {
	t.Parallel()
	got := ComponentRef{Kind: KindSkill, Name: "go-style"}.String()
	if want := "skill/go-style"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestSelectionValidate(t *testing.T) {
	t.Parallel()
	base := Selection{
		SourceName:  "team",
		PluginName:  Wildcard,
		ComponentID: Wildcard,
		Agents:      []AgentID{AgentClaudeCode},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}

	tests := []struct {
		name string
		mut  func(s *Selection)
	}{
		{"empty source", func(s *Selection) { s.SourceName = "" }},
		{"wildcard source", func(s *Selection) { s.SourceName = Wildcard }},
		{"bad plugin", func(s *Selection) { s.PluginName = "a/b" }},
		{"bad component", func(s *Selection) { s.ComponentID = "nope" }},
		{"no agents", func(s *Selection) { s.Agents = nil }},
		{"unknown agent", func(s *Selection) { s.Agents = []AgentID{"acme-bot"} }},
		{"duplicate agent", func(s *Selection) { s.Agents = []AgentID{AgentClaudeCode, AgentClaudeCode} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := base
			s.Agents = append([]AgentID(nil), base.Agents...)
			tt.mut(&s)
			if err := s.Validate(); err == nil {
				t.Errorf("%s: want error, got nil", tt.name)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := Config{
		Sources: []Source{{Name: "team", Location: "/srv/team"}},
		Selections: []Selection{{
			SourceName: "team", PluginName: Wildcard, ComponentID: Wildcard,
			Agents: []AgentID{AgentClaudeCode, AgentOpenCode},
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	dup := Config{Sources: []Source{
		{Name: "team", Location: "/a"},
		{Name: "team", Location: "/b"},
	}}
	if err := dup.Validate(); err == nil {
		t.Error("duplicate source name: want error, got nil")
	}

	dangling := Config{
		Sources:    []Source{{Name: "team", Location: "/a"}},
		Selections: []Selection{{SourceName: "personal", PluginName: Wildcard, ComponentID: Wildcard, Agents: []AgentID{AgentClaudeCode}}},
	}
	if err := dangling.Validate(); err == nil {
		t.Error("selection referencing undeclared source: want error, got nil")
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	valid := []string{"team", "go-style", "coding_standards", "a.b", "v2"}
	for _, n := range valid {
		if err := ValidateName("name", n); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", n, err)
		}
	}
	invalid := []string{"", "*", "bad name", "tab\tname", " leading", "trailing ", "a/b", `a\b`}
	for _, n := range invalid {
		if err := ValidateName("name", n); err == nil {
			t.Errorf("ValidateName(%q): want error, got nil", n)
		}
	}
}

func TestIsKnownAgent(t *testing.T) {
	t.Parallel()
	if !IsKnownAgent(AgentPiDev) {
		t.Error("pi.dev should be a known agent")
	}
	if IsKnownAgent("acme-bot") {
		t.Error("acme-bot should not be a known agent")
	}
}
