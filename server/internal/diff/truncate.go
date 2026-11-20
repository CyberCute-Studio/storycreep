package diff

import (
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// The bounds of a diff that the API answers with, sized for display (PRD
// FR-SYNC-02): at most limit changes, DefaultLimit unless a client asks for
// at most MaxLimit, and at most MaxBytes of their JSON.
const (
	DefaultLimit = 1_000
	MaxLimit     = 5_000
	MaxBytes     = 8 << 20
)

// Truncate returns a copy of d that holds at most limit of its changes, and
// at most MaxBytes of their JSON, as the API writes each, without escaping
// HTML (schemagen.Marshal), the first of them in the order in which the diff
// lists them: the project's variables, component types, attribute
// definitions, and components, and then each board's changes, in the order
// of the boards. Every change after the first that does not fit is left out,
// and the copy counts how many it left out of the project's and of each
// board's, and is Truncated if it left any out. It keeps every board, the
// project's and each board's own fields, and the summary, which counts every
// change of d (FR-SYNC-02 AC2). A limit below 1 is 1, and one above MaxLimit
// is MaxLimit. Of a diff that CompareSized made, it computes the word-level
// diffs of the changes that it keeps, and of the first that MaxBytes leaves
// out, which it measures, and of no other, in d itself, whose changes the
// copy shares. It fails only where a change is not JSON, which none is.
func (d *Diff) Truncate(limit int) (*Diff, error) {
	limit = min(max(limit, 1), MaxLimit)
	out := *d
	kept, used, full := 0, 0, false
	// take returns the changes of cs that fit, and how many it leaves out.
	take := func(cs []EntityChange) ([]EntityChange, int, error) {
		taken := []EntityChange{}
		for i := range cs {
			if !full && kept < limit {
				cs[i].fill()
				data, err := schemagen.Marshal(cs[i])
				if err != nil {
					return nil, 0, fmt.Errorf("diff: %w", err)
				}
				if used+len(data) <= MaxBytes {
					taken = append(taken, cs[i])
					kept++
					used += len(data)
					continue
				}
			}
			full = true
			return taken, len(cs) - i, nil
		}
		return taken, 0, nil
	}
	out.Project.Omitted = 0
	for _, l := range []*[]EntityChange{&out.Project.Variables, &out.Project.ComponentTypes, &out.Project.Attributes,
		&out.Project.Components} {
		taken, omitted, err := take(*l)
		if err != nil {
			return nil, err
		}
		*l = taken
		out.Project.Omitted += omitted
	}
	out.Boards = make([]BoardChanges, len(d.Boards))
	for i, b := range d.Boards {
		taken, omitted, err := take(b.Changes)
		if err != nil {
			return nil, err
		}
		b.Changes, b.Omitted = taken, omitted
		out.Boards[i] = b
	}
	out.Truncated = full
	return &out, nil
}
