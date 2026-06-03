package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"codeberg.org/botfile/botfile/internal/core"
)

// AddSelection appends a selection to the config file at path as a new
// [[selections]] table, leaving the rest of the file untouched: the smallest
// structured entry, no marker-delimited regions (manifesto 27). It validates
// that the file parses both before and after the edit, and returns an undo that
// restores the prior contents, for the adopt saga.
//
// Appending is safe because selection order does not affect precedence (that is
// source order), so a new [[selections]] table at the end is semantically
// equivalent wherever it sits.
func AddSelection(path string, sel core.Selection) (undo func() error, err error) {
	if err := sel.Validate(); err != nil {
		return nil, fmt.Errorf("invalid selection: %w", err)
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if _, err := Parse(orig); err != nil {
		return nil, fmt.Errorf("config %s does not parse: %w", path, err)
	}

	updated := make([]byte, 0, len(orig)+64)
	updated = append(updated, orig...)
	updated = append(updated, []byte(selectionTOML(sel))...)
	if _, err := Parse(updated); err != nil {
		return nil, fmt.Errorf("appending the selection produced an invalid config: %w", err)
	}

	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return nil, fmt.Errorf("write config %s: %w", path, err)
	}
	return func() error { return os.WriteFile(path, orig, 0o644) }, nil
}

// selectionTOML renders a selection as a [[selections]] table, with a leading
// blank line so it appends cleanly after existing content. The field values are
// validated names, so quoting them as basic strings is sufficient.
func selectionTOML(sel core.Selection) string {
	var b strings.Builder
	b.WriteString("\n[[selections]]\n")
	fmt.Fprintf(&b, "source = %s\n", strconv.Quote(sel.SourceName))
	fmt.Fprintf(&b, "plugin = %s\n", strconv.Quote(sel.PluginName))
	fmt.Fprintf(&b, "component = %s\n", strconv.Quote(sel.ComponentID))
	agents := make([]string, len(sel.Agents))
	for i, a := range sel.Agents {
		agents[i] = strconv.Quote(string(a))
	}
	fmt.Fprintf(&b, "agents = [%s]\n", strings.Join(agents, ", "))
	return b.String()
}
