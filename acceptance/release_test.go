//go:build integration

package acceptance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	acc "github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
)

func TestReleaseSearch(t *testing.T) {
	out := search(t, "severance", idxUsenet, idxTorrent)
	found := acc.Rows(t, out["releases"], "releases")
	// two episodes on each site
	if acc.Num(t, out["total"], "total") != 4 || len(found) != 4 {
		t.Fatalf("found %v", out)
	}
	byIndexer := acc.Object(t, out["by_indexer"], "by_indexer")
	if byIndexer[idxUsenet] != float64(2) || byIndexer[idxTorrent] != float64(2) {
		t.Errorf("by_indexer = %v", byIndexer)
	}
	for _, r := range found {
		if !strings.Contains(acc.Str(r["title"]), "Severance") || acc.Str(r["guid"]) == "" || acc.Num(t, r["indexer_id"], "indexer_id") == 0 {
			t.Errorf("release = %v", r)
		}
		// torrents carry their swarm, usenet none
		_, seeded := r["seeders"]
		if seeded != (acc.Str(r["protocol"]) == "torrent") {
			t.Errorf("seeders on a %s release: %v", r["protocol"], r)
		}
	}
	// most seeders first: torrents ahead of usenet, the busier episode first
	if acc.Str(found[0]["protocol"]) != "torrent" || acc.Num(t, found[0]["seeders"], "seeders") < acc.Num(t, found[1]["seeders"], "seeders") {
		t.Errorf("not sorted by seeders: %v, %v", found[0], found[1])
	}

	// narrowed by protocol, category and kind of search
	for _, r := range acc.Rows(t, suite.Call(t, "release_search", map[string]any{"query": "dune", "protocol": "usenet", "indexers": []string{idxUsenet, idxTorrent}})["releases"], "releases") {
		if acc.Str(r["protocol"]) != "usenet" {
			t.Errorf("a usenet search found %v", r)
		}
	}
	films := acc.Rows(t, suite.Call(t, "release_search", map[string]any{"categories": []string{"Movies"}, "indexers": []string{idxTorrent}})["releases"], "releases")
	if len(films) != 2 {
		t.Errorf("films = %v", films)
	}
	for _, r := range films {
		if !slices.Contains(acc.Strs(t, r["categories"], "categories"), "Movies") {
			t.Errorf("a movies search found %v", r)
		}
	}
	tv := suite.Call(t, "release_search", map[string]any{"query": "expanse", "type": "tv", "indexers": []string{idxTorrent}})
	if n := acc.Num(t, tv["total"], "total"); n != 1 {
		t.Errorf("a TV search for the expanse found %d", n)
	}
	if msg := suite.CallErr(t, "release_search", map[string]any{"type": "podcast"}); !strings.Contains(msg, "tv, movie") {
		t.Errorf("an unknown kind = %q", msg)
	}
	if msg := suite.CallErr(t, "release_search", map[string]any{"categories": []string{"Knitting"}}); !strings.Contains(msg, "category_list") {
		t.Errorf("an unknown category = %q", msg)
	}
	// sorted by size and by age
	for _, sort := range []string{"size", "age", "grabs"} {
		suite.Call(t, "release_search", map[string]any{"query": "severance", "sort": sort, "indexers": []string{idxTorrent}})
	}
}

func TestReleaseGrab(t *testing.T) {
	before := len(sitesUp.Indexer(siteTorrent).Grabbed())
	out := grabFrom(t, idxTorrent, "arrival")
	if grabbed := acc.Strs(t, out["grabbed"], "grabbed"); len(grabbed) != 1 || !strings.Contains(grabbed[0], "Arrival") || acc.Str(out["indexer"]) != idxTorrent {
		t.Errorf("grabbed = %v", out)
	}
	// Prowlarr fetched it from the site and the blackhole wrote it out
	if len(sitesUp.Indexer(siteTorrent).Grabbed()) <= before {
		t.Error("the site was never asked for the torrent")
	}
	dir := filepath.Join(testenv.DataDir(), "downloads", "torrent")
	if !acc.EventuallyWithin(patience, func() bool {
		entries, _ := os.ReadDir(dir)
		return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), "Arrival") })
	}) {
		t.Errorf("no Arrival torrent in %s", dir)
	}

	// several at once, in Prowlarr's one bulk grab: each is fetched and named
	found := acc.Rows(t, search(t, "severance", idxTorrent)["releases"], "releases")
	if len(found) < 2 {
		t.Fatalf("severance on %s found %d releases, want two to grab", idxTorrent, len(found))
	}
	fetched := len(sitesUp.Indexer(siteTorrent).Grabbed())
	several := suite.Call(t, "release_grab", map[string]any{"indexer": idxTorrent, "guids": []string{acc.Str(found[0]["guid"]), acc.Str(found[1]["guid"])}})
	if grabbed := acc.Strs(t, several["grabbed"], "grabbed"); len(grabbed) != 2 || !strings.Contains(grabbed[0], "Severance") || !strings.Contains(grabbed[1], "Severance") {
		t.Errorf("two grabbed = %v", several)
	}
	if got := len(sitesUp.Indexer(siteTorrent).Grabbed()); got != fetched+2 {
		t.Errorf("the site was asked for %d torrents by the bulk grab, want 2", got-fetched)
	}

	// a usenet grab always redirects, which a blackhole cannot take
	found = acc.Rows(t, search(t, "arrival", idxUsenet)["releases"], "releases")
	if msg := suite.CallErr(t, "release_grab", map[string]any{"indexer": idxUsenet, "guid": acc.Str(found[0]["guid"]), "download_client": clientUsenet}); !strings.Contains(msg, "redirect") {
		t.Errorf("a usenet grab to a blackhole = %q", msg)
	}
	// a release no search has turned up
	if msg := suite.CallErr(t, "release_grab", map[string]any{"indexer": idxTorrent, "guid": "nothing-found-this"}); !strings.Contains(msg, "search") {
		t.Errorf("grabbing an unknown release = %q", msg)
	}
}

func TestCategoryList(t *testing.T) {
	cats := acc.Rows(t, suite.Call(t, "category_list", nil)["categories"], "categories")
	byID := map[int]map[string]any{}
	for _, c := range cats {
		byID[acc.Num(t, c["id"], "id")] = c
	}
	if acc.Str(byID[2000]["name"]) != "Movies" || acc.Num(t, byID[5040]["parent"], "parent") != 5000 {
		t.Errorf("categories = %v %v", byID[2000], byID[5040])
	}
}

func TestHistoryList(t *testing.T) {
	grabFrom(t, idxTorrent, "dune")
	out := suite.Call(t, "history_list", map[string]any{"event": "grab", "indexer": idxTorrent})
	events := acc.Rows(t, out["events"], "events")
	if len(events) == 0 || acc.Str(events[0]["event"]) != "grab" || !strings.Contains(acc.Str(events[0]["title"]), "Dune") {
		t.Fatalf("grabs = %v", events)
	}

	search(t, "expanse", idxUsenet)
	queries := acc.Rows(t, suite.Call(t, "history_list", map[string]any{"event": "query", "indexer": idxUsenet, "limit": 5})["events"], "events")
	if len(queries) == 0 {
		t.Fatal("no queries")
	}
	q := queries[0]
	if acc.Str(q["query"]) != "expanse" || q["successful"] != true || q["results"] == nil {
		t.Errorf("query = %v", q)
	}
	// the indexer's key is kept out of the request it records
	if u := acc.Str(q["url"]); strings.Contains(u, siteKeys[siteUsenet]) || !strings.Contains(u, "REDACTED") {
		t.Errorf("url = %q", u)
	}

	// the failures; Prowlarr may be holding the indexer back already, which
	// fails the search itself, and either way it has failed before
	_, _ = suite.Invoke("release_search", map[string]any{"query": "failures", "indexers": []string{idxRevoked}})
	failures := acc.Rows(t, suite.Call(t, "history_list", map[string]any{"successful": false})["events"], "events")
	if len(failures) == 0 {
		t.Error("no failures recorded")
	}
	for _, e := range failures {
		if e["successful"] != false {
			t.Errorf("a failure listing held %v", e)
		}
	}
	if msg := suite.CallErr(t, "history_list", map[string]any{"event": "download"}); !strings.Contains(msg, "grab") {
		t.Errorf("an unknown event = %q", msg)
	}
}
