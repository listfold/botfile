package output

import (
	"fmt"
	"io"
	"strings"
)

// RenderText writes the human-readable form of a report: botfile's established
// CLI output. Classification already happened in ReportFromModel, so this is a
// boring walk over the Report using the copy templates.
func RenderText(w io.Writer, r Report) {
	if r.Failure != nil {
		fmt.Fprintf(w, sumFailed, r.Failure.Stage, r.Failure.Message)
		return
	}
	switch r.Command {
	case "status":
		renderStatusText(w, r)
	case "adopt":
		renderAdoptText(w, r)
	default: // plan, sync
		renderOpsText(w, r.Ops)
		renderNotesText(w, r.Notes)
		renderIssuesText(w, r.Issues)
		renderPlanSyncSummary(w, r)
	}
}

func renderOpsText(w io.Writer, ops []Op) {
	for _, op := range ops {
		if op.Kind == "remove" {
			fmt.Fprintf(w, lineOpRemove, op.Target)
		} else {
			fmt.Fprintf(w, lineOp, op.Kind, op.Target, op.Dest)
		}
	}
}

func renderNotesText(w io.Writer, notes []Note) {
	for _, n := range notes {
		switch n.Kind {
		case "notice":
			fmt.Fprintf(w, lineNotice, n.Selected, n.AlsoReaches, n.Namespace)
		case "shadowed":
			fmt.Fprintf(w, lineShadowed, n.Target, n.Source, n.WonBy)
		case "skipped":
			fmt.Fprintf(w, lineSkipped, n.Component, n.Agent, n.Detail)
		}
	}
}

func renderIssuesText(w io.Writer, issues []Issue) {
	for _, b := range issues {
		fmt.Fprintf(w, lineIssue, b.Kind, b.Ref, b.Detail)
	}
}

func renderPlanSyncSummary(w io.Writer, r Report) {
	switch {
	case r.Phase == "blocked":
		fmt.Fprintf(w, sumBlocked, r.Summary.Issues)
	case r.Phase == "done" && r.Command == "sync":
		fmt.Fprintf(w, sumSynced, r.Summary.Ops)
	case r.Phase == "done" && r.Summary.Issues > 0: // plan with blockers
		fmt.Fprintf(w, sumPlanBlocked, r.Summary.Ops, r.Summary.Issues)
	case r.Phase == "done": // plan, clean
		fmt.Fprintf(w, sumPlan, r.Summary.Ops)
	default: // incomplete run
		fmt.Fprintf(w, sumIncomplete, r.incompletePhase)
	}
}

func renderStatusText(w io.Writer, r Report) {
	st := r.Status
	if len(st.Managed) > 0 {
		fmt.Fprintf(w, statusManagedHeader, len(st.Managed))
		for _, t := range st.Managed {
			fmt.Fprintf(w, statusItem, t)
		}
	}
	if r.Summary.OutOfSync > 0 {
		fmt.Fprintf(w, statusOutOfSyncHeader, r.Summary.OutOfSync)
		renderOpsText(w, r.Ops)
		renderIssuesText(w, r.Issues)
	}
	if len(r.Notes) > 0 {
		fmt.Fprintf(w, statusNotesHeader, len(r.Notes))
		renderNotesText(w, r.Notes)
	}
	if len(st.Adoptable) > 0 {
		fmt.Fprintf(w, statusAdoptableHeader, len(st.Adoptable))
		for _, a := range st.Adoptable {
			fmt.Fprintf(w, statusAdoptableItem, strings.Join(a.Agents, ","), a.Ref, a.Path)
		}
	}
	fmt.Fprintf(w, statusSummary, r.Summary.Managed, r.Summary.OutOfSync, r.Summary.Skipped, r.Summary.Adoptable)
}

func renderAdoptText(w io.Writer, r Report) {
	switch r.Outcome {
	case "blocked":
		fmt.Fprintf(w, adoptCannot, r.Adopt.Problem)
	case "ok":
		ad := r.Adopt
		fmt.Fprintf(w, adoptMove, ad.Move.From, ad.Move.To)
		fmt.Fprintf(w, adoptLink, ad.Link.From, ad.Link.To)
		if ad.Select != nil {
			fmt.Fprintf(w, adoptSelect, ad.Select.ComponentID, strings.Join(ad.Select.Agents, ","))
		}
		fmt.Fprintf(w, adoptDone, ad.Kind, ad.Name, ad.Source, ad.Plugin)
	default: // incomplete adopt
		fmt.Fprintf(w, adoptIncomplete, r.incompletePhase)
	}
}
