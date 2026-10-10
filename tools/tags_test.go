package tools

import (
	"strings"
	"testing"
)

// A tag is renamed by sending its id with the new label, lower case; a label
// another tag has is refused before anything is sent.
func TestTagEditRenames(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/tag", []any{map[string]any{"id": 1, "label": "tv"}, map[string]any{"id": 2, "label": "films"}})
	f.answer("GET /api/v1/tag/detail", []any{map[string]any{"id": 1, "label": "shows"}, map[string]any{"id": 2, "label": "films"}})
	f.answer("PUT /api/v1/tag/1", map[string]any{"id": 1, "label": "shows"})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "tag_edit", map[string]any{"tag": "TV", "label": " Shows "})
	sent := f.sent("PUT /api/v1/tag/1")
	if out["label"] != "shows" || len(sent) != 1 || !strings.Contains(sent[0], `"label":"shows"`) || !strings.Contains(sent[0], `"id":1`) {
		t.Errorf("tag_edit = %v, sent %v", out, sent)
	}

	if msg := callError(t, cs, "tag_edit", map[string]any{"tag": "tv", "label": "Films"}); !strings.Contains(msg, "already exists") {
		t.Errorf("renaming onto another tag's label = %q", msg)
	}
	if msg := callError(t, cs, "tag_edit", map[string]any{"tag": "nope", "label": "x"}); !strings.Contains(msg, "films, tv") {
		t.Errorf("a tag that is not there = %q", msg)
	}
	if msg := callError(t, cs, "tag_edit", map[string]any{"tag": "tv", "label": " "}); !strings.Contains(msg, "new label") {
		t.Errorf("an empty label = %q", msg)
	}
	if n := len(f.sent("PUT /api/v1/tag/1")); n != 1 {
		t.Errorf("a refused rename was sent anyway (%d updates)", n)
	}
}
