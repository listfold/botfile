package main

import (
	"bytes"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/runtime"
)

func TestParseDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		argv []string
		mode runtime.Mode
	}{
		{[]string{"plan"}, runtime.ModePlan},
		{[]string{"sync"}, runtime.ModeSync},
		{[]string{"status"}, runtime.ModeStatus},
		{[]string{"adopt", "/p/bark", "--into", "personal/mine"}, runtime.ModeAdopt},
	} {
		inv, err := parse(tc.argv)
		if err != nil {
			t.Fatalf("parse(%v): %v", tc.argv, err)
		}
		if inv.Cmd.Mode != tc.mode {
			t.Errorf("parse(%v) mode = %v, want %v", tc.argv, inv.Cmd.Mode, tc.mode)
		}
	}

	// adopt's positionals and flags land in the invocation.
	inv, _ := parse([]string{"adopt", "/p/bark", "--into", "personal/mine"})
	if inv.Args["path"] != "/p/bark" || inv.Flags["into"] != "personal/mine" {
		t.Errorf("adopt invocation = args %v flags %v", inv.Args, inv.Flags)
	}
}

func TestParseFlagForms(t *testing.T) {
	t.Parallel()
	// --into value and --into=value are equivalent.
	a, _ := parse([]string{"adopt", "/p", "--into", "s/pl"})
	b, _ := parse([]string{"adopt", "/p", "--into=s/pl"})
	if a.Flags["into"] != "s/pl" || b.Flags["into"] != "s/pl" {
		t.Fatalf("flag forms = %q, %q", a.Flags["into"], b.Flags["into"])
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()
	bad := map[string][]string{
		"unknown command":       {"bogus"},
		"extra arg on no-arg":   {"plan", "extra"},
		"flag not on this cmd":  {"plan", "--into", "x"}, // --into is adopt-only
		"missing required arg":  {"adopt", "--into", "s/pl"},
		"missing required flag": {"adopt", "/p"},
		"flag without value":    {"adopt", "/p", "--into"},
		"short flag":            {"adopt", "/p", "-x"},
		"unknown long flag":     {"adopt", "/p", "--bogus", "--into", "s/pl"},
		"extra positional":      {"adopt", "/p", "/q", "--into", "s/pl"},
	}
	for name, argv := range bad {
		if _, err := parse(argv); err == nil {
			t.Errorf("%s: parse(%v) = nil error, want error", name, argv)
		}
	}
}

func TestFormatGlobalFlag(t *testing.T) {
	t.Parallel()
	// The contract's core invariant for the global --format flag: parse accepts
	// exactly what usage advertises, applies the default, and rejects bad values.
	inv, err := parse([]string{"plan", "--format", "json"})
	if err != nil || inv.Flags["format"] != "json" {
		t.Fatalf("parse --format json = %v, %v", inv.Flags, err)
	}
	if inv, _ := parse([]string{"status"}); inv.Flags["format"] != "text" {
		t.Errorf("default format not applied: %v", inv.Flags)
	}
	if _, err := parse([]string{"plan", "--format", "yaml"}); err == nil {
		t.Errorf("out-of-enum --format should error")
	}
	var buf bytes.Buffer
	usage(&buf)
	if !strings.Contains(buf.String(), "--format") {
		t.Errorf("usage omits global flag --format\n%s", buf.String())
	}
}

func TestVersionLine(t *testing.T) {
	t.Parallel()
	if version == "" {
		t.Fatal("version must not be empty")
	}
	if got, want := versionLine(), "botfile "+version; got != want {
		t.Errorf("versionLine() = %q, want %q", got, want)
	}
	var buf bytes.Buffer
	usage(&buf)
	if !strings.Contains(buf.String(), "botfile version") {
		t.Errorf("usage should mention the version command\n%s", buf.String())
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	usage(&buf)
	out := buf.String()
	for _, want := range []string{
		"botfile plan", "show what a sync would change",
		"botfile sync", "botfile status",
		"botfile adopt <path> --into <source>/<plugin>",
		"bring an agent-created component under management",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage missing %q\n%s", want, out)
		}
	}
}
