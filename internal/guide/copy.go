package guide

// User-facing text copy for the operator guide, single-sourced here so the
// wording lives in one place (reviews/patterns.md 6, mirroring
// internal/output/copy.go). guide.go builds the Guide value and the three
// renderers walk it; both the content tables and the per-format templates live
// here, so the human and machine forms cannot drift on wording.

// Tagline is botfile's one-line description, the guide's opening line.
const Tagline = "botfile manages AI-agent skills and instructions as symlinks from source repositories you control."

// minimalConfig is the smallest config.toml that does something: one source,
// one selection for one agent. Shown verbatim in every format.
const minimalConfig = `[[sources]]
name = "personal"
location = "~/botfiles"

[[selections]]
source = "personal"
agents = ["claude-code"]`

// modelTerms defines the four nouns botfile's config is built from.
var modelTerms = []Term{
	{"source", "A local directory, often a git checkout, holding curated components. botfile reads it in place; git does any fetching."},
	{"plugin", "A named bundle inside a source. Even a single-bundle source has an explicit plugin directory: <source>/<plugin>/."},
	{"component", "A typed artifact under a plugin. Kinds today: a skill (a directory with a SKILL.md) and an instruction (a .md file)."},
	{"selection", "A config rule mapping a source (and optionally one plugin or component) to one or more agents that should receive it."},
}

// workflowSteps is the safe operating order. The confirm step is deliberate:
// plan and status are read-only, but sync changes the filesystem, so an agent
// must get the user's agreement before running it.
var workflowSteps = []Step{
	{"botfile status", "See what is managed, out of sync, and adoptable. Read-only, safe to run anytime."},
	{"botfile plan", "Preview the exact symlinks a sync would create or remove. Read-only; changes nothing."},
	{"confirm with the user", "Show the plan and get the user's agreement before changing anything on disk."},
	{"botfile sync", "Apply the plan only after the user agrees: create and remove symlinks to match the config."},
	{"botfile adopt <path> --into <source>/<plugin>", "If sync reports a conflict (a real file where botfile wants a link), adopt that file into a source instead of overwriting it. botfile never clobbers."},
}

// jsonGuidance tells an agent to prefer the structured output and how to read it.
var jsonGuidance = []string{
	"Every command accepts --format json. Prefer it: parse the structured report rather than scraping text.",
	"The JSON envelope carries schemaVersion, command, phase, outcome, exitCode, plus ops, notes, issues, and summary counts.",
	"exitCode is authoritative: 0 ok, 1 blocked (a conflict or broken config refused the change), 2 a usage or effect error.",
	"plan and status never modify anything; only sync and adopt change the filesystem.",
}

// emptyCell is shown for an agent kind with no install location.
const emptyCell = "-"

// Text renderer templates. Header consts carry no trailing newline (Fprintln
// adds it); row consts carry their own, since they are written with Fprintf.
const (
	txtTitle       = "botfile: %s\n" // tagline
	txtModelHdr    = "\nMODEL"
	txtRow2        = "  %s\t%s\n" // name, value (tabwriter-aligned)
	txtConfigHdr   = "\nCONFIG  (%s)\n"
	txtConfigRow   = "  %s\n"
	txtWorkflowHdr = "\nWORKFLOW  (run in this order; sync only after the user agrees)"
	txtWorkflowRow = "  %d. %s\n     %s\n" // n, command, detail
	txtCommandsHdr = "\nCOMMANDS"
	txtAgentsHdr   = "\nAGENTS  (where each kind installs)"
	txtAgentsHead  = "  agent\tskills\tinstructions"
	txtRow3        = "  %s\t%s\t%s\n" // id, skills, instructions
	txtJSONHdr     = "\nJSON  (for agents)"
	txtJSONRow     = "  - %s\n"
)

// Markdown renderer templates. Section headers carry their own surrounding
// blank lines and are written with Fprint, so a trailing newline is correct.
const (
	mdTitle        = "# botfile\n\n%s\n"
	mdModelHdr     = "\n## Model\n\n"
	mdModelRow     = "- **%s**: %s\n"
	mdConfig       = "\n## Config\n\nPath: `%s`\n\n```toml\n%s\n```\n" // path, example
	mdWorkflowHdr  = "\n## Workflow\n\nRun in this order; only run `sync` after the user agrees.\n\n"
	mdWorkflowRow  = "%d. **%s**: %s\n"
	mdCommandsHdr  = "\n## Commands\n\n"
	mdCommandsHead = "| Command | Does |\n|---|---|\n"
	mdCommandRow   = "| `%s` | %s |\n"
	mdAgentsHdr    = "\n## Agents\n\n"
	mdAgentsHead   = "| Agent | Skills | Instructions |\n|---|---|---|\n"
	mdAgentRow     = "| `%s` | `%s` | `%s` |\n"
	mdJSONHdr      = "\n## JSON for agents\n\n"
	mdJSONRow      = "- %s\n"
)
