package output

// User-facing text copy, single-sourced so the wording lives in one place
// (reviews/patterns.md 6). RenderText and the package tests reference these
// templates rather than repeating literals, so they cannot drift. The format
// verbs and spacing are load-bearing: they reproduce botfile's established CLI
// output exactly.
const (
	// Filesystem operations.
	lineOpRemove = "  remove   %s\n"   // target
	lineOp       = "  %-8s %s -> %s\n" // kind, target, dest

	// Non-blocking notes.
	lineNotice   = "  note     skills for %v also reach %v via %s\n" // selected, alsoReaches, namespace
	lineShadowed = "  shadowed %s: %s overridden by %s\n"            // target, source, wonBy
	lineSkipped  = "  skipped  %s on %s (%s)\n"                      // component, agent, detail

	// Blocking issues.
	lineIssue = "  ! %-18s %s: %s\n" // kind, ref, detail

	// Plan / sync summary lines.
	sumFailed      = "failed during %s: %v\n"                                                           // stage, message
	sumBlocked     = "\nblocked: resolve the %d issue(s) above, then run `botfile sync`\n"              // issues
	sumSynced      = "\nsynced: %d operation(s) applied\n"                                              // ops
	sumPlanBlocked = "\nplan: %d operation(s), but %d issue(s) would block sync (resolve them first)\n" // ops, issues
	sumPlan        = "\nplan: %d operation(s) (run `botfile sync` to apply)\n"                          // ops
	sumIncomplete  = "incomplete run (phase %d)\n"                                                      // phase

	// Status overview.
	statusManagedHeader   = "managed (%d)\n"
	statusItem            = "  %s\n"
	statusOutOfSyncHeader = "out of sync (%d)\n"
	statusNotesHeader     = "notes (%d)\n"
	statusAdoptableHeader = "adoptable (%d)\n"
	statusAdoptableItem   = "  %-22s %-16s %s\n" // agents, ref, path
	statusSummary         = "\n%d managed, %d out of sync, %d skipped, %d adoptable\n"

	// Adopt.
	adoptCannot     = "cannot adopt: %s\n"                           // problem detail
	adoptMove       = "  move   %s -> %s\n"                          // from, to
	adoptLink       = "  link   %s -> %s\n"                          // from, to
	adoptSelect     = "  select %s for %s\n"                         // componentID, agents
	adoptDone       = "\nadopted %s/%s into source %q (plugin %q)\n" // kind, name, source, plugin
	adoptIncomplete = "incomplete adopt (phase %d)\n"                // phase
)
