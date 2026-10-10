//go:build integration

// Journeys: the tools chained the way a real session chains them, and held to
// what they say about themselves. The per-tool tests prove each tool answers;
// these prove that a tool which says it only reads leaves Prowlarr as it
// found it, that a write which says a repeat changes nothing more is right
// about that, that calls made at once do not undo each other, that a list
// read a page at a time is the list read whole, and that a delete does
// nothing until it is confirmed. A 200 from Prowlarr is not proof of any of
// it, so each is read back from Prowlarr itself.
package acceptance

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	acc "github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// state is everything Prowlarr is set up with, as its own API reports it,
// part by part: what a tool that only reads must leave as it was. What
// Prowlarr records about its own work - history, statistics, an indexer's
// failures, its logs, its backups - is not in it: a search is a read, and
// leaves a line in the history.
func state(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}
	keep := func(part string, v any, err error) {
		t.Helper()

		if err != nil {
			t.Fatalf("reading %s: %v", part, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[part] = string(raw)
	}

	indexers, err := sdk.GetIndexer(suite.Ctx)
	keep("indexers", indexers.Model, err)
	apps, err := sdk.GetApplications(suite.Ctx)
	keep("applications", apps.Model, err)
	profiles, err := sdk.GetAppProfile(suite.Ctx)
	keep("sync profiles", profiles.Model, err)
	proxies, err := sdk.GetIndexerProxy(suite.Ctx)
	keep("proxies", proxies.Model, err)
	clients, err := sdk.GetDownloadClient(suite.Ctx)
	keep("download clients", clients.Model, err)
	notifications, err := sdk.GetNotification(suite.Ctx)
	keep("notifications", notifications.Model, err)
	tags, err := sdk.GetTag(suite.Ctx)
	keep("tags", tags.Model, err)
	filters, err := sdk.GetCustomFilter(suite.Ctx)
	keep("saved filters", filters.Model, err)
	host, err := sdk.GetConfigHost(suite.Ctx)
	keep("host settings", host.Model, err)
	ui, err := sdk.GetConfigUi(suite.Ctx)
	keep("ui settings", ui.Model, err)
	development, err := sdk.GetConfigDevelopment(suite.Ctx)
	keep("development settings", development.Model, err)

	return out
}

// changed names the parts of Prowlarr's state that differ between two
// readings of it.
func changed(before, after map[string]string) []string {
	var out []string
	for _, part := range slices.Sorted(maps.Keys(before)) {
		if before[part] != after[part] {
			out = append(out, part)
		}
	}

	return out
}

// served is the tools the suite's server registered, as a client lists them.
func served(t *testing.T) []*mcp.Tool {
	t.Helper()

	if !suite.Ready {
		t.Skip(testenv.NotConfigured)
	}
	res, err := suite.Session.ListTools(suite.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	return res.Tools
}

// --- reads change nothing ---------------------------------------------------

// Every tool that says it only reads, run with what a session would ask of
// it - the tests that make Prowlarr reach out, the searches, every audit -
// and Prowlarr's state read before and after is the same. This is what makes
// --read-only worth trusting: a tool marked read-only that wrote would show
// here. A read tool that is added fails the test until it is given a call.
func TestJourneyReadsChangeNothing(t *testing.T) {
	calls := map[string]map[string]any{
		"server_info": nil, "server_health": {"check": true}, "server_logs": nil, "server_log": nil, "server_updates": nil, "server_settings": nil,
		"task_list": nil, "command_list": nil, "backup_list": nil, "filter_list": nil, "category_list": nil, "tag_list": nil,
		"indexer_list": nil, "indexer_get": {"indexer": idxTorrent}, "indexer_test": {"indexer": idxTorrent}, "indexer_stats": nil, "indexer_catalog": {"search": "1337"},
		"app_list": nil, "app_get": {"app": appSonarr}, "app_test": nil,
		"profile_list": nil, "proxy_list": nil, "proxy_test": nil, "downloadclient_list": nil, "downloadclient_test": nil,
		"notification_list": nil, "notification_test": nil,
		"history_list": nil, "release_search": {"query": "severance", "indexers": []string{idxTorrent}},
		"audit_all": nil,
	}
	for _, tool := range served(t) {
		if strings.HasPrefix(tool.Name, "audit_") {
			calls[tool.Name] = nil
		}
	}

	before := state(t)
	for _, tool := range served(t) {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			continue
		}
		args, ok := calls[tool.Name]
		if !ok {
			t.Errorf("%s says it only reads and this journey does not call it: give it a call here", tool.Name)
			continue
		}
		suite.Call(t, tool.Name, args)
		delete(calls, tool.Name)
	}
	for name := range calls {
		t.Errorf("this journey calls %s as a read, and it is not one the server marks read-only", name)
	}
	if parts := changed(before, state(t)); len(parts) > 0 {
		t.Errorf("the read tools changed Prowlarr's %s", strings.Join(parts, ", "))
	}
}

// --- writes done twice ------------------------------------------------------

// Every write that tells a client a repeat changes nothing more is repeated,
// and Prowlarr's state after the second call is its state after the first, or
// the second call is refused. A tool that claims it and is not proved here
// fails the test.
func TestJourneyWritesDoneTwice(t *testing.T) {
	h := newHook(t)
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": "Twice Hook", "confirm": true}, "no notification")
	suite.Call(t, "notification_add", map[string]any{"kind": "Webhook", "name": "Twice Hook", "on": []string{"health_issue"}, "settings": map[string]any{"url": h.url}})
	filter := acc.Num(t, suite.Call(t, "filter_create", map[string]any{"label": "Twice", "page": "history", "filters": []map[string]any{{"key": "successful", "type": "equal", "value": false}}})["id"], "id")
	suite.DeleteLaterIfThere(t, "filter_delete", map[string]any{"filter": itoa(filter), "confirm": true}, "no filter")
	rotate := 0
	for _, sec := range acc.Rows(t, suite.Call(t, "server_settings", map[string]any{"section": "development"})["sections"], "sections") {
		rotate = acc.Num(t, acc.Object(t, sec["settings"], "settings")["logRotate"], "logRotate")
	}

	// each is a call, and the call that puts back what it changed
	type write struct {
		tool    string
		args    map[string]any
		undo    string
		undoing map[string]any
	}
	writes := []write{
		{"indexer_edit", map[string]any{"indexer": idxMusic, "priority": 31, "force": true}, "indexer_edit", map[string]any{"indexer": idxMusic, "priority": 25, "force": true}},
		{"indexer_bulk_edit", map[string]any{"indexers": []string{idxMusic, idxAnime}, "add_tags": []string{"twice-bulk"}}, "indexer_bulk_edit", map[string]any{"indexers": []string{idxMusic, idxAnime}, "remove_tags": []string{"twice-bulk"}}},
		{"app_edit", map[string]any{"app": appSonarr, "sync_level": "addOnly"}, "app_edit", map[string]any{"app": appSonarr, "sync_level": "full"}},
		{"app_sync", nil, "", nil},
		{"profile_edit", map[string]any{"profile": profileSpare, "minimum_seeders": 5}, "profile_edit", map[string]any{"profile": profileSpare, "minimum_seeders": 1}},
		{"proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 3129}, "force": true}, "proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 3128}, "force": true}},
		{"downloadclient_edit", map[string]any{"client": clientSpare, "priority": 3}, "downloadclient_edit", map[string]any{"client": clientSpare, "priority": 2}},
		{"tag_create", map[string]any{"label": "twice-tag"}, "", nil},
		{"tag_edit", map[string]any{"tag": "twice-tag", "label": "twice-renamed"}, "", nil},
		{"tag_delete", map[string]any{"label": "twice-renamed"}, "", nil},
		{"server_settings_edit", map[string]any{"section": "development", "settings": map[string]any{"logRotate": rotate + 1}}, "server_settings_edit", map[string]any{"section": "development", "settings": map[string]any{"logRotate": rotate}}},
		{"notification_edit", map[string]any{"notification": "Twice Hook", "health_warnings": true, "force": true}, "", nil},
		{"filter_edit", map[string]any{"filter": itoa(filter), "label": "Twice Over"}, "", nil},
	}
	// what says it and is proved elsewhere, and why not here
	elsewhere := map[string]string{
		"command_cancel": "a command waits in the queue for a moment Prowlarr chooses: TestCommands catches one, and the second cancel of it is refused as finished",
	}

	proved := map[string]bool{}
	for _, w := range writes {
		proved[w.tool] = true
		if w.undo != "" {
			suite.PutBack(t, w.undo, w.undoing)
		}
		suite.Call(t, w.tool, w.args)
		once := state(t)
		held := appsUp.Indexers("sonarr")
		// the second call changes nothing, whether it answers or is refused
		_, err := suite.Invoke(w.tool, w.args)
		if parts := changed(once, state(t)); len(parts) > 0 {
			t.Errorf("%s a second time changed Prowlarr's %s (it answered %v)", w.tool, strings.Join(parts, ", "), err)
		}
		if w.tool == "app_sync" && !slices.EqualFunc(held, appsUp.Indexers("sonarr"), func(a, b servarr.Indexer) bool { return a.Name == b.Name }) {
			t.Errorf("a second sync changed what Sonarr holds: %d indexers, then %d", len(held), len(appsUp.Indexers("sonarr")))
		}
	}
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "twice-bulk"})

	for _, tool := range served(t) {
		if tool.Annotations == nil || !tool.Annotations.IdempotentHint || proved[tool.Name] {
			continue
		}
		if elsewhere[tool.Name] == "" {
			t.Errorf("%s tells a client a repeat changes nothing more, and nothing here proves it: repeat it in this journey", tool.Name)
		}
	}
	for _, w := range writes {
		if !slices.ContainsFunc(served(t), func(tool *mcp.Tool) bool {
			return tool.Name == w.tool && tool.Annotations != nil && tool.Annotations.IdempotentHint
		}) {
			t.Errorf("this journey repeats %s, which does not tell a client a repeat changes nothing more", w.tool)
		}
	}
}

// --- edits made at once -----------------------------------------------------

// A client's calls in one turn run at once, and an edit sends the whole
// record back: eight edits of one indexer, each adding a tag of its own and
// one they share, leave all nine on it, and the shared tag is made once.
func TestJourneyEditsAtOnceAllHold(t *testing.T) {
	if !suite.Ready {
		t.Skip(testenv.NotConfigured)
	}
	const edits = 8
	want := []string{"at-once"}
	for n := range edits {
		want = append(want, fmt.Sprintf("at-once-%d", n))
	}
	t.Cleanup(func() {
		_, _ = suite.Invoke("indexer_edit", map[string]any{"indexer": idxMusic, "remove_tags": want, "force": true})
		for _, label := range want {
			_, _ = suite.Invoke("tag_delete", map[string]any{"label": label})
		}
	})

	var wg sync.WaitGroup
	failures := make([]error, edits)
	for n := range edits {
		wg.Go(func() {
			_, failures[n] = suite.Invoke("indexer_edit", map[string]any{"indexer": idxMusic, "add_tags": []string{"at-once", fmt.Sprintf("at-once-%d", n)}, "force": true})
		})
	}
	wg.Wait()
	for n, err := range failures {
		if err != nil {
			t.Errorf("edit %d: %v", n, err)
		}
	}

	held := acc.Strs(t, indexerRow(t, idxMusic)["tags"], "tags")
	for _, label := range want {
		if !slices.Contains(held, label) {
			t.Errorf("%s holds %v after %d edits at once, and not %s", idxMusic, held, edits, label)
		}
	}
	shared := 0
	for _, tag := range acc.Rows(t, suite.Call(t, "tag_list", nil)["tags"], "tags") {
		if acc.Str(tag["label"]) == "at-once" {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("the tag every edit shared was made %d times", shared)
	}
}

// --- lists a page at a time -------------------------------------------------

// listTool is one list tool: the arguments it is asked with, the field its
// rows are in, and whether what it lists can change while it is being read.
type listTool struct {
	args map[string]any
	rows string
	// grows says why its total only rises between two calls
	grows string
	// moves says why its rows come and go between two calls, so that only
	// what each page says of itself can be held
	moves string
}

// Every list read a page at a time, following next_offset until there is
// none, is the list read whole: as many rows as its total says, the same
// rows, in the same order. A list that is added fails the test until it is
// walked here.
func TestJourneyEveryListAddsUpToItsTotal(t *testing.T) {
	st := suite.Call(t, "server_info", nil)
	home.Offer(arrserver.Update{Version: "99.0.0.3"}, arrserver.Update{Version: "99.0.0.2"}, arrserver.Update{Version: acc.Str(st["version"])})
	t.Cleanup(func() { home.Offer() })

	lists := map[string]listTool{
		"indexer_list": {rows: "indexers"}, "app_list": {rows: "apps"}, "profile_list": {rows: "profiles"}, "proxy_list": {rows: "proxies"},
		"downloadclient_list": {rows: "download_clients"}, "notification_list": {rows: "notifications"}, "tag_list": {rows: "tags"},
		"category_list": {rows: "categories"}, "task_list": {rows: "tasks"}, "backup_list": {rows: "backups"}, "filter_list": {rows: "filters"},
		"indexer_catalog": {rows: "entries"}, "indexer_stats": {rows: "indexers"}, "server_health": {rows: "checks"}, "server_logs": {rows: "files"},
		"server_updates": {rows: "updates"},
		"release_search": {rows: "releases", args: map[string]any{"indexers": []string{idxTorrent}}},
		"history_list":   {rows: "events", grows: "every search and test the suite makes is one more event"},
		"server_log":     {rows: "entries", grows: "Prowlarr logs as it works"},
		"command_list":   {rows: "commands", grows: "a scheduled task queues one when it is due"},
		"audit_failing":  {rows: "findings", moves: "Prowlarr's hold on a failing indexer lapses and returns"},
	}
	for _, tool := range served(t) {
		if _, ok := lists[tool.Name]; !ok && strings.HasPrefix(tool.Name, "audit_") && tool.Name != "audit_all" {
			lists[tool.Name] = listTool{rows: "findings"}
		}
	}

	for _, tool := range served(t) {
		l, ok := lists[tool.Name]
		if !ok {
			if strings.HasSuffix(tool.Name, "_list") {
				t.Errorf("%s is a list and this journey does not walk it", tool.Name)
			}
			continue
		}
		delete(lists, tool.Name)
		total := "total"
		if l.rows == "findings" {
			total = "total_findings"
		}
		with := func(extra map[string]any) map[string]any {
			args := maps.Clone(l.args)
			if args == nil {
				args = map[string]any{}
			}
			maps.Copy(args, extra)

			return args
		}

		whole := suite.Call(t, tool.Name, with(map[string]any{"limit": 1000}))
		all, count := append([]any{}, acc.RowsOfAny(whole[l.rows])...), acc.Num(t, whole[total], total)
		if len(all) != count || whole["next_offset"] != nil {
			t.Errorf("%s read whole: %d rows, a total of %d, next_offset %v", tool.Name, len(all), count, whole["next_offset"])
		}

		// in pages small enough that there are several, when there is that much
		size := max(1, count/4)
		walked := []any{}
		offset, pages := 0, 0
		for {
			page := suite.Call(t, tool.Name, with(map[string]any{"limit": size, "offset": offset}))
			rows := acc.RowsOfAny(page[l.rows])
			if len(rows) > size || acc.Num(t, page["offset"], "offset") != offset {
				t.Errorf("%s limit %d offset %d answered %d rows from offset %v", tool.Name, size, offset, len(rows), page["offset"])
			}
			walked = append(walked, rows...)
			next, more := page["next_offset"].(float64)
			if pages++; !more || pages > 2000 {
				break
			}
			if int(next) != offset+len(rows) {
				t.Errorf("%s: next_offset %v after %d rows from %d", tool.Name, next, len(rows), offset)
			}
			offset = int(next)
		}

		switch {
		case l.moves != "":
			// each page was held to its limit, its offset and its next_offset above
		case l.grows != "":
			if len(walked) < count {
				t.Errorf("%s a page at a time is %d rows, fewer than the %d it had when read whole", tool.Name, len(walked), count)
			}
		default:
			a, _ := json.Marshal(all)
			b, _ := json.Marshal(walked)
			if len(walked) != count || string(a) != string(b) {
				t.Errorf("%s a page at a time (%d rows in %d pages of %d) is not the list read whole (%d rows)", tool.Name, len(walked), pages, size, count)
			}
		}
	}
	for name := range lists {
		t.Errorf("this journey walks %s, which the server does not serve", name)
	}
}

// --- deletes ask first ------------------------------------------------------

// One of everything that can be deleted is made, and each delete without
// confirm says what it would take and takes nothing: Prowlarr's state is as
// it was. With confirm each goes.
func TestJourneyDeletesAskFirst(t *testing.T) {
	if !suite.Ready {
		t.Skip(testenv.NotConfigured)
	}
	if err := appsUp.Add(servarr.App{Name: "asked", Kind: servarr.Readarr, APIKey: "askedkey"}); err != nil {
		t.Fatal(err)
	}
	const name = "Ask First"
	suite.Call(t, "indexer_add", map[string]any{"definition": "Generic Torznab", "name": name, "base_url": sitesUp.Indexer(siteAnime).URL(), "settings": map[string]any{"apiKey": siteKeys[siteAnime]}})
	suite.Call(t, "app_add", map[string]any{"kind": "Readarr", "name": name, "base_url": appsUp.URL("asked"), "api_key": "askedkey"})
	suite.Call(t, "profile_create", map[string]any{"name": name})
	suite.Call(t, "proxy_add", map[string]any{"kind": "Http", "name": name, "host": "proxy.invalid", "port": 3128, "tags": []string{"ask-first"}, "force": true})
	suite.Call(t, "downloadclient_add", map[string]any{"kind": "TorrentBlackhole", "name": name, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}})
	suite.Call(t, "notification_add", map[string]any{"kind": "Webhook", "name": name, "on": []string{"health_issue"}, "settings": map[string]any{"url": "http://" + testenv.Host() + ":1/hook"}, "force": true})
	suite.Call(t, "filter_create", map[string]any{"label": name, "page": "indexerIndex", "filters": []map[string]any{{"key": "protocol", "type": "equal", "value": []string{"torrent"}}}})
	backup := acc.Str(acc.Object(t, suite.Call(t, "backup_create", nil)["backup"], "backup")["name"])
	t.Cleanup(func() { _, _ = suite.Invoke("tag_delete", map[string]any{"label": "ask-first"}) })

	deletes := map[string]map[string]any{
		"indexer_delete": {"indexer": name}, "app_delete": {"app": name}, "profile_delete": {"profile": name}, "proxy_delete": {"proxy": name},
		"downloadclient_delete": {"client": name}, "notification_delete": {"notification": name}, "filter_delete": {"filter": name}, "backup_delete": {"backup": backup},
	}
	backups := func() int { return acc.Num(t, suite.Call(t, "backup_list", nil)["total"], "total") }
	for tool, args := range deletes {
		confirmed := maps.Clone(args)
		confirmed["confirm"] = true
		suite.DeleteLaterIfThere(t, tool, confirmed, "no ")
	}

	before, had := state(t), backups()
	for _, tool := range served(t) {
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || !strings.HasSuffix(tool.Name, "_delete") || tool.Name == "tag_delete" {
			continue
		}
		args, ok := deletes[tool.Name]
		if !ok {
			t.Errorf("%s deletes and this journey does not ask it first: make one of what it deletes here", tool.Name)
			continue
		}
		would := suite.Call(t, tool.Name, args)
		want := name
		if tool.Name == "backup_delete" {
			want = backup
		}
		if acc.Str(would["would_delete"]) != want || would["deleted"] != nil || len(acc.Strs(t, would["loses"], "loses")) == 0 {
			t.Errorf("%s without confirm = %v, want it to say %s would go, and what with it", tool.Name, would, want)
		}
	}
	if parts := changed(before, state(t)); len(parts) > 0 || backups() != had {
		t.Fatalf("the deletes changed Prowlarr's %s without confirm (backups %d, then %d)", strings.Join(parts, ", "), had, backups())
	}

	for tool, args := range deletes {
		args["confirm"] = true
		if gone := suite.Call(t, tool, args); acc.Str(gone["deleted"]) == "" || gone["would_delete"] != nil {
			t.Errorf("%s with confirm = %v", tool, gone)
		}
	}
	after := state(t)
	for part, raw := range after {
		if strings.Contains(raw, `"`+name+`"`) {
			t.Errorf("after every delete was confirmed, Prowlarr's %s still has %q", part, name)
		}
	}
	if backups() != had-1 {
		t.Errorf("%d backups after the delete, %d before", backups(), had)
	}
}
