//go:build integration

package acceptance

import (
	"slices"
	"strings"
	"testing"

	acc "github.com/katbyte/go-kt/mcp/acctest"
)

func TestIndexerList(t *testing.T) {
	out := suite.Call(t, "indexer_list", nil)
	var names []string
	for _, row := range acc.Rows(t, out["indexers"], "indexers") {
		names = append(names, acc.Str(row["name"]))
	}
	for _, g := range genericIndexers {
		if !slices.Contains(names, g.Name) {
			t.Errorf("%s missing from %v", g.Name, names)
		}
	}
	if !slices.IsSortedFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }) {
		t.Errorf("not sorted by name: %v", names)
	}
	// Across The Tasman was added disabled
	if total, enabled := acc.Num(t, out["total"], "total"), acc.Num(t, out["enabled"], "enabled"); enabled != total-1 {
		t.Errorf("%d enabled of %d, want all but one", enabled, total)
	}

	row := indexerRow(t, idxUsenet)
	if acc.Str(row["protocol"]) != "usenet" || acc.Str(row["definition"]) != "Newznab" || !slices.Contains(acc.Strs(t, row["tags"], "tags"), tagMovies) ||
		acc.Str(row["sync_profile"]) != "Standard" || acc.Str(row["base_url"]) != sitesUp.Indexer(siteUsenet).URL() {
		t.Errorf("%s = %v", idxUsenet, row)
	}

	// the filters
	for _, c := range []struct {
		args map[string]any
		want func(map[string]any) bool
	}{
		{map[string]any{"protocol": "usenet"}, func(r map[string]any) bool { return acc.Str(r["protocol"]) == "usenet" }},
		{map[string]any{"privacy": "public"}, func(r map[string]any) bool { return acc.Str(r["privacy"]) == "public" }},
		{map[string]any{"tag": tagMovies}, func(r map[string]any) bool { return slices.Contains(acc.Strs(t, r["tags"], "tags"), tagMovies) }},
		{map[string]any{"enabled": false}, func(r map[string]any) bool { return acc.Str(r["name"]) == idxTasman }},
		{map[string]any{"name": "torrent"}, func(r map[string]any) bool { return strings.Contains(strings.ToLower(acc.Str(r["name"])), "torrent") }},
	} {
		got := acc.Rows(t, suite.Call(t, "indexer_list", c.args)["indexers"], "indexers")
		if len(got) == 0 {
			t.Errorf("%v matched nothing", c.args)
		}
		for _, r := range got {
			if !c.want(r) {
				t.Errorf("%v listed %v", c.args, r)
			}
		}
	}
}

func TestIndexerGet(t *testing.T) {
	out := suite.Call(t, "indexer_get", map[string]any{"indexer": idxTorrent})
	settings := acc.Object(t, out["settings"], "settings")
	if settings["baseUrl"] != sitesUp.Indexer(siteTorrent).URL() {
		t.Errorf("settings = %v", settings)
	}
	// the key is named, never shown
	if _, shown := settings["apiKey"]; shown || !slices.Contains(acc.Strs(t, out["secrets_set"], "secrets_set"), "apiKey") {
		t.Errorf("apiKey: settings %v, secrets_set %v", settings, out["secrets_set"])
	}
	if got := acc.Strs(t, out["searches"], "searches"); !slices.Contains(got, "tv") || !slices.Contains(got, "movie") {
		t.Errorf("searches = %v", got)
	}
	// which applications receive it, from the same rules the sync follows
	apps := map[string]map[string]any{}
	for _, a := range acc.Rows(t, out["apps"], "apps") {
		apps[acc.Str(a["app"])] = a
	}
	if apps[appSonarr]["synced"] != true || apps[appRadarr]["synced"] != true {
		t.Errorf("apps = %v", apps)
	}
	if apps[appLidarr]["synced"] != false || !strings.Contains(acc.Str(apps[appLidarr]["why_not"]), tagMusicOnly) {
		t.Errorf("Lidarr = %v, want not synced for want of its tag", apps[appLidarr])
	}

	// a Cardigann definition: its addresses, and the ones it has left
	out = suite.Call(t, "indexer_get", map[string]any{"indexer": idx1337x})
	if !slices.Contains(acc.Strs(t, out["indexer_urls"], "indexer_urls"), currentURL1337x) ||
		!slices.Contains(acc.Strs(t, out["legacy_urls"], "legacy_urls"), retiredURL1337x) || acc.Str(out["base_url"]) != retiredURL1337x {
		t.Errorf("1337x urls = %v, %v, base %v", out["indexer_urls"], out["legacy_urls"], out["base_url"])
	}

	// by id as well as by name, and an unknown one names what there is
	id := indexerID(t, idxUsenet)
	if got := suite.Call(t, "indexer_get", map[string]any{"indexer": itoa(id)}); acc.Str(got["name"]) != idxUsenet {
		t.Errorf("by id = %v", got["name"])
	}
	if msg := suite.CallErr(t, "indexer_get", map[string]any{"indexer": "Nope"}); !strings.Contains(msg, idxUsenet) {
		t.Errorf("unknown indexer error = %q, want the ones there are", msg)
	}
}

func TestIndexerTest(t *testing.T) {
	out := suite.Call(t, "indexer_test", map[string]any{"indexer": idxUsenet})
	res := acc.Rows(t, out["results"], "results")
	if len(res) != 1 || res[0]["ok"] != true {
		t.Errorf("testing a healthy indexer = %v", res)
	}
	// a failure is an answer, not an error
	out = suite.Call(t, "indexer_test", map[string]any{"indexer": idxRevoked})
	res = acc.Rows(t, out["results"], "results")
	if len(res) != 1 || res[0]["ok"] != false || len(acc.Strs(t, res[0]["problems"], "problems")) == 0 {
		t.Errorf("testing the revoked indexer = %v", res)
	}
	// every enabled one
	byName := map[string]bool{}
	for _, r := range acc.Rows(t, suite.Call(t, "indexer_test", nil)["results"], "results") {
		byName[acc.Str(r["name"])] = r["ok"] == true
	}
	if !byName[idxTorrent] || byName[idxDown] {
		t.Errorf("testing every indexer = %v", byName)
	}
}

func TestIndexerEdit(t *testing.T) {
	out := suite.Call(t, "indexer_edit", map[string]any{"indexer": idxTorrentCopy, "priority": 40, "add_tags": []string{"edit-test"}})
	row := acc.Object(t, out["indexer"], "indexer")
	if acc.Num(t, row["priority"], "priority") != 40 || !slices.Contains(acc.Strs(t, row["tags"], "tags"), "edit-test") {
		t.Errorf("edited = %v", row)
	}
	out = suite.Call(t, "indexer_edit", map[string]any{"indexer": idxTorrentCopy, "priority": 25, "remove_tags": []string{"edit-test"}})
	if slices.Contains(acc.Strs(t, acc.Object(t, out["indexer"], "indexer")["tags"], "tags"), "edit-test") {
		t.Error("remove_tags left the tag")
	}
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "edit-test"})

	// a setting it does not have is refused, naming the ones it has
	if msg := suite.CallErr(t, "indexer_edit", map[string]any{"indexer": idxUsenet, "settings": map[string]any{"apikee": "x"}}); !strings.Contains(msg, "apiKey") {
		t.Errorf("unknown setting error = %q", msg)
	}
	// Prowlarr tests before saving, so a change it cannot test is refused,
	// saying how to force it
	if msg := suite.CallErr(t, "indexer_edit", map[string]any{"indexer": idxRevoked, "priority": 30}); !strings.Contains(msg, "force=true") {
		t.Errorf("saving a failing indexer = %q, want the way to force it", msg)
	}
	suite.Call(t, "indexer_edit", map[string]any{"indexer": idxRevoked, "priority": 30, "force": true})
	suite.Call(t, "indexer_edit", map[string]any{"indexer": idxRevoked, "priority": 25, "force": true})
	// but a site that is down cannot be saved enabled at all, because
	// Prowlarr reads its capabilities on every save
	if msg := suite.CallErr(t, "indexer_edit", map[string]any{"indexer": idxDown, "priority": 30, "force": true}); !strings.Contains(msg, "enable=false") {
		t.Errorf("saving an indexer whose site is down = %q, want the way out", msg)
	}
	if msg := suite.CallErr(t, "indexer_edit", map[string]any{"indexer": idxUsenet}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("an edit of nothing = %q", msg)
	}
}

func TestIndexerBulkEdit(t *testing.T) {
	out := suite.Call(t, "indexer_bulk_edit", map[string]any{"indexers": []string{idxAnime, idxMusic}, "add_tags": []string{"bulk-test"}, "priority": 35})
	if acc.Num(t, out["changed"], "changed") != 2 {
		t.Errorf("changed = %v", out["changed"])
	}
	for _, row := range acc.Rows(t, out["indexers"], "indexers") {
		if acc.Num(t, row["priority"], "priority") != 35 || !slices.Contains(acc.Strs(t, row["tags"], "tags"), "bulk-test") {
			t.Errorf("bulk edited = %v", row)
		}
	}
	suite.Call(t, "indexer_bulk_edit", map[string]any{"tag": "bulk-test", "remove_tags": []string{"bulk-test"}, "priority": 25})
	if got := indexerRow(t, idxMusic); slices.Contains(acc.Strs(t, got["tags"], "tags"), "bulk-test") || acc.Num(t, got["priority"], "priority") != 25 {
		t.Errorf("after undoing the bulk edit = %v", got)
	}
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "bulk-test"})
	if msg := suite.CallErr(t, "indexer_bulk_edit", map[string]any{"priority": 10}); !strings.Contains(msg, "name the indexers") {
		t.Errorf("a bulk edit of nothing named = %q", msg)
	}
}

func TestIndexerCatalog(t *testing.T) {
	out := suite.Call(t, "indexer_catalog", map[string]any{"search": "1337x"})
	entries := acc.Rows(t, out["entries"], "entries")
	if len(entries) != 1 || acc.Str(entries[0]["definition"]) != "1337x" || entries[0]["cloudflare"] != true ||
		!slices.Contains(acc.Strs(t, entries[0]["added"], "added"), idx1337x) {
		t.Errorf("1337x in the catalogue = %v", entries)
	}
	// what adding one needs
	entries = acc.Rows(t, suite.Call(t, "indexer_catalog", map[string]any{"search": "milkie"})["entries"], "entries")
	if len(entries) != 1 || !slices.Contains(acc.Strs(t, entries[0]["needs"], "needs"), "apikey") {
		t.Errorf("Milkie's needs = %v", entries)
	}
	// the filters
	for _, e := range acc.Rows(t, suite.Call(t, "indexer_catalog", map[string]any{"protocol": "usenet", "limit": 100})["entries"], "entries") {
		if acc.Str(e["protocol"]) != "usenet" {
			t.Errorf("a usenet search listed %v", e)
		}
	}
	music := acc.Rows(t, suite.Call(t, "indexer_catalog", map[string]any{"category": 3000, "privacy": "private", "limit": 200})["entries"], "entries")
	if len(music) == 0 {
		t.Error("no private tracker in the catalogue carries audio")
	}
	for _, e := range music {
		if acc.Str(e["privacy"]) != "private" {
			t.Errorf("a private search listed %v", e)
		}
	}
}

func TestIndexerAddAndDelete(t *testing.T) {
	const name = "Temporary Torrent"
	out := suite.Call(t, "indexer_add", map[string]any{
		"definition": "Generic Torznab", "name": name, "base_url": sitesUp.Indexer(siteAnime).URL(),
		"settings": map[string]any{"apiKey": siteKeys[siteAnime]}, "tags": []string{"temp-tag"}, "seed_ratio": 2, "priority": 10,
	})
	row := acc.Object(t, out["indexer"], "indexer")
	if acc.Str(row["name"]) != name || acc.Num(t, row["priority"], "priority") != 10 || !slices.Contains(acc.Strs(t, row["tags"], "tags"), "temp-tag") {
		t.Errorf("added = %v", row)
	}
	if got := acc.Object(t, suite.Call(t, "indexer_get", map[string]any{"indexer": name})["settings"], "settings"); got["torrentBaseSettings.seedRatio"] != float64(2) {
		t.Errorf("seed ratio = %v", got)
	}
	// a wrong key fails Prowlarr's test and is refused
	if msg := suite.CallErr(t, "indexer_add", map[string]any{
		"definition": "Generic Torznab", "name": "Wrong Key", "base_url": sitesUp.Indexer(siteAnime).URL(), "settings": map[string]any{"apiKey": "wrong"},
	}); !strings.Contains(msg, "force=true") {
		t.Errorf("adding with a wrong key = %q", msg)
	}
	// a definition the catalogue does not have suggests the ones it does
	if msg := suite.CallErr(t, "indexer_add", map[string]any{"definition": "1337"}); !strings.Contains(msg, "1337x") {
		t.Errorf("an unknown definition = %q", msg)
	}

	// a second, so that both go at once in Prowlarr's bulk delete
	const other = "Temporary Too"
	suite.Call(t, "indexer_add", map[string]any{"definition": "Generic Torznab", "name": other, "base_url": sitesUp.Indexer(siteAnime).URL(), "settings": map[string]any{"apiKey": siteKeys[siteAnime]}})
	would := suite.Call(t, "indexer_delete", map[string]any{"indexers": []string{name, other}})
	if acc.Str(would["would_delete"]) != name+", "+other || would["deleted"] != nil {
		t.Errorf("two deletes without confirm = %v", would)
	}
	del := suite.Call(t, "indexer_delete", map[string]any{"indexers": []string{name, other}, "confirm": true})
	if acc.Str(del["deleted"]) != name+", "+other {
		t.Errorf("deleted = %v", del)
	}
	for _, gone := range []string{name, other} {
		if msg := suite.CallErr(t, "indexer_get", map[string]any{"indexer": gone}); !strings.Contains(msg, "no indexer") {
			t.Errorf("after the delete %s = %q", gone, msg)
		}
	}
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "temp-tag"})
}

func TestIndexerStats(t *testing.T) {
	search(t, "severance", idxUsenet, idxTorrent)
	out := suite.Call(t, "indexer_stats", nil)
	byName := map[string]map[string]any{}
	for _, row := range acc.Rows(t, out["indexers"], "indexers") {
		byName[acc.Str(row["indexer"])] = row
	}
	if acc.Num(t, byName[idxUsenet]["queries"], "queries") == 0 || acc.Num(t, out["queries"], "queries") == 0 {
		t.Errorf("stats = %v", out)
	}
	// the source of the searches: this suite, through Prowlarr's own search
	if len(acc.Rows(t, out["sources"], "sources")) == 0 {
		t.Error("no sources")
	}
	// one indexer, one protocol
	one := acc.Rows(t, suite.Call(t, "indexer_stats", map[string]any{"indexer": idxUsenet})["indexers"], "indexers")
	if len(one) != 1 || acc.Str(one[0]["indexer"]) != idxUsenet {
		t.Errorf("one indexer's stats = %v", one)
	}
	for _, row := range acc.Rows(t, suite.Call(t, "indexer_stats", map[string]any{"protocol": "usenet"})["indexers"], "indexers") {
		if name := acc.Str(row["indexer"]); name != idxUsenet && name != idxRevoked {
			t.Errorf("usenet stats listed %s", name)
		}
	}
	tagged := acc.Rows(t, suite.Call(t, "indexer_stats", map[string]any{"tag": tagMovies})["indexers"], "indexers")
	for _, row := range tagged {
		if name := acc.Str(row["indexer"]); name != idxUsenet && name != idxTorrent {
			t.Errorf("stats for the movies tag listed %s", name)
		}
	}
}
