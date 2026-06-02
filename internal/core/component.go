package core

import (
	"fmt"
	"strings"
)

// Kind is the kind of a component (manifesto 7). botfile supports a kind only
// where the agent treats it in a way manageable by symlinks (manifesto 19);
// the set grows as agents expose conformant directories (manifesto 24). Today
// the rigorously specified, broadly supported kinds are skills (manifesto 17)
// and memory (manifesto 18).
type Kind string

const (
	KindSkill  Kind = "skill"
	KindMemory Kind = "memory"
)

// knownKinds is the canonical set of component kinds botfile models today. It
// grows by adding a constant here as an agent exposes a conformant directory
// for a new kind (manifesto 19, 24).
var knownKinds = []Kind{KindSkill, KindMemory}

// IsKnownKind reports whether k names a component kind botfile models.
func IsKnownKind(k Kind) bool {
	for _, known := range knownKinds {
		if known == k {
			return true
		}
	}
	return false
}

// Wildcard is the token that matches every plugin or component in a selection
// (manifesto 39: "*" for all plugins / all components).
const Wildcard = "*"

// ComponentRef names a single component within its source by kind and name. It
// is the parsed form of a Selection.ComponentID (manifesto 39), where the
// on-the-wire spelling is "<kind>/<name>".
type ComponentRef struct {
	Kind Kind
	Name string
}

// String renders the ref in its canonical "<kind>/<name>" form.
func (r ComponentRef) String() string {
	return string(r.Kind) + "/" + r.Name
}

// ParseComponentID parses a Selection.ComponentID (manifesto 39). The wildcard
// "*" yields (zero ComponentRef, true); a concrete "<kind>/<name>" yields the
// parsed ref and false. A malformed or unknown-kind id is an error.
func ParseComponentID(id string) (ref ComponentRef, isWildcard bool, err error) {
	if id == Wildcard {
		return ComponentRef{}, true, nil
	}
	kindPart, namePart, ok := strings.Cut(id, "/")
	if !ok {
		return ComponentRef{}, false, fmt.Errorf("component id %q must be %q or \"<kind>/<name>\"", id, Wildcard)
	}
	kind := Kind(kindPart)
	if !IsKnownKind(kind) {
		return ComponentRef{}, false, fmt.Errorf("component id %q has unknown kind %q", id, kindPart)
	}
	if namePart == "" {
		return ComponentRef{}, false, fmt.Errorf("component id %q is missing a name after %q", id, kindPart+"/")
	}
	if strings.ContainsAny(namePart, "/") {
		return ComponentRef{}, false, fmt.Errorf("component id %q name %q must not contain %q", id, namePart, "/")
	}
	return ComponentRef{Kind: kind, Name: namePart}, false, nil
}

// Component is a single context or config artifact within a source (manifesto
// 7). The tree of plugins and components is discovered by scanning a source on
// disk (a later concern); this type is the validated unit those scans yield and
// that selections target.
type Component struct {
	Kind Kind
	Name string
}

// Ref returns the component's reference form.
func (c Component) Ref() ComponentRef {
	return ComponentRef{Kind: c.Kind, Name: c.Name}
}

// Validate checks that the component names a known kind and carries a name.
func (c Component) Validate() error {
	if !IsKnownKind(c.Kind) {
		return fmt.Errorf("component kind %q is not known", c.Kind)
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("component of kind %q has an empty name", c.Kind)
	}
	return nil
}
