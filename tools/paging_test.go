package tools

import (
	"slices"
	"testing"
)

// A list is cut to the page asked for, says how many rows there are in all,
// and names where the next page starts until there is none.
func TestPaged(t *testing.T) {
	t.Parallel()

	rows := []int{1, 2, 3, 4, 5}
	for _, tc := range []struct {
		name          string
		limit, offset int
		want          []int
		out           pageOut
	}{
		{"no limit is the default", 0, 0, []int{1, 2, 3}, pageOut{Total: 5, NextOffset: 3}},
		{"the first page", 2, 0, []int{1, 2}, pageOut{Total: 5, NextOffset: 2}},
		{"the page after it", 2, 2, []int{3, 4}, pageOut{Total: 5, Offset: 2, NextOffset: 4}},
		{"the last page names no next", 2, 4, []int{5}, pageOut{Total: 5, Offset: 4}},
		{"a page that ends at the end", 3, 2, []int{3, 4, 5}, pageOut{Total: 5, Offset: 2}},
		{"past the end is nothing", 2, 9, []int{}, pageOut{Total: 5, Offset: 5}},
		{"before the start is the start", 2, -4, []int{1, 2}, pageOut{Total: 5, NextOffset: 2}},
	} {
		got, out := paged(rows, tc.limit, tc.offset, 3)
		if !slices.Equal(got, tc.want) || out != tc.out {
			t.Errorf("%s: paged(limit %d, offset %d) = %v %+v, want %v %+v", tc.name, tc.limit, tc.offset, got, out, tc.want, tc.out)
		}
	}

	// a limit is never more than the cap, however much is asked for
	if got, out := paged(make([]int, maxLimit+5), 1_000_000, 0, 3); len(got) != maxLimit || out.NextOffset != maxLimit || out.Total != maxLimit+5 {
		t.Errorf("a limit of a million answered %d rows, next %d", len(got), out.NextOffset)
	}
	if got, out := paged([]int(nil), 0, 0, 3); len(got) != 0 || out != (pageOut{}) {
		t.Errorf("an empty list = %v %+v", got, out)
	}
}

// listTools are the tools that answer a list and are not named for it:
// *_list and the audits are lists by their names. The guard that holds every
// one of them to limit, offset, total and next_offset is in confirm_test.go.
var listTools = []string{"indexer_catalog", "indexer_stats", "release_search", "server_health", "server_logs", "server_log", "server_updates"}
