//go:build acceptance

package tools_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
)

func appRow(t *testing.T, name string) map[string]any {
	t.Helper()

	for _, row := range acctest.Rows(t, suite.Call(t, "app_list", nil)["apps"], "apps") {
		if acctest.Str(row["name"]) == name {
			return row
		}
	}
	t.Fatalf("no application %q", name)

	return nil
}

func TestAppList(t *testing.T) {
	sonarr := appRow(t, appSonarr)
	if acctest.Str(sonarr["kind"]) != "Sonarr" || acctest.Str(sonarr["sync_level"]) != "fullSync" || acctest.Str(sonarr["base_url"]) != appsUp.URL("sonarr") ||
		!strings.Contains(acctest.Str(sonarr["sync_categories"]), "5000") || len(acctest.Strs(t, sonarr["tags"], "tags")) != 0 {
		t.Errorf("%s = %v", appSonarr, sonarr)
	}
	radarr := appRow(t, appRadarr)
	// Radarr takes only the indexers tagged movies
	if acctest.Num(t, radarr["indexers_synced"], "indexers_synced") != 2 {
		t.Errorf("%s receives %v indexers, want the two tagged %s", appRadarr, radarr["indexers_synced"], tagMovies)
	}
	if acctest.Num(t, appRow(t, appLidarr)["indexers_synced"], "indexers_synced") != 0 {
		t.Errorf("%s receives indexers, but no indexer carries its tag", appLidarr)
	}
}

func TestAppGet(t *testing.T) {
	out := suite.Call(t, "app_get", map[string]any{"app": appRadarr})
	if got := acctest.Strs(t, out["synced"], "synced"); !slices.Equal(got, []string{idxTorrent, idxUsenet}) {
		t.Errorf("%s receives %v", appRadarr, got)
	}
	skipped := map[string]string{}
	for _, s := range acctest.Rows(t, out["skipped"], "skipped") {
		skipped[acctest.Str(s["indexer"])] = acctest.Str(s["rule"])
	}
	if skipped[idxAnime] != "no_shared_tag" || skipped[idxTorrentCopy] != "no_shared_tag" {
		t.Errorf("skipped = %v", skipped)
	}
	// the API key is named, never shown
	if _, shown := acctest.Object(t, out["settings"], "settings")["apiKey"]; shown || !slices.Contains(acctest.Strs(t, out["secrets_set"], "secrets_set"), "apiKey") {
		t.Errorf("settings %v, secrets_set %v", out["settings"], out["secrets_set"])
	}

	// Sonarr takes every indexer carrying TV, and the anime one by its anime
	// categories; not the music one
	out = suite.Call(t, "app_get", map[string]any{"app": appSonarr})
	got := acctest.Strs(t, out["synced"], "synced")
	if !slices.Contains(got, idxAnime) || slices.Contains(got, idxMusic) {
		t.Errorf("%s receives %v", appSonarr, got)
	}
	for _, s := range acctest.Rows(t, out["skipped"], "skipped") {
		if acctest.Str(s["indexer"]) == idxMusic && acctest.Str(s["rule"]) != "no_shared_category" {
			t.Errorf("the music indexer is skipped for %v", s)
		}
	}
}

// app_sync pushes the indexers into the applications, and what arrives in
// the fake Sonarr and Radarr is what app_get said each would receive: the
// rules the tools work sync out by are Prowlarr's.
func TestAppSyncMatchesWhatArrives(t *testing.T) {
	out := suite.Call(t, "app_sync", map[string]any{"force": true})
	if st := acctest.Str(acctest.Object(t, out["command"], "command")["status"]); st != "completed" {
		t.Fatalf("sync = %v", out["command"])
	}
	for _, app := range []struct{ name, fake string }{{appSonarr, "sonarr"}, {appRadarr, "radarr"}} {
		detail := suite.Call(t, "app_get", map[string]any{"app": app.name})
		want, held := acctest.Strs(t, detail["synced"], "synced"), acctest.Strs(t, detail["held_back"], "held_back")
		var got []string
		// a held-back indexer stays where it already is, and is not added
		// anywhere new until it recovers, so it may or may not be there
		ok := acctest.EventuallyWithin(patience, func() bool {
			got = got[:0]
			for _, i := range appsUp.Indexers(app.fake) {
				got = append(got, strings.TrimSuffix(i.Name, " (Prowlarr)"))
			}
			slices.Sort(got)
			for _, name := range want {
				if !slices.Contains(got, name) && !slices.Contains(held, name) {
					return false
				}
			}
			return !slices.ContainsFunc(got, func(name string) bool { return !slices.Contains(want, name) })
		})
		if !ok {
			t.Errorf("%s holds %v, app_get says it receives %v (held back %v)", app.name, got, want, held)
		}
	}
	// what Prowlarr wrote into them: its own address, and the categories
	for _, i := range appsUp.Indexers("radarr") {
		if base := fmt.Sprint(i.Value("baseUrl")); !strings.HasPrefix(base, "http://localhost:9696/") {
			t.Errorf("%s in Radarr points at %v", i.Name, i.Value("baseUrl"))
		}
		if cats := i.Categories(); len(cats) == 0 || slices.ContainsFunc(cats, func(c int) bool { return c < 2000 || c >= 3000 }) {
			t.Errorf("%s in Radarr synced categories %v, want only movies", i.Name, cats)
		}
	}
}

func TestAppTest(t *testing.T) {
	res := acctest.Rows(t, suite.Call(t, "app_test", map[string]any{"app": appSonarr})["results"], "results")
	if len(res) != 1 || res[0]["ok"] != true {
		t.Errorf("testing Sonarr = %v", res)
	}
	byName := map[string]bool{}
	for _, r := range acctest.Rows(t, suite.Call(t, "app_test", nil)["results"], "results") {
		byName[acctest.Str(r["name"])] = r["ok"] == true
	}
	if !byName[appRadarr] || byName[appLidarr] {
		t.Errorf("testing every application = %v", byName)
	}
	// the fake saw the test: its status, and a test indexer
	if calls := appsUp.Calls("sonarr"); !slices.Contains(calls, "POST /api/v3/indexer/test") {
		t.Errorf("Sonarr saw %v", calls)
	}
}

func TestAppEdit(t *testing.T) {
	out := suite.Call(t, "app_edit", map[string]any{"app": appRadarr, "sync_categories": []int{2000, 2040}})
	if got := acctest.Str(acctest.Object(t, out["app"], "app")["sync_categories"]); got != "2000, 2040" {
		t.Errorf("sync categories = %q", got)
	}
	t.Cleanup(func() {
		_, _ = suite.Invoke("app_edit", map[string]any{"app": appRadarr, "sync_categories": []int{2000, 2010, 2020, 2030, 2040, 2045, 2050, 2060, 2070, 2080, 2090}})
	})
	if msg := suite.CallErr(t, "app_edit", map[string]any{"app": appRadarr, "sync_level": "sometimes"}); !strings.Contains(msg, "full, addOnly or disabled") {
		t.Errorf("a bad sync level = %q", msg)
	}
	// the tags decide what it receives: cleared, it takes every indexer
	// carrying films
	suite.Call(t, "app_edit", map[string]any{"app": appRadarr, "tags": []string{}})
	t.Cleanup(func() { _, _ = suite.Invoke("app_edit", map[string]any{"app": appRadarr, "tags": []string{tagMovies}}) })
	if n := acctest.Num(t, appRow(t, appRadarr)["indexers_synced"], "indexers_synced"); n <= 2 {
		t.Errorf("with no tags Radarr receives %d indexers, want more than the two tagged", n)
	}
}

func TestAppAddAndDelete(t *testing.T) {
	if err := appsUp.Add(servarr.App{Name: "readarr", Kind: servarr.Readarr, APIKey: "readarrkey"}); err != nil {
		t.Fatal(err)
	}
	out := suite.Call(t, "app_add", map[string]any{"kind": "Readarr", "name": "Books", "base_url": appsUp.URL("readarr"), "api_key": "readarrkey"})
	row := acctest.Object(t, out["app"], "app")
	if acctest.Str(row["kind"]) != "Readarr" || acctest.Str(row["name"]) != "Books" || acctest.Str(row["sync_level"]) != "fullSync" {
		t.Errorf("added = %v", row)
	}
	// it was not given the address it reaches Prowlarr by, so it was given
	// the one the tools use, and told that it may be wrong
	if !strings.Contains(acctest.Str(out["next"]), "prowlarr_url") {
		t.Errorf("next = %q, want the warning about a local address", out["next"])
	}
	// a wrong key fails the test and is refused
	if msg := suite.CallErr(t, "app_add", map[string]any{"kind": "Readarr", "name": "Wrong", "base_url": appsUp.URL("readarr"), "api_key": "wrong"}); !strings.Contains(msg, "force=true") {
		t.Errorf("adding with a wrong key = %q", msg)
	}
	if msg := suite.CallErr(t, "app_add", map[string]any{"kind": "Plex", "base_url": "http://x", "api_key": "k"}); !strings.Contains(msg, "Sonarr") {
		t.Errorf("an unknown kind = %q", msg)
	}
	if got := suite.Call(t, "app_delete", map[string]any{"app": "Books", "confirm": true}); acctest.Str(got["deleted"]) != "Books" {
		t.Errorf("deleted = %v", got)
	}
}

func TestProfiles(t *testing.T) {
	out := suite.Call(t, "profile_list", nil)
	byName := map[string]map[string]any{}
	for _, p := range acctest.Rows(t, out["profiles"], "profiles") {
		byName[acctest.Str(p["name"])] = p
	}
	std, nothing := byName["Standard"], byName[profileNothing]
	if std == nil || std["rss"] != true || !slices.Contains(acctest.Strs(t, std["indexers"], "indexers"), idxUsenet) {
		t.Errorf("Standard = %v", std)
	}
	if nothing == nil || nothing["rss"] != false || nothing["automatic_search"] != false || !slices.Contains(acctest.Strs(t, nothing["indexers"], "indexers"), idxAnime) {
		t.Errorf("%s = %v", profileNothing, nothing)
	}

	edited := suite.Call(t, "profile_edit", map[string]any{"profile": profileSpare, "minimum_seeders": 5, "interactive_search": false})
	if acctest.Num(t, edited["minimum_seeders"], "minimum_seeders") != 5 || edited["interactive_search"] != false || edited["rss"] != true {
		t.Errorf("edited = %v", edited)
	}

	made := suite.Call(t, "profile_create", map[string]any{"name": "Temporary"})
	if made["rss"] != true || acctest.Num(t, made["minimum_seeders"], "minimum_seeders") != 1 {
		t.Errorf("created = %v", made)
	}
	if got := suite.Call(t, "profile_delete", map[string]any{"profile": "Temporary", "confirm": true}); acctest.Str(got["deleted"]) != "Temporary" {
		t.Errorf("deleted = %v", got)
	}
	// one in use is refused, naming who uses it
	if msg := suite.CallErr(t, "profile_delete", map[string]any{"profile": profileNothing}); !strings.Contains(msg, idxAnime) {
		t.Errorf("deleting a profile in use = %q", msg)
	}
}
