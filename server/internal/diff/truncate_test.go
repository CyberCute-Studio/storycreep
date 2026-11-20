package diff

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// changesOf returns the changes that d lists, in its order.
func changesOf(d *Diff) []EntityChange {
	var out []EntityChange
	out = append(out, d.Project.Variables...)
	out = append(out, d.Project.ComponentTypes...)
	out = append(out, d.Project.Attributes...)
	out = append(out, d.Project.Components...)
	for _, b := range d.Boards {
		out = append(out, b.Changes...)
	}
	return out
}

// omittedOf returns how many changes d says that it left out.
func omittedOf(d *Diff) int {
	n := d.Project.Omitted
	for _, b := range d.Boards {
		n += b.Omitted
	}
	return n
}

// TestTruncate checks that Truncate keeps the first changes of a diff, in
// its order, as many as its limit asks for, and counts those that it leaves
// out, of the project and of each board, while every board, every board's
// and the project's own fields, and the summary, which counts every change,
// stay as they were, and that a limit that holds every change truncates
// nothing.
//
// Verifies: FR-SYNC-02/AC2
func TestTruncate(t *testing.T) {
	d, _ := compareCase(t, caseNamed(t, "added"))
	all := changesOf(d)
	if len(all) != 5 {
		t.Fatalf("the case added has %d changes, want 5", len(all))
	}
	for limit := 0; limit <= 6; limit++ {
		got, err := d.Truncate(limit)
		if err != nil {
			t.Fatal(err)
		}
		keep := min(max(limit, 1), len(all))
		if kept := changesOf(got); !reflect.DeepEqual(kept, all[:keep]) {
			t.Errorf("limit %d keeps %+v, want the first %d", limit, kept, keep)
		}
		if omitted := omittedOf(got); omitted != len(all)-keep || got.Truncated != (keep < len(all)) {
			t.Errorf("limit %d leaves %d out, truncated %v, want %d", limit, omitted, got.Truncated, len(all)-keep)
		}
		if got.Summary != d.Summary || len(got.Boards) != len(d.Boards) ||
			!reflect.DeepEqual(got.Project.Fields, d.Project.Fields) {
			t.Errorf("limit %d changes the summary, the boards, or the project's fields", limit)
		}
	}
	// The project's two changes come first, then the board's three.
	got, err := d.Truncate(3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.Omitted != 0 || got.Boards[0].Omitted != 2 || len(got.Boards[0].Changes) != 1 {
		t.Errorf("limit 3 leaves %d of the project's and %d of the board's out, want 0 and 2",
			got.Project.Omitted, got.Boards[0].Omitted)
	}
	if len(d.Boards[0].Changes) != 3 || d.Truncated {
		t.Error("Truncate changed the diff that it copied")
	}
}

// TestTruncateBytes checks that Truncate keeps no more than MaxBytes of the
// changes' JSON, leaving out every change from the first that would pass it,
// whatever the limit.
//
// Verifies: FR-SYNC-02/AC2
func TestTruncateBytes(t *testing.T) {
	big := strings.Repeat("w", MaxBytes/3)
	d := &Diff{Boards: []BoardChanges{{Board: BoardRef{ID: b1}}}, Project: ProjectChanges{}}
	for range 4 {
		d.Boards[0].Changes = append(d.Boards[0].Changes, EntityChange{Change: Modified, Kind: KindNode, ID: n1,
			Fields: []FieldChange{{Field: "title", Before: big, After: "x"}}})
		d.Summary.count(KindNode, Modified, false)
	}
	got, err := d.Truncate(MaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	size := 0
	for _, c := range got.Boards[0].Changes {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		size += len(data)
	}
	if len(got.Boards[0].Changes) != 2 || got.Boards[0].Omitted != 2 || !got.Truncated || size > MaxBytes {
		t.Errorf("Truncate keeps %d changes of %d bytes, leaves %d out, want 2 within %d, and 2 left out",
			len(got.Boards[0].Changes), size, got.Boards[0].Omitted, MaxBytes)
	}
	if got.Summary.Modified != 4 {
		t.Errorf("the truncated diff's summary counts %d modified, want 4", got.Summary.Modified)
	}
}

// TestWordsRebuildTexts checks, of random texts of a few words, white space,
// and punctuation, that the equal and delete runs of Words make the before
// text and the equal and insert runs the after text, that no run is empty,
// and that no two runs in a row have one operation, but a delete followed by
// an insert.
func TestWordsRebuildTexts(t *testing.T) {
	r := rand.New(rand.NewPCG(80, 4))
	pieces := []string{"a", "b", "the", "tide", " ", "  ", "\n", ",", "\u00a0", "é", "{luck}"}
	text := func() string {
		var b strings.Builder
		for range r.IntN(12) {
			b.WriteString(pieces[r.IntN(len(pieces))])
		}
		return b.String()
	}
	for range 2000 {
		before, after := text(), text()
		runs := Words(before, after)
		var gotBefore, gotAfter strings.Builder
		for i, run := range runs {
			if run.Text == "" {
				t.Fatalf("%q into %q: an empty run in %+v", before, after, runs)
			}
			if i > 0 && runs[i-1].Op == run.Op {
				t.Fatalf("%q into %q: two %s runs in a row in %+v", before, after, run.Op, runs)
			}
			if i > 0 && runs[i-1].Op == Insert && run.Op == Delete {
				t.Fatalf("%q into %q: an insert before a delete in %+v", before, after, runs)
			}
			if run.Op != Insert {
				gotBefore.WriteString(run.Text)
			}
			if run.Op != Delete {
				gotAfter.WriteString(run.Text)
			}
		}
		if gotBefore.String() != before || gotAfter.String() != after {
			t.Fatalf("%q into %q: the runs %+v make %q into %q", before, after, runs, gotBefore.String(),
				gotAfter.String())
		}
	}
}
