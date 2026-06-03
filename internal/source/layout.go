// Package source scans a curated source directory tree into the plugins and
// components it contains, enforcing botfile's source grammar (manifesto 46-48):
//
//	<source>/<plugin>/<kind>/<component>
//
// The first level under the source root is always a plugin, the second is
// always a kind directory (plural: skills/, instructions/), and the third is the
// component: a skill is a directory containing SKILL.md, an instruction is a
// <name>.md file. The scanner is the I/O port that produces the desired
// component set; it reads through an io/fs.FS so it is testable without the
// real disk, and reports malformed entries as typed Problems rather than
// dropping them silently (reviews/patterns.md, explicit outcome algebra).
package source

import (
	"strings"

	"codeberg.org/botfile/botfile/internal/core"
)

// ManifestFile is the file a skill directory must contain to be a skill
// (agentskills.io, manifesto 17, 48).
const ManifestFile = "SKILL.md"

// instructionExt is the extension of an instruction file (manifesto 18, 48).
const instructionExt = ".md"

// kindForDir maps each plural on-disk kind directory (manifesto 47) to its
// singular Kind token. It is the single source for the mapping; DirForKind is
// its inverse. Adding a kind means adding one entry here.
var kindForDir = map[string]core.Kind{
	"skills":       core.KindSkill,
	"instructions": core.KindInstruction,
}

// dirForKind is the inverse of kindForDir, built once at init so both
// directions stay consistent from a single declaration.
var dirForKind = func() map[core.Kind]string {
	m := make(map[core.Kind]string, len(kindForDir))
	for dir, kind := range kindForDir {
		m[kind] = dir
	}
	return m
}()

// DirForKind returns the plural on-disk directory name for a kind (manifesto
// 47): KindSkill -> "skills", KindInstruction -> "instructions". The projection
// layer uses it (with ComponentLeaf) to reconstruct a component's path under a
// source.
func DirForKind(k core.Kind) (string, bool) {
	dir, ok := dirForKind[k]
	return dir, ok
}

// ComponentLeaf returns the on-disk leaf name of a component within its kind
// directory (manifesto 48): "<name>" for a skill (a directory) and "<name>.md"
// for an instruction (a file). It is the inverse of how the scanner derives a
// component name from a directory entry, so a scanned component round-trips back
// to its path.
func ComponentLeaf(c core.Component) string {
	if c.Kind == core.KindInstruction {
		return c.Name + instructionExt
	}
	return c.Name
}

// InstructionName returns the component name for an instruction file entry (the
// filename without the .md extension) and whether the entry is a .md file at all.
func InstructionName(entry string) (string, bool) {
	if !strings.HasSuffix(entry, instructionExt) {
		return "", false
	}
	return strings.TrimSuffix(entry, instructionExt), true
}
