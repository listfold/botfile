package main

import (
	"fmt"
	"io"
	"strings"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/runtime"
)

// Command is a declarative description of one CLI verb: its name, a one-line
// summary for usage, the runtime mode it drives, and its positional/flag
// contract. Expressing the surface as data, rather than hand-coded parsing in
// each handler, keeps dispatch, validation, and usage from drifting apart
// (reviews/patterns.md 6, 7): an unrecognized invocation fails fast at the edge,
// and usage is generated from the same table the parser validates against.
type Command struct {
	Name    string
	Summary string
	Mode    runtime.Mode
	Args    []Arg
	Flags   []Flag
}

// Arg is a positional argument. The parser fills declared args in order; a
// required arg that is absent is a usage error.
type Arg struct {
	Name     string
	Required bool
}

// Flag is a "--name value" option. Value is the usage placeholder (for example
// "<source>/<plugin>"). A required flag must be given; others may carry a
// Default. Enum, when set, constrains the accepted values.
type Flag struct {
	Name     string
	Value    string
	Required bool
	Default  string
	Enum     []string
}

// commands is the canonical CLI surface: the single table the parser, the
// dispatcher in run, and usage all read.
var commands = []Command{
	{Name: "plan", Summary: "show what a sync would change", Mode: runtime.ModePlan},
	{Name: "sync", Summary: "reconcile your agents to match your config", Mode: runtime.ModeSync},
	{Name: "status", Summary: "show what is managed, out of sync, and adoptable", Mode: runtime.ModeStatus},
	{Name: "adopt", Summary: "bring an agent-created component under management", Mode: runtime.ModeAdopt,
		Args:  []Arg{{Name: "path", Required: true}},
		Flags: []Flag{{Name: "into", Value: "<source>/<plugin>", Required: true}}},
}

// globalFlags apply to every command. --format selects the output renderer; it
// is validated and shown in usage by the same machinery as command flags.
var globalFlags = []Flag{
	{Name: "format", Enum: []string{"text", "json"}, Default: "text"},
}

// Invocation is a parsed, validated command line: the matched command plus its
// positional and flag values, ready for a handler to consume.
type Invocation struct {
	Cmd   *Command
	Args  map[string]string
	Flags map[string]string
}

// parse validates argv (verb first) against the command table and returns the
// matched command with its values, or a usage error. It rejects unknown verbs,
// unknown flags, missing required args/flags, extra positionals, and flag values
// outside a declared enum.
func parse(argv []string) (Invocation, error) {
	if len(argv) == 0 {
		return Invocation{}, fmt.Errorf("no command")
	}
	cmd := findCommand(argv[0])
	if cmd == nil {
		return Invocation{}, fmt.Errorf("unknown command %q", argv[0])
	}
	flagSpecs := append(append([]Flag(nil), cmd.Flags...), globalFlags...)
	inv := Invocation{Cmd: cmd, Args: map[string]string{}, Flags: map[string]string{}}

	var positional []string
	for i := 1; i < len(argv); i++ {
		tok := argv[i]
		switch {
		case strings.HasPrefix(tok, "--") && strings.Contains(tok, "="):
			name, val, _ := strings.Cut(strings.TrimPrefix(tok, "--"), "=")
			if findFlag(flagSpecs, name) == nil {
				return Invocation{}, fmt.Errorf("unknown flag %q", "--"+name)
			}
			inv.Flags[name] = val
		case strings.HasPrefix(tok, "--"):
			name := strings.TrimPrefix(tok, "--")
			if findFlag(flagSpecs, name) == nil {
				return Invocation{}, fmt.Errorf("unknown flag %q", tok)
			}
			i++
			if i >= len(argv) {
				return Invocation{}, fmt.Errorf("flag %q needs a value", tok)
			}
			inv.Flags[name] = argv[i]
		case strings.HasPrefix(tok, "-"):
			return Invocation{}, fmt.Errorf("unknown flag %q", tok)
		default:
			positional = append(positional, tok)
		}
	}

	if len(positional) > len(cmd.Args) {
		return Invocation{}, fmt.Errorf("unexpected argument %q", positional[len(cmd.Args)])
	}
	for idx, a := range cmd.Args {
		if idx < len(positional) {
			inv.Args[a.Name] = positional[idx]
		} else if a.Required {
			return Invocation{}, fmt.Errorf("missing <%s>", a.Name)
		}
	}
	for _, f := range flagSpecs {
		v, ok := inv.Flags[f.Name]
		if !ok {
			if f.Required {
				return Invocation{}, fmt.Errorf("missing required flag --%s", f.Name)
			}
			if f.Default != "" {
				inv.Flags[f.Name] = f.Default
			}
			continue
		}
		if len(f.Enum) > 0 && !contains(f.Enum, v) {
			return Invocation{}, fmt.Errorf("--%s must be one of %s, got %q", f.Name, strings.Join(f.Enum, "|"), v)
		}
	}
	return inv, nil
}

func findCommand(name string) *Command {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}

func findFlag(flags []Flag, name string) *Flag {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// adoptRequest derives the adopt request from a parsed adopt invocation,
// splitting --into into its source and plugin. Path resolution (~ expansion) is
// the caller's job, since it needs the resolved home.
func adoptRequest(inv Invocation) (adopt.Request, error) {
	into := inv.Flags["into"]
	source, plugin, ok := strings.Cut(into, "/")
	if !ok || source == "" || plugin == "" {
		return adopt.Request{}, fmt.Errorf("--into must be <source>/<plugin>, got %q", into)
	}
	return adopt.Request{Path: inv.Args["path"], SourceName: source, PluginName: plugin}, nil
}

// parseAdopt parses the adopt verb's arguments (without the leading "adopt")
// into a request. It is the adopt entrypoint the dispatcher and tests share.
func parseAdopt(args []string) (adopt.Request, error) {
	inv, err := parse(append([]string{"adopt"}, args...))
	if err != nil {
		return adopt.Request{}, err
	}
	return adoptRequest(inv)
}

// usage prints the command summary, generated from the commands and globalFlags
// tables so it cannot drift from what the parser accepts (patterns.md 6, 7):
// every flag parse honors is advertised here, including global flags.
func usage(w io.Writer) {
	fmt.Fprintln(w, "botfile manages agent skills and instructions.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "usage:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %s\n      %s\n", invocationLine(c), c.Summary)
	}
	if len(globalFlags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "global options (any command):")
		for _, f := range globalFlags {
			fmt.Fprintf(w, "  --%s %s\n", f.Name, flagPlaceholder(f))
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "also: botfile version, botfile help, botfile guide (agent-oriented; --format text|markdown|json)")
}

// invocationLine renders a command's calling form, for example
// "botfile adopt <path> --into <source>/<plugin>".
func invocationLine(c Command) string {
	parts := []string{"botfile", c.Name}
	for _, a := range c.Args {
		parts = append(parts, "<"+a.Name+">")
	}
	for _, f := range c.Flags {
		parts = append(parts, "--"+f.Name+" "+flagPlaceholder(f))
	}
	return strings.Join(parts, " ")
}

// flagPlaceholder is the value hint shown for a flag in usage: its explicit
// Value, else its enum alternatives, else its name.
func flagPlaceholder(f Flag) string {
	switch {
	case f.Value != "":
		return f.Value
	case len(f.Enum) > 0:
		return "<" + strings.Join(f.Enum, "|") + ">"
	default:
		return "<" + f.Name + ">"
	}
}
