//go:build acceptance

// Journeys: the tools chained the way a real session chains them, and held to
// what they say about themselves. The per-tool tests prove each tool answers;
// these prove that a tool which says it only reads leaves Prowlarr as it
// found it, that a write which says a repeat changes nothing more is right
// about that, that calls made at once do not undo each other, that a list
// read a page at a time is the list read whole, and that a delete does
// nothing until it is confirmed. A 200 from Prowlarr is not proof of any of
// it, so each is read back from Prowlarr itself.
package tools_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
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
	keep("indexers", indexers.Answer, err)
	apps, err := sdk.GetApplications(suite.Ctx)
	keep("applications", apps.Answer, err)
	profiles, err := sdk.GetAppProfile(suite.Ctx)
	keep("sync profiles", profiles.Answer, err)
	proxies, err := sdk.GetIndexerProxy(suite.Ctx)
	keep("proxies", proxies.Answer, err)
	clients, err := sdk.GetDownloadClient(suite.Ctx)
	keep("download clients", clients.Answer, err)
	notifications, err := sdk.GetNotification(suite.Ctx)
	keep("notifications", notifications.Answer, err)
	tags, err := sdk.GetTag(suite.Ctx)
	keep("tags", tags.Answer, err)
	filters, err := sdk.GetCustomFilter(suite.Ctx)
	keep("saved filters", filters.Answer, err)
	host, err := sdk.GetConfigHost(suite.Ctx)
	keep("host settings", host.Answer, err)
	ui, err := sdk.GetConfigUi(suite.Ctx)
	keep("ui settings", ui.Answer, err)
	development, err := sdk.GetConfigDevelopment(suite.Ctx)
	keep("development settings", development.Answer, err)

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

// --- a setup from nothing, and back ----------------------------------------

// What a person setting Prowlarr up for an application does, with the tools
// alone and in the order they would: a tag and a sync profile, a tracker
// added from the catalogue, a download client, the application with the tag,
// a sync, a search and a grab through it, a hook to be told when something
// breaks, a backup. Then the audits over the lot, which find the two things
// a first setup gets wrong, each fixed with the tool the audit names until
// the audits are clean of it; then every piece taken down again in the order
// that lets each go, until Prowlarr is as it was.
func TestJourneySetupAndTeardown(t *testing.T) {
	if !suite.Ready {
		t.Skip(testenv.NotConfigured)
	}
	if err := appsUp.Add(servarr.App{Name: "journey", Kind: servarr.Sonarr, APIKey: "journeykey"}); err != nil {
		t.Fatal(err)
	}
	const tag, profile, indexer, client, app, hook, filter = "journey", "Journey Profile", "Journey Torrent", "Journey Blackhole", "Journey Sonarr", "Journey Hook", "Journey Filter"
	// put back last to first: a profile in use cannot go before its indexer
	for _, later := range []struct {
		tool string
		args map[string]any
	}{
		{"profile_delete", map[string]any{"profile": profile}},
		{"downloadclient_delete", map[string]any{"client": client}},
		{"indexer_delete", map[string]any{"indexer": indexer}},
		{"app_delete", map[string]any{"app": app}},
		{"filter_delete", map[string]any{"filter": filter}},
		{"notification_delete", map[string]any{"notification": hook}},
	} {
		later.args["confirm"] = true
		suite.DeleteLaterIfThere(t, later.tool, later.args, "no ")
	}
	t.Cleanup(func() { _, _ = suite.Invoke("tag_delete", map[string]any{"label": tag}) })
	before := state(t)
	h := newHook(t)

	// set up
	suite.Call(t, "tag_create", map[string]any{"label": tag})
	suite.Call(t, "profile_create", map[string]any{"name": profile, "minimum_seeders": 2})
	added := acctest.Object(t, suite.Call(t, "indexer_add", map[string]any{"definition": "Generic Torznab", "name": indexer, "base_url": sitesUp.Indexer(siteAnime).URL(), "settings": map[string]any{"apiKey": siteKeys[siteAnime]}, "tags": []string{tag}, "sync_profile": profile})["indexer"], "indexer")
	if acctest.Str(added["sync_profile"]) != profile || !slices.Contains(acctest.Strs(t, added["tags"], "tags"), tag) {
		t.Fatalf("the indexer as added = %v", added)
	}
	suite.Call(t, "downloadclient_add", map[string]any{"kind": "TorrentBlackhole", "name": client, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}})
	// as a first setup has it: no seed goal on the private tracker, and the
	// application told to reach Prowlarr at the default, localhost
	suite.Call(t, "app_add", map[string]any{"kind": "Sonarr", "name": app, "base_url": appsUp.URL("journey"), "api_key": "journeykey", "tags": []string{tag}})
	suite.Call(t, "notification_add", map[string]any{"kind": "Webhook", "name": hook, "on": []string{"health_issue", "health_restored"}, "health_warnings": true, "settings": map[string]any{"url": h.url}})
	suite.Call(t, "filter_create", map[string]any{"label": filter, "page": "indexerIndex", "filters": []map[string]any{{"key": "tags", "type": "contains", "value": []string{tag}}}})

	// the application receives the tagged tracker, and only that one
	suite.Call(t, "app_sync", map[string]any{"force": true})
	if got := acctest.Strs(t, suite.Call(t, "app_get", map[string]any{"app": app})["synced"], "synced"); !slices.Equal(got, []string{indexer}) {
		t.Errorf("%s receives %v, want only %s", app, got, indexer)
	}
	if !acctest.EventuallyWithin(patience, func() bool {
		return slices.ContainsFunc(appsUp.Indexers("journey"), func(i servarr.Indexer) bool { return strings.Contains(i.Name, indexer) })
	}) {
		t.Errorf("the fake Sonarr never received %s: %v", indexer, appsUp.Indexers("journey"))
	}

	// a search and a grab through it, which the history then shows
	found := acctest.Rows(t, search(t, "frieren", indexer)["releases"], "releases")
	if len(found) == 0 {
		t.Fatalf("nothing found on %s", indexer)
	}
	grabbed := suite.Call(t, "release_grab", map[string]any{"indexer": indexer, "guid": acctest.Str(found[0]["guid"]), "download_client": client})
	if got := acctest.Strs(t, grabbed["grabbed"], "grabbed"); len(got) != 1 {
		t.Errorf("grabbed = %v", grabbed)
	}
	if !acctest.EventuallyWithin(patience, func() bool {
		return len(acctest.Rows(t, suite.Call(t, "history_list", map[string]any{"indexer": indexer, "event": "grab"})["events"], "events")) == 1
	}) {
		t.Errorf("the history does not show the grab from %s", indexer)
	}

	// told when something breaks: the hook hears Prowlarr's test, and a
	// backup is the way back
	if res := acctest.Rows(t, suite.Call(t, "notification_test", map[string]any{"notification": hook})["results"], "results"); len(res) != 1 || !acctest.IsBool(res[0]["ok"], true) || !slices.Contains(h.got(), "Test") {
		t.Errorf("the hook = %v, heard %v", res, h.got())
	}
	backup := acctest.Str(acctest.Object(t, suite.Call(t, "backup_create", nil)["backup"], "backup")["name"])
	suite.DeleteLaterIfThere(t, "backup_delete", map[string]any{"backup": backup, "confirm": true}, "no backup")

	// the audits over the lot: what they report of this journey's own
	ownFindings := func() map[string][]string {
		own := map[string][]string{}
		for _, row := range acctest.Rows(t, suite.Call(t, "audit_all", nil)["audits"], "audits") {
			if acctest.Num(t, row["findings"], "findings") == 0 {
				continue
			}
			audit := acctest.Str(row["audit"])
			for _, f := range findings(t, suite.Call(t, audit, map[string]any{"limit": 1000})) {
				if strings.HasPrefix(f, "Journey ") || strings.HasPrefix(f, tag+"/") {
					own[audit] = append(own[audit], f)
				}
			}
		}

		return own
	}
	// a first setup gets two things wrong, and the audits say which
	own := ownFindings()
	if !slices.Equal(own["audit_seeding"], []string{indexer + "/no_seed_goal"}) || !slices.Equal(own["audit_apps"], []string{app + "/prowlarr_url_local"}) || len(own) != 2 {
		t.Fatalf("the audits over a first setup report %v, want the tracker's missing seed goal and the application's localhost address and nothing else", own)
	}
	// each fixed with the tool its audit names
	suite.Call(t, "indexer_edit", map[string]any{"indexer": indexer, "seed_ratio": 1})
	suite.Call(t, "app_edit", map[string]any{"app": app, "prowlarr_url": "http://" + testenv.Container() + ":9696"})
	suite.Call(t, "app_sync", map[string]any{"force": true})
	if own := ownFindings(); len(own) != 0 {
		t.Errorf("after the fixes the audits still report %v", own)
	}

	// take it down, each in the order that lets it go: what is in use
	// cannot be deleted first
	for _, step := range []struct {
		tool string
		args map[string]any
	}{
		{"notification_delete", map[string]any{"notification": hook}},
		{"filter_delete", map[string]any{"filter": filter}},
		{"app_delete", map[string]any{"app": app}},
		{"indexer_delete", map[string]any{"indexer": indexer}},
		{"downloadclient_delete", map[string]any{"client": client}},
		{"profile_delete", map[string]any{"profile": profile}},
		{"backup_delete", map[string]any{"backup": backup}},
	} {
		step.args["confirm"] = true
		if gone := suite.Call(t, step.tool, step.args); acctest.Str(gone["deleted"]) == "" {
			t.Errorf("%s = %v", step.tool, gone)
		}
	}
	suite.Call(t, "tag_delete", map[string]any{"label": tag})
	if parts := changed(before, state(t)); len(parts) > 0 {
		t.Errorf("after the teardown Prowlarr's %s differ from before the setup", strings.Join(parts, ", "))
	}
}

// --- no answer shows a stored key -------------------------------------------

// The failing indexers are made to fail afresh and then tested, and every
// answer that can quote the request that failed is asked for. That is when
// Prowlarr writes the whole address of the request, key and all, into a
// failed test, its log and the error it refuses a save with. The harness
// reads every answer for every key the suite gave Prowlarr (suite.Secret),
// so the calls are what this journey makes, and the watch is what fails it.
func TestJourneyNoAnswerShowsAStoredKey(t *testing.T) {
	if !suite.Ready {
		t.Skip(testenv.NotConfigured)
	}

	// words no test has searched for, so the failing indexers are asked again
	_, _ = suite.Invoke("release_search", map[string]any{"query": "a stored key", "indexers": []string{idxDown, idxRevoked, idxFlaky}})
	suite.Call(t, "task_run", map[string]any{"name": "CheckHealth"})

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"indexer_test", nil},
		{"indexer_test", map[string]any{"indexer": idxDown}},
		{"indexer_test", map[string]any{"indexer": idxRevoked}},
		{"app_test", nil},
		{"proxy_test", nil},
		{"downloadclient_test", nil},
		{"audit_failing", map[string]any{"test": true}},
		{"audit_apps", map[string]any{"test": true}},
		{"audit_proxies", map[string]any{"test": true}},
		{"audit_downloadclients", map[string]any{"test": true}},
		{"audit_health", nil},
		{"audit_definitions", nil},
		{"server_health", map[string]any{"check": true}},
		{"server_info", nil},
		{"server_settings", nil},
		{"server_log", map[string]any{"limit": 1000}},
		{"history_list", map[string]any{"limit": 1000}},
		{"command_list", nil},
		{"indexer_list", nil},
		{"indexer_get", map[string]any{"indexer": idxDown}},
		{"indexer_get", map[string]any{"indexer": "Milkie"}},
		{"indexer_stats", nil},
		{"app_list", nil},
		{"app_get", map[string]any{"app": appSonarr}},
		{"proxy_list", nil},
		{"downloadclient_list", nil},
		{"notification_list", nil},
		// a save Prowlarr refuses with a 500 that quotes the address it made of this one, the indexer's own key on the end
		{"indexer_edit", map[string]any{"indexer": idxTorrent, "base_url": "https://someone:a-sign-in-word@site.example/path?passkey=a-pass-key#frag", "force": true}}, //nolint:gosec // a made-up address, there to prove the sign-in in it is not shown
	}
	suite.Secret("a-sign-in-word")
	suite.Secret("a-pass-key")
	for _, c := range calls {
		_, _ = suite.Invoke(c.tool, c.args)
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
	filter := acctest.Num(t, suite.Call(t, "filter_create", map[string]any{"label": "Twice", "page": "history", "filters": []map[string]any{{"key": "successful", "type": "equal", "value": false}}})["id"], "id")
	suite.DeleteLaterIfThere(t, "filter_delete", map[string]any{"filter": itoa(filter), "confirm": true}, "no filter")
	rotate := 0
	for _, sec := range acctest.Rows(t, suite.Call(t, "server_settings", map[string]any{"section": "development"})["sections"], "sections") {
		rotate = acctest.Num(t, acctest.Object(t, sec["settings"], "settings")["logRotate"], "logRotate")
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
	want := make([]string, 0, 1+edits)
	want = append(want, "at-once")
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

	held := acctest.Strs(t, indexerRow(t, idxMusic)["tags"], "tags")
	for _, label := range want {
		if !slices.Contains(held, label) {
			t.Errorf("%s holds %v after %d edits at once, and not %s", idxMusic, held, edits, label)
		}
	}
	shared := 0
	for _, tag := range acctest.Rows(t, suite.Call(t, "tag_list", nil)["tags"], "tags") {
		if acctest.Str(tag["label"]) == "at-once" {
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
	// ticks are the fields of a row that move with the clock between two
	// calls: the rows are compared without them
	ticks []string
}

// Every list read a page at a time, following next_offset until there is
// none, is the list read whole: as many rows as its total says, the same
// rows, in the same order. A list that is added fails the test until it is
// walked here.
func TestJourneyEveryListAddsUpToItsTotal(t *testing.T) {
	st := suite.Call(t, "server_info", nil)
	home.Offer(arrserver.Update{Version: "99.0.0.3"}, arrserver.Update{Version: "99.0.0.2"}, arrserver.Update{Version: acctest.Str(st["version"])})
	t.Cleanup(func() { home.Offer() })

	lists := map[string]listTool{
		"indexer_list": {rows: "indexers"}, "app_list": {rows: "apps"}, "profile_list": {rows: "profiles"}, "proxy_list": {rows: "proxies"},
		"downloadclient_list": {rows: "download_clients"}, "notification_list": {rows: "notifications"}, "tag_list": {rows: "tags"},
		"category_list": {rows: "categories"}, "backup_list": {rows: "backups"}, "filter_list": {rows: "filters"},
		"indexer_catalog": {rows: "entries"}, "indexer_stats": {rows: "indexers"}, "server_health": {rows: "checks"},
		"server_updates": {rows: "updates"},
		// a release's age is in tenths of an hour, a log file is written as Prowlarr works, and a task that comes due runs
		"release_search": {rows: "releases", args: map[string]any{"indexers": []string{idxTorrent}}, ticks: []string{"age_hours"}},
		"server_logs":    {rows: "files", ticks: []string{"modified"}},
		"task_list":      {rows: "tasks", ticks: []string{"last_run", "last_duration", "next_run"}},
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

		// walk reads the list whole and then a page at a time, and says what
		// does not add up
		walk := func() (problems []string) {
			whole := suite.Call(t, tool.Name, with(map[string]any{"limit": 1000}))
			all, count := append([]any{}, acctest.RowsOfAny(whole[l.rows])...), acctest.Num(t, whole[total], total)
			if len(all) != count || whole["next_offset"] != nil {
				problems = append(problems, fmt.Sprintf("%s read whole: %d rows, a total of %d, next_offset %v", tool.Name, len(all), count, whole["next_offset"]))
			}

			// in pages small enough that there are several, when there is that much
			size := max(1, count/4)
			walked := []any{}
			offset, pages := 0, 0
			for {
				page := suite.Call(t, tool.Name, with(map[string]any{"limit": size, "offset": offset}))
				rows := acctest.RowsOfAny(page[l.rows])
				if len(rows) > size || acctest.Num(t, page["offset"], "offset") != offset {
					problems = append(problems, fmt.Sprintf("%s limit %d offset %d answered %d rows from offset %v", tool.Name, size, offset, len(rows), page["offset"]))
				}
				walked = append(walked, rows...)
				next, more := page["next_offset"].(float64)
				pages++
				if !more || pages > 2000 {
					break
				}
				if int(next) != offset+len(rows) {
					problems = append(problems, fmt.Sprintf("%s: next_offset %v after %d rows from %d", tool.Name, next, len(rows), offset))
				}
				offset = int(next)
			}

			switch {
			case l.moves != "":
				// each page was held to its limit, its offset and its next_offset above
			case l.grows != "":
				if len(walked) < count {
					problems = append(problems, fmt.Sprintf("%s a page at a time is %d rows, fewer than the %d it had when read whole", tool.Name, len(walked), count))
				}
			default:
				for _, row := range slices.Concat(all, walked) {
					if fields, ok := row.(map[string]any); ok {
						for _, field := range l.ticks {
							delete(fields, field)
						}
					}
				}
				a, _ := json.Marshal(all)
				b, _ := json.Marshal(walked)
				if len(walked) != count || !bytes.Equal(a, b) {
					problems = append(problems, fmt.Sprintf("%s a page at a time (%d rows in %d pages of %d) is not the list read whole (%d rows)", tool.Name, len(walked), pages, size, count))
				}
			}

			return problems
		}

		// what a list holds can change under a walk of it - Prowlarr runs a
		// health check again, a task comes due - so a walk that does not add
		// up is made once more: a list that pages wrongly does so every time
		problems := walk()
		if len(problems) > 0 {
			problems = walk()
		}
		for _, problem := range problems {
			t.Error(problem)
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
	backup := acctest.Str(acctest.Object(t, suite.Call(t, "backup_create", nil)["backup"], "backup")["name"])
	t.Cleanup(func() { _, _ = suite.Invoke("tag_delete", map[string]any{"label": "ask-first"}) })

	deletes := map[string]map[string]any{
		"indexer_delete": {"indexer": name}, "app_delete": {"app": name}, "profile_delete": {"profile": name}, "proxy_delete": {"proxy": name},
		"downloadclient_delete": {"client": name}, "notification_delete": {"notification": name}, "filter_delete": {"filter": name}, "backup_delete": {"backup": backup},
	}
	backups := func() int { return acctest.Num(t, suite.Call(t, "backup_list", nil)["total"], "total") }
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
		if acctest.Str(would["would_delete"]) != want || would["deleted"] != nil || len(acctest.Strs(t, would["loses"], "loses")) == 0 {
			t.Errorf("%s without confirm = %v, want it to say %s would go, and what with it", tool.Name, would, want)
		}
	}
	if parts := changed(before, state(t)); len(parts) > 0 || backups() != had {
		t.Fatalf("the deletes changed Prowlarr's %s without confirm (backups %d, then %d)", strings.Join(parts, ", "), had, backups())
	}

	for tool, args := range deletes {
		args["confirm"] = true
		if gone := suite.Call(t, tool, args); acctest.Str(gone["deleted"]) == "" || gone["would_delete"] != nil {
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
