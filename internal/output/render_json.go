package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// RenderJSON writes the report as one indented JSON object followed by a
// newline. The Report is the wire contract: its json tags are authoritative, so
// this is a direct marshal with no per-field logic. The same Report drives
// RenderText, so the human and machine forms can never disagree on the outcome.
func RenderJSON(w io.Writer, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}
