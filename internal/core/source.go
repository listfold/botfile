package core

import (
	"fmt"
	"strings"
)

// Source is a curated repository or directory of components (manifesto 8): for
// example a private personal repo of secret skills, or a public team repo of
// coding standards. botfile takes a source as already-curated state; it never
// seeds or authors its content (manifesto 29).
type Source struct {
	// Name identifies the source within a configuration. Selections reference
	// a source by this name (manifesto 39).
	Name string
	// Location is where the curated state lives: a local path or a git URL.
	// botfile does not create it (manifesto 29); it manages symlinks out of it.
	Location string
}

// Validate checks a source in isolation. Cross-source concerns (unique names)
// are checked by Config.Validate.
func (s Source) Validate() error {
	if err := validateName("source name", s.Name); err != nil {
		return err
	}
	if strings.TrimSpace(s.Location) == "" {
		return fmt.Errorf("source %q has an empty location", s.Name)
	}
	return nil
}

// Plugin is a bundle of components within a source (manifesto 9), the middle
// tier of the source > plugin > component hierarchy (manifesto 10). Like
// components, the populated set is discovered by scanning a source; selections
// reference a plugin by name (or the wildcard) without the tree being present.
type Plugin struct {
	Name       string
	Components []Component
}

// validateName enforces the shared rule for identifiers that double as
// reference keys and may appear as path segments: non-empty, no whitespace, no
// path separators, and not the wildcard token (which has dedicated meaning in
// selections, manifesto 39).
func validateName(what, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if name == Wildcard {
		return fmt.Errorf("%s must not be the wildcard %q", what, Wildcard)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%s %q must not contain a path separator", what, name)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("%s %q must not have leading or trailing whitespace", what, name)
	}
	return nil
}
