//go:build acceptance

package tools_test

import (
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

// A filter is saved for a page of the web interface, listed, changed and
// deleted, and Prowlarr keeps it as it was given.
func TestFilters(t *testing.T) {
	if got := acctest.Rows(t, suite.Call(t, "filter_list", nil)["filters"], "filters"); len(got) != 0 {
		t.Fatalf("filters = %v", got)
	}
	const label = "Private Torrents"
	conditions := []map[string]any{{"key": "protocol", "type": "equal", "value": []string{"torrent"}}, {"key": "privacy", "type": "equal", "value": []string{"private"}}}

	made := suite.Call(t, "filter_create", map[string]any{"label": label, "page": "indexerIndex", "filters": conditions})
	id := acctest.Num(t, made["id"], "id")
	suite.DeleteLaterIfThere(t, "filter_delete", map[string]any{"filter": itoa(id), "confirm": true}, "no filter")
	if acctest.Str(made["label"]) != label || acctest.Str(made["page"]) != "indexerIndex" || len(acctest.Rows(t, made["filters"], "filters")) != 2 {
		t.Errorf("created = %v", made)
	}

	// the same label on another page: the two are told apart by page and id
	other := suite.Call(t, "filter_create", map[string]any{"label": label, "page": "history", "filters": []map[string]any{{"key": "successful", "type": "equal", "value": false}}})
	otherID := acctest.Num(t, other["id"], "id")
	suite.DeleteLaterIfThere(t, "filter_delete", map[string]any{"filter": itoa(otherID), "confirm": true}, "no filter")
	if all := suite.Call(t, "filter_list", nil); acctest.Num(t, all["total"], "total") != 2 {
		t.Errorf("filter_list = %v", all)
	}
	if one := acctest.Rows(t, suite.Call(t, "filter_list", map[string]any{"page": "history"})["filters"], "filters"); len(one) != 1 || acctest.Num(t, one[0]["id"], "id") != otherID {
		t.Errorf("filter_list page=history = %v", one)
	}
	if msg := suite.CallErr(t, "filter_edit", map[string]any{"filter": label, "label": "x"}); !strings.Contains(msg, "name it by id") {
		t.Errorf("editing a label two filters share = %q", msg)
	}

	// renamed, and its conditions replaced
	edited := suite.Call(t, "filter_edit", map[string]any{"filter": itoa(id), "label": "Usenet", "filters": []map[string]any{{"key": "protocol", "type": "equal", "value": []string{"usenet"}}}})
	if acctest.Str(edited["label"]) != "Usenet" || acctest.Str(edited["page"]) != "indexerIndex" || len(acctest.Rows(t, edited["filters"], "filters")) != 1 {
		t.Errorf("edited = %v", edited)
	}
	if msg := suite.CallErr(t, "filter_create", map[string]any{"label": "x", "page": "indexers", "filters": conditions}); !strings.Contains(msg, "indexerIndex") {
		t.Errorf("a page there is not = %q", msg)
	}

	// deleted only once it is confirmed
	if would := suite.Call(t, "filter_delete", map[string]any{"filter": "Usenet"}); acctest.Str(would["would_delete"]) != "Usenet" || acctest.Num(t, suite.Call(t, "filter_list", nil)["total"], "total") != 2 {
		t.Errorf("without confirm = %v", would)
	}
	for _, ref := range []string{"Usenet", itoa(otherID)} {
		if gone := suite.Call(t, "filter_delete", map[string]any{"filter": ref, "confirm": true}); acctest.Str(gone["deleted"]) == "" {
			t.Errorf("deleting %s = %v", ref, gone)
		}
	}
	if left := suite.Call(t, "filter_list", nil); acctest.Num(t, left["total"], "total") != 0 {
		t.Errorf("after deleting both = %v", left)
	}
}
