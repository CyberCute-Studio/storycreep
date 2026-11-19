package queries

import (
	"strings"
	"testing"
)

// TestSearchWithoutComments holds the text of SearchWithoutComments to
// Search's without its branch of the comments, as SearchWithoutComments says
// it is, so that a change of the one that the other misses, which would
// search a project by different rules as it has a comment or not, fails.
func TestSearchWithoutComments(t *testing.T) {
	const start = "    UNION ALL\n    SELECT e.kind, e.entity_id, t.board_id, t.node_id, t.target_type"
	const end = "        AND c.deleted_at IS NULL AND t.target_live\n"
	if strings.Count(search, start) != 1 || strings.Count(search, end) != 1 {
		t.Fatalf("Search holds its branch of the comments %d times by its start and %d by its end, want once",
			strings.Count(search, start), strings.Count(search, end))
	}
	i, j := strings.Index(search, start), strings.Index(search, end)+len(end)
	if j < i {
		t.Fatal("Search's branch of the comments ends before it starts")
	}
	if !strings.Contains(search[i:j], "e.kind = 'comment'") {
		t.Fatalf("Search's branch of the comments reads no comment:\n%s", search[i:j])
	}
	want := strings.Replace(search[:i]+search[j:], "-- name: Search :many", "-- name: SearchWithoutComments :many", 1)
	if searchWithoutComments != want {
		t.Errorf("SearchWithoutComments is not Search without its branch of the comments:\ngot:\n%s\nwant:\n%s",
			searchWithoutComments, want)
	}
	if strings.Contains(searchWithoutComments, "comment") {
		t.Errorf("SearchWithoutComments reads comments:\n%s", searchWithoutComments)
	}
}
