package tools

import (
	"strings"
	"testing"
)

// One release goes as Prowlarr's own search page sends one; several from the
// same indexer go in its one bulk grab, and each is named from the history
// where it records a grab.
func TestReleaseGrabOneOrSeveral(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Site", "private")})
	f.answerOK("POST /api/v1/search", map[string]any{"guid": "g1", "indexerId": 1})
	f.answerOK("POST /api/v1/search/bulk", []any{map[string]any{"guid": "g1", "indexerId": 1}, map[string]any{"guid": "g2", "indexerId": 1}})
	f.answer("GET /api/v1/history/indexer", []any{
		map[string]any{"id": 2, "indexerId": 1, "date": "2026-10-10T00:00:02Z", "eventType": "releaseGrabbed", "data": map[string]any{"grabTitle": "Second.Show.S01E02"}},
		map[string]any{"id": 1, "indexerId": 1, "date": "2026-10-10T00:00:01Z", "eventType": "releaseGrabbed", "data": map[string]any{"grabTitle": "First.Show.S01E01"}},
	})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "release_grab", map[string]any{"indexer": "Site", "guid": "g1"})
	if grabbed := listOf(out["grabbed"]); len(grabbed) != 1 || grabbed[0] != "Second.Show.S01E02" || len(f.requested("POST /api/v1/search")) != 1 || len(f.requested("POST /api/v1/search/bulk")) != 0 {
		t.Errorf("one grab = %v, sent %d single and %d bulk", out, len(f.requested("POST /api/v1/search")), len(f.requested("POST /api/v1/search/bulk")))
	}

	out = mustCall(t, cs, "release_grab", map[string]any{"indexer": "Site", "guids": []string{"g1", "g2"}})
	grabbed := listOf(out["grabbed"])
	if len(grabbed) != 2 || grabbed[0] != "First.Show.S01E01" || grabbed[1] != "Second.Show.S01E02" || len(f.requested("POST /api/v1/search/bulk")) != 1 {
		t.Errorf("two grabs = %v, sent %d bulk", out, len(f.requested("POST /api/v1/search/bulk")))
	}
	if sent := f.sent("POST /api/v1/search/bulk"); len(sent) != 1 || !strings.Contains(sent[0], `"guid":"g1"`) || !strings.Contains(sent[0], `"guid":"g2"`) {
		t.Errorf("the bulk grab sent %v", sent)
	}

	if msg := callError(t, cs, "release_grab", map[string]any{"indexer": "Site"}); !strings.Contains(msg, "guid") {
		t.Errorf("a grab of nothing = %q", msg)
	}
}
