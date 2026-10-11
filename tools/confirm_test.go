package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// thing is a provider as Prowlarr lists one: enough for a tool to find it by
// name and say what goes with it.
func thing(id int, name, kind string) map[string]any {
	return map[string]any{"id": id, "name": name, "implementation": kind, "fields": []any{}, "tags": []any{}}
}

// Every tool that deletes changes nothing without confirm, and says what it
// would take with it: nothing is sent until confirm is given, and then the
// one delete is.
func TestADeleteChangesNothingWithoutConfirm(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		tool, arg, name string
		list, remove    string
		rows            []any
	}{
		{"indexer_delete", "indexer", "Gone Soon", "GET /api/v1/indexer", "DELETE /api/v1/indexer/4", []any{torznab(4, "Gone Soon", "private")}},
		{"app_delete", "app", "Sonarr", "GET /api/v1/applications", "DELETE /api/v1/applications/7", []any{app(7, "Sonarr", "Sonarr", "http://sonarr:8989", "http://prowlarr:9696", "fullSync", []int{5000}, nil)}},
		{"profile_delete", "profile", "Spare", "GET /api/v1/appprofile", "DELETE /api/v1/appprofile/2", []any{map[string]any{"id": 2, "name": "Spare", "enableRss": true, "minimumSeeders": 1}}},
		{"proxy_delete", "proxy", "Flare", "GET /api/v1/indexerproxy", "DELETE /api/v1/indexerproxy/3", []any{thing(3, "Flare", "FlareSolverr")}},
		{"downloadclient_delete", "client", "Blackhole", "GET /api/v1/downloadclient", "DELETE /api/v1/downloadclient/5", []any{thing(5, "Blackhole", "TorrentBlackhole")}},
		{"notification_delete", "notification", "Discord", "GET /api/v1/notification", "DELETE /api/v1/notification/6", []any{map[string]any{"id": 6, "name": "Discord", "implementation": "Discord", "onGrab": true, "fields": []any{}, "tags": []any{}}}},
		{"filter_delete", "filter", "Private", "GET /api/v1/customfilter", "DELETE /api/v1/customfilter/8", []any{map[string]any{"id": 8, "label": "Private", "type": "indexerIndex", "filters": []any{map[string]any{"key": "privacy", "type": "equal", "value": "private"}}}}},
		{"backup_delete", "backup", "prowlarr_backup.zip", "GET /api/v1/system/backup", "DELETE /api/v1/system/backup/9", []any{map[string]any{"id": 9, "name": "prowlarr_backup.zip", "type": "manual", "time": "2026-10-01T00:00:00Z", "size": 4096}}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			f := newFakeServer(t)
			f.answer(tc.list, tc.rows)
			f.answerOK(tc.remove, map[string]any{})
			cs := session(t, f, Options{EnableDelete: true})

			out := mustCall(t, cs, tc.tool, map[string]any{tc.arg: tc.name})
			if out["would_delete"] != tc.name || out["deleted"] != nil {
				t.Errorf("without confirm = %v, want it to say %q would go and nothing has", out, tc.name)
			}
			if loses := listOf(out["loses"]); len(loses) == 0 {
				t.Errorf("without confirm it does not say what goes with %q: %v", tc.name, out)
			}
			if sent := f.requested(tc.remove); len(sent) != 0 {
				t.Fatalf("a delete was sent without confirm: %v", sent)
			}

			out = mustCall(t, cs, tc.tool, map[string]any{tc.arg: tc.name, "confirm": true})
			if out["deleted"] != tc.name || out["would_delete"] != nil {
				t.Errorf("with confirm = %v, want %q deleted", out, tc.name)
			}
			if sent := f.requested(tc.remove); len(sent) != 1 {
				t.Errorf("with confirm %d deletes were sent, want the one", len(sent))
			}
		})
	}
}

// Every list takes limit and offset and answers a total and next_offset, and
// every tool that deletes takes confirm and answers what it would delete: a
// tool added without either is caught here by its name or its kind. The lists
// are counted, so one cannot go missing from the check either.
func TestEveryListPagesAndEveryDeleteConfirms(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	res, err := session(t, f, Options{EnableDelete: true}).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := queuedTools(t, queuedFor(newTestClient(t), Options{EnableDelete: true}))
	properties := func(schema any) map[string]any {
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}

		return s.Properties
	}

	lists, deletes := 0, 0
	for _, tool := range res.Tools {
		in, out := properties(tool.InputSchema), properties(tool.OutputSchema)

		audit := strings.HasPrefix(tool.Name, "audit_") && tool.Name != "audit_all"
		if audit || strings.HasSuffix(tool.Name, "_list") || slices.Contains(listTools, tool.Name) {
			lists++
			for _, arg := range []string{"limit", "offset"} {
				if in[arg] == nil {
					t.Errorf("%s answers a list and takes no %s", tool.Name, arg)
				}
			}
			total := "total"
			if audit {
				total = "total_findings"
			}
			for _, field := range []string{total, "offset", "next_offset"} {
				if out[field] == nil {
					t.Errorf("%s answers a list and no %s", tool.Name, field)
				}
			}
		}

		if kinds[tool.Name].Kind == deleteTool.String() {
			deletes++
			if in["confirm"] == nil {
				t.Errorf("%s deletes and takes no confirm", tool.Name)
			}
			for _, field := range []string{"would_delete", "deleted", "loses"} {
				if out[field] == nil {
					t.Errorf("%s deletes and answers no %s", tool.Name, field)
				}
			}
		}
	}
	if lists != 38 || deletes != 8 {
		t.Errorf("%d lists and %d deletes were checked, want 38 and 8: a tool was added or renamed, so count it here", lists, deletes)
	}
	for _, name := range listTools {
		if _, ok := kinds[name]; !ok {
			t.Errorf("listTools names %s, which is no tool", name)
		}
	}
}

// A list answers the page asked for and where it sits in the whole, through
// a tool as a client calls it.
func TestAListAnswersAPageAndWhereItSits(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	labels := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	tags := make([]any, 0, len(labels))
	for i, label := range labels {
		tags = append(tags, map[string]any{"id": i + 1, "label": label})
	}
	f.answer("GET /api/v1/tag/detail", tags)
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "tag_list", map[string]any{"limit": 2, "offset": 2})
	rows := listOf(out["tags"])
	if num(t, out["total"], "total") != 5 || num(t, out["offset"], "offset") != 2 || num(t, out["next_offset"], "next_offset") != 4 || len(rows) != 2 {
		t.Fatalf("limit 2 from 2 of five tags = %v", out)
	}
	if first := objectOf(rows[0]); first["label"] != "charlie" {
		t.Errorf("the page starts at %v, want charlie", first["label"])
	}

	out = mustCall(t, cs, "tag_list", map[string]any{"offset": 4})
	if rows := listOf(out["tags"]); len(rows) != 1 || out["next_offset"] != nil {
		t.Errorf("the last page = %v, want one tag and no next_offset", out)
	}
	if out = mustCall(t, cs, "tag_list", nil); num(t, out["total"], "total") != 5 || out["next_offset"] != nil {
		t.Errorf("with no limit = %v, want all five and no next_offset", out)
	}
}

// Several indexers named at once are deleted in Prowlarr's one bulk delete,
// after confirm says what every one of them would lose.
func TestSeveralIndexersAreDeletedAtOnce(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(4, "Gone Soon", "private"), torznab(5, "Gone Too", "public")})
	f.answerOK("DELETE /api/v1/indexer/bulk", map[string]any{})
	cs := session(t, f, Options{EnableDelete: true})

	out := mustCall(t, cs, "indexer_delete", map[string]any{"indexers": []string{"Gone Soon", "Gone Too"}})
	if out["would_delete"] != "Gone Soon, Gone Too" || out["deleted"] != nil || len(f.requested("DELETE /api/v1/indexer/bulk")) != 0 {
		t.Errorf("without confirm = %v", out)
	}
	out = mustCall(t, cs, "indexer_delete", map[string]any{"indexers": []string{"Gone Soon", "5"}, "confirm": true})
	if out["deleted"] != "Gone Soon, Gone Too" || len(f.requested("DELETE /api/v1/indexer/4")) != 0 {
		t.Errorf("with confirm = %v", out)
	}
	if sent := f.sent("DELETE /api/v1/indexer/bulk"); len(sent) != 1 || !strings.Contains(sent[0], `"ids":[4,5]`) {
		t.Errorf("the bulk delete sent %v", sent)
	}
	if msg := callError(t, cs, "indexer_delete", map[string]any{"confirm": true}); !strings.Contains(msg, "name the indexer") {
		t.Errorf("a delete of nothing = %q", msg)
	}
}
