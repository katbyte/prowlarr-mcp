package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// privateFilter is a saved filter as Prowlarr answers one.
func privateFilter(id int, label, page string) map[string]any {
	return map[string]any{"id": id, "label": label, "type": page, "filters": []any{map[string]any{"key": "privacy", "type": "equal", "value": "private"}}}
}

// A filter is saved for a page with conditions that each have a key, a type
// and a value; one for no page, or with a condition missing a part, is
// refused before anything is sent.
func TestFilterCreateChecksWhatItSaves(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("POST /api/v1/customfilter", privateFilter(8, "Private", "indexerIndex"))
	cs := session(t, f, Options{})

	conditions := []any{map[string]any{"key": "privacy", "type": "equal", "value": "private"}}
	out := mustCall(t, cs, "filter_create", map[string]any{"label": " Private ", "page": "indexerindex", "filters": conditions})
	if out["label"] != "Private" || out["page"] != "indexerIndex" || num(t, out["id"], "id") != 8 {
		t.Errorf("filter_create = %v", out)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(f.sent("POST /api/v1/customfilter")[0]), &sent); err != nil {
		t.Fatal(err)
	}
	// the label trimmed, and the page under the name the web interface keeps it by
	if sent["label"] != "Private" || sent["type"] != "indexerIndex" {
		t.Errorf("sent %v", sent)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		says string
	}{
		"no label":                  {map[string]any{"page": "history", "filters": conditions}, "label"},
		"a page there is not":       {map[string]any{"label": "x", "page": "indexers", "filters": conditions}, "indexerIndex, indexerStats, history, releases"},
		"no conditions":             {map[string]any{"label": "x", "page": "history", "filters": []any{}}, "at least one condition"},
		"a condition with no key":   {map[string]any{"label": "x", "page": "history", "filters": []any{map[string]any{"type": "equal", "value": 1}}}, "condition 1 has no key"},
		"a condition with no value": {map[string]any{"label": "x", "page": "history", "filters": []any{map[string]any{"key": "a", "type": "equal"}}}, "condition 1 has no value"},
	} {
		if msg := callError(t, cs, "filter_create", tc.args); !strings.Contains(msg, tc.says) {
			t.Errorf("%s = %q, want it to say %q", name, msg, tc.says)
		}
	}
	if n := len(f.sent("POST /api/v1/customfilter")); n != 1 {
		t.Errorf("a refused filter was sent anyway (%d creates)", n)
	}
}

// A filter is found by its label or its id, two of one label are told apart
// by id, and an edit sends the whole filter back with only what was asked
// changed.
func TestFilterEditAndList(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/customfilter", []any{privateFilter(8, "Private", "indexerIndex"), privateFilter(9, "Private", "history"), privateFilter(10, "Failed", "history")})
	f.answer("GET /api/v1/customfilter/10", privateFilter(10, "Failed", "history"))
	f.answer("PUT /api/v1/customfilter/10", privateFilter(10, "Failures", "history"))
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "filter_list", map[string]any{"page": "history"})
	if rows := listOf(out["filters"]); num(t, out["total"], "total") != 2 || len(rows) != 2 {
		t.Errorf("filter_list page=history = %v", out)
	}

	out = mustCall(t, cs, "filter_edit", map[string]any{"filter": "failed", "label": "Failures"})
	sent := f.sent("PUT /api/v1/customfilter/10")
	if out["label"] != "Failures" || len(sent) != 1 || !strings.Contains(sent[0], `"label":"Failures"`) || !strings.Contains(sent[0], `"type":"history"`) || !strings.Contains(sent[0], `"privacy"`) {
		t.Errorf("filter_edit = %v, sent %v", out, sent)
	}

	if msg := callError(t, cs, "filter_edit", map[string]any{"filter": "Private", "label": "x"}); !strings.Contains(msg, "name it by id") {
		t.Errorf("a label two filters share = %q", msg)
	}
	if msg := callError(t, cs, "filter_edit", map[string]any{"filter": "Failed"}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("an edit of nothing = %q", msg)
	}
	if msg := callError(t, cs, "filter_edit", map[string]any{"filter": "nope", "label": "x"}); !strings.Contains(msg, "Failed (history, id 10)") {
		t.Errorf("a filter that is not there = %q", msg)
	}
}
