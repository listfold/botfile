package source

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"codeberg.org/botfile/botfile/internal/core"
)

// file is a small helper for a regular file in a MapFS.
func file(data string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(data)} }

// dir is a helper for an explicit (possibly empty) directory in a MapFS.
func dir() *fstest.MapFile { return &fstest.MapFile{Mode: fs.ModeDir} }

func TestScanWellFormed(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"coding/skills/go-style/SKILL.md":     file("# go style"),
		"coding/skills/go-style/reference.md": file("resource inside the skill, not a separate component"),
		"coding/instructions/style.md":        file("an instruction"),
		"coding/commands/changelog.md":        file("a command"),
		"coding/README.md":                    file("furniture under a plugin, ignored"),
		"secrets/skills/deploy/SKILL.md":      file("# deploy"),
		".git/config":                         file("[core]"),
		"README.md":                           file("furniture at the root, ignored"),
	}
	res := Scan(fsys)
	if len(res.Problems) != 0 {
		t.Fatalf("well-formed source had problems: %+v", res.Problems)
	}
	if len(res.Plugins) != 2 {
		t.Fatalf("got %d plugins, want 2: %+v", len(res.Plugins), res.Plugins)
	}

	coding := res.Plugins[0]
	if coding.Name != "coding" {
		t.Fatalf("first plugin = %q, want coding", coding.Name)
	}
	// Sorted by kind then name: command before instruction before skill.
	want := []core.Component{
		{Kind: core.KindCommand, Name: "changelog"},
		{Kind: core.KindInstruction, Name: "style"},
		{Kind: core.KindSkill, Name: "go-style"},
	}
	if len(coding.Components) != 3 || coding.Components[0] != want[0] || coding.Components[1] != want[1] || coding.Components[2] != want[2] {
		t.Fatalf("coding components = %+v, want %+v", coding.Components, want)
	}

	secrets := res.Plugins[1]
	if secrets.Name != "secrets" || len(secrets.Components) != 1 || secrets.Components[0] != (core.Component{Kind: core.KindSkill, Name: "deploy"}) {
		t.Fatalf("secrets plugin = %+v, want one skill deploy", secrets)
	}
}

func TestScanSkillMissingManifestIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/skills/nomanifest":  dir(),
		"p/skills/ok/SKILL.md": file("ok"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "skill/ok" {
		t.Fatalf("components = %v, want [skill/ok]", got)
	}
	if !hasProblem(res, ProblemSkillMissingManifest, "p/skills/nomanifest") {
		t.Fatalf("expected skill-missing-manifest at p/skills/nomanifest, got %+v", res.Problems)
	}
}

func TestScanStraySkillFileIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/skills/loose.md": file("a flat file where a skill directory is expected"),
	}
	res := Scan(fsys)
	if len(componentNames(res)) != 0 {
		t.Fatalf("a stray skill file must not be a component, got %v", componentNames(res))
	}
	if !hasProblem(res, ProblemStraySkillFile, "p/skills/loose.md") {
		t.Fatalf("expected stray-skill-file, got %+v", res.Problems)
	}
}

func TestScanInstructionNotMarkdownIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/instructions/note.md":  file("good instruction"),
		"p/instructions/note.txt": file("wrong extension"),
		"p/instructions/sub/x.md": file("a directory under instructions is wrong too"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "instruction/note" {
		t.Fatalf("components = %v, want [instruction/note]", got)
	}
	if !hasProblem(res, ProblemInstructionNotMarkdown, "p/instructions/note.txt") {
		t.Fatalf("expected instruction-not-markdown for note.txt, got %+v", res.Problems)
	}
	if !hasProblem(res, ProblemInstructionNotMarkdown, "p/instructions/sub") {
		t.Fatalf("expected instruction-not-markdown for the sub directory, got %+v", res.Problems)
	}
}

func TestScanCommandNotMarkdownIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/commands/release.md": file("good command"),
		"p/commands/release.sh": file("wrong extension"),
		"p/commands/sub/x.md":   file("a directory under commands is wrong too"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "command/release" {
		t.Fatalf("components = %v, want [command/release]", got)
	}
	if !hasProblem(res, ProblemCommandNotMarkdown, "p/commands/release.sh") {
		t.Fatalf("expected command-not-markdown for release.sh, got %+v", res.Problems)
	}
	if !hasProblem(res, ProblemCommandNotMarkdown, "p/commands/sub") {
		t.Fatalf("expected command-not-markdown for the sub directory, got %+v", res.Problems)
	}
}

func TestScanUnknownKindDirIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/hooks/whatever":     file("hooks are not a supported kind directory"),
		"p/skills/ok/SKILL.md": file("ok"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "skill/ok" {
		t.Fatalf("components = %v, want [skill/ok]", got)
	}
	if !hasProblem(res, ProblemUnknownKindDir, "p/hooks") {
		t.Fatalf("expected unknown-kind-dir for p/hooks, got %+v", res.Problems)
	}
}

func TestScanInvalidPluginNameIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"bad plugin/skills/x/SKILL.md": file("under an invalid plugin name"),
	}
	res := Scan(fsys)
	if len(res.Plugins) != 0 {
		t.Fatalf("an invalid plugin name must not yield a plugin, got %+v", res.Plugins)
	}
	if !hasProblem(res, ProblemInvalidName, "bad plugin") {
		t.Fatalf("expected invalid-name for the plugin, got %+v", res.Problems)
	}
}

func TestScanInvalidComponentNameIsProblem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p/skills/bad name/SKILL.md": file("invalid skill dir name"),
	}
	res := Scan(fsys)
	if len(componentNames(res)) != 0 {
		t.Fatalf("an invalid component name must not yield a component, got %v", componentNames(res))
	}
	if !hasProblem(res, ProblemInvalidName, "p/skills/bad name") {
		t.Fatalf("expected invalid-name for the skill, got %+v", res.Problems)
	}
}

func TestScanSkillManifestNotRegularIsProblem(t *testing.T) {
	t.Parallel()
	// A directory named SKILL.md is not a manifest (manifesto 48).
	fsys := fstest.MapFS{
		"p/skills/foo/SKILL.md": dir(),
		"p/skills/ok/SKILL.md":  file("ok"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "skill/ok" {
		t.Fatalf("components = %v, want [skill/ok]", got)
	}
	if !hasProblem(res, ProblemSkillMissingManifest, "p/skills/foo") {
		t.Fatalf("expected skill-missing-manifest for a directory SKILL.md, got %+v", res.Problems)
	}
}

func TestScanInstructionNonRegularIsProblem(t *testing.T) {
	t.Parallel()
	// A directory named note.md is not an instruction (manifesto 48).
	fsys := fstest.MapFS{
		"p/instructions/note.md": dir(),
		"p/instructions/ok.md":   file("ok"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "instruction/ok" {
		t.Fatalf("components = %v, want [instruction/ok]", got)
	}
	if !hasProblem(res, ProblemInstructionNotMarkdown, "p/instructions/note.md") {
		t.Fatalf("expected instruction-not-markdown for a directory note.md, got %+v", res.Problems)
	}
}

func TestScanHiddenComponentEntriesAreProblems(t *testing.T) {
	t.Parallel()
	// Hidden entries under a kind directory are component candidates, so they are
	// flagged, not silently dropped (review 1dbd9e0).
	fsys := fstest.MapFS{
		"p/skills/.tool/SKILL.md":   file("hidden skill"),
		"p/instructions/.secret.md": file("hidden instruction"),
		"p/skills/real/SKILL.md":    file("a real one"),
	}
	res := Scan(fsys)
	if got := componentNames(res); len(got) != 1 || got[0] != "skill/real" {
		t.Fatalf("components = %v, want [skill/real]", got)
	}
	if !hasProblem(res, ProblemHiddenComponent, "p/skills/.tool") {
		t.Fatalf("expected hidden-component for p/skills/.tool, got %+v", res.Problems)
	}
	if !hasProblem(res, ProblemHiddenComponent, "p/instructions/.secret.md") {
		t.Fatalf("expected hidden-component for p/instructions/.secret.md, got %+v", res.Problems)
	}
}

func TestLayoutRoundTrip(t *testing.T) {
	t.Parallel()
	if d, ok := DirForKind(core.KindSkill); !ok || d != "skills" {
		t.Errorf("DirForKind(skill) = %q,%v, want skills,true", d, ok)
	}
	if d, ok := DirForKind(core.KindInstruction); !ok || d != "instructions" {
		t.Errorf("DirForKind(instruction) = %q,%v, want instructions,true", d, ok)
	}
	if d, ok := DirForKind(core.KindCommand); !ok || d != "commands" {
		t.Errorf("DirForKind(command) = %q,%v, want commands,true", d, ok)
	}
	if got := ComponentLeaf(core.Component{Kind: core.KindSkill, Name: "go-style"}); got != "go-style" {
		t.Errorf("ComponentLeaf(skill) = %q, want go-style", got)
	}
	if got := ComponentLeaf(core.Component{Kind: core.KindInstruction, Name: "style"}); got != "style.md" {
		t.Errorf("ComponentLeaf(instruction) = %q, want style.md", got)
	}
	if got := ComponentLeaf(core.Component{Kind: core.KindCommand, Name: "changelog"}); got != "changelog.md" {
		t.Errorf("ComponentLeaf(command) = %q, want changelog.md", got)
	}
}

// componentNames flattens all scanned components to "<kind>/<name>" refs.
func componentNames(res Result) []string {
	var out []string
	for _, p := range res.Plugins {
		for _, c := range p.Components {
			out = append(out, c.Ref().String())
		}
	}
	return out
}

func hasProblem(res Result, kind ProblemKind, path string) bool {
	for _, p := range res.Problems {
		if p.Kind == kind && p.Path == path {
			return true
		}
	}
	return false
}
