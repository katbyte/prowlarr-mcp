//go:build acceptance

package tools_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
)

func TestReleaseSearch(t *testing.T) {
	out := search(t, "severance", idxUsenet, idxTorrent)
	found := acctest.Rows(t, out["releases"], "releases")
	// two episodes on each site
	if acctest.Num(t, out["total"], "total") != 4 || len(found) != 4 {
		t.Fatalf("found %v", out)
	}
	byIndexer := acctest.Object(t, out["by_indexer"], "by_indexer")
	if byIndexer[idxUsenet] != float64(2) || byIndexer[idxTorrent] != float64(2) {
		t.Errorf("by_indexer = %v", byIndexer)
	}
	for _, r := range found {
		if !strings.Contains(acctest.Str(r["title"]), "Severance") || acctest.Str(r["guid"]) == "" || acctest.Num(t, r["indexer_id"], "indexer_id") == 0 {
			t.Errorf("release = %v", r)
		}
		// torrents carry their swarm, usenet none
		_, seeded := r["seeders"]
		if seeded != (acctest.Str(r["protocol"]) == "torrent") {
			t.Errorf("seeders on a %s release: %v", r["protocol"], r)
		}
	}
	// most seeders first: torrents ahead of usenet, the busier episode first
	if acctest.Str(found[0]["protocol"]) != "torrent" || acctest.Num(t, found[0]["seeders"], "seeders") < acctest.Num(t, found[1]["seeders"], "seeders") {
		t.Errorf("not sorted by seeders: %v, %v", found[0], found[1])
	}

	// narrowed by protocol, category and kind of search
	for _, r := range acctest.Rows(t, suite.Call(t, "release_search", map[string]any{"query": "dune", "protocol": "usenet", "indexers": []string{idxUsenet, idxTorrent}})["releases"], "releases") {
		if acctest.Str(r["protocol"]) != "usenet" {
			t.Errorf("a usenet search found %v", r)
		}
	}
	films := acctest.Rows(t, suite.Call(t, "release_search", map[string]any{"categories": []string{"Movies"}, "indexers": []string{idxTorrent}})["releases"], "releases")
	if len(films) != 2 {
		t.Errorf("films = %v", films)
	}
	for _, r := range films {
		if !slices.Contains(acctest.Strs(t, r["categories"], "categories"), "Movies") {
			t.Errorf("a movies search found %v", r)
		}
	}
	tv := suite.Call(t, "release_search", map[string]any{"query": "expanse", "type": "tv", "indexers": []string{idxTorrent}})
	if n := acctest.Num(t, tv["total"], "total"); n != 1 {
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
	out := grabTorrent(t, "arrival")
	if grabbed := acctest.Strs(t, out["grabbed"], "grabbed"); len(grabbed) != 1 || !strings.Contains(grabbed[0], "Arrival") || acctest.Str(out["indexer"]) != idxTorrent {
		t.Errorf("grabbed = %v", out)
	}
	// Prowlarr fetched it from the site and the blackhole wrote it out
	if len(sitesUp.Indexer(siteTorrent).Grabbed()) <= before {
		t.Error("the site was never asked for the torrent")
	}
	dir := filepath.Join(testenv.DataDir(), "downloads", "torrent")
	if !acctest.EventuallyWithin(patience, func() bool {
		entries, _ := os.ReadDir(dir)
		return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), "Arrival") })
	}) {
		t.Errorf("no Arrival torrent in %s", dir)
	}

	// several at once, in Prowlarr's one bulk grab: each is fetched and named
	found := acctest.Rows(t, search(t, "severance", idxTorrent)["releases"], "releases")
	if len(found) < 2 {
		t.Fatalf("severance on %s found %d releases, want two to grab", idxTorrent, len(found))
	}
	fetched := len(sitesUp.Indexer(siteTorrent).Grabbed())
	several := suite.Call(t, "release_grab", map[string]any{"indexer": idxTorrent, "guids": []string{acctest.Str(found[0]["guid"]), acctest.Str(found[1]["guid"])}})
	if grabbed := acctest.Strs(t, several["grabbed"], "grabbed"); len(grabbed) != 2 || !strings.Contains(grabbed[0], "Severance") || !strings.Contains(grabbed[1], "Severance") {
		t.Errorf("two grabbed = %v", several)
	}
	if got := len(sitesUp.Indexer(siteTorrent).Grabbed()); got != fetched+2 {
		t.Errorf("the site was asked for %d torrents by the bulk grab, want 2", got-fetched)
	}

	// a usenet grab always redirects, which a blackhole cannot take
	found = acctest.Rows(t, search(t, "arrival", idxUsenet)["releases"], "releases")
	if msg := suite.CallErr(t, "release_grab", map[string]any{"indexer": idxUsenet, "guid": acctest.Str(found[0]["guid"]), "download_client": clientUsenet}); !strings.Contains(msg, "redirect") {
		t.Errorf("a usenet grab to a blackhole = %q", msg)
	}
	// a release no search has turned up
	if msg := suite.CallErr(t, "release_grab", map[string]any{"indexer": idxTorrent, "guid": "nothing-found-this"}); !strings.Contains(msg, "search") {
		t.Errorf("grabbing an unknown release = %q", msg)
	}
}

func TestCategoryList(t *testing.T) {
	cats := acctest.Rows(t, suite.Call(t, "category_list", nil)["categories"], "categories")
	byID := map[int]map[string]any{}
	for _, c := range cats {
		byID[acctest.Num(t, c["id"], "id")] = c
	}
	if acctest.Str(byID[2000]["name"]) != "Movies" || acctest.Num(t, byID[5040]["parent"], "parent") != 5000 {
		t.Errorf("categories = %v %v", byID[2000], byID[5040])
	}
}

func TestHistoryList(t *testing.T) {
	grabTorrent(t, "dune")
	out := suite.Call(t, "history_list", map[string]any{"event": "grab", "indexer": idxTorrent})
	events := acctest.Rows(t, out["events"], "events")
	if len(events) == 0 || acctest.Str(events[0]["event"]) != "grab" || !strings.Contains(acctest.Str(events[0]["title"]), "Dune") {
		t.Fatalf("grabs = %v", events)
	}

	search(t, "expanse", idxUsenet)
	queries := acctest.Rows(t, suite.Call(t, "history_list", map[string]any{"event": "query", "indexer": idxUsenet, "limit": 5})["events"], "events")
	if len(queries) == 0 {
		t.Fatal("no queries")
	}
	q := queries[0]
	if acctest.Str(q["query"]) != "expanse" || q["successful"] != true || q["results"] == nil {
		t.Errorf("query = %v", q)
	}
	// the indexer's key is kept out of the request it records
	if u := acctest.Str(q["url"]); strings.Contains(u, siteKeys[siteUsenet]) || !strings.Contains(u, "REDACTED") {
		t.Errorf("url = %q", u)
	}

	// the failures; Prowlarr may be holding the indexer back already, which
	// fails the search itself, and either way it has failed before
	_, _ = suite.Invoke("release_search", map[string]any{"query": "failures", "indexers": []string{idxRevoked}})
	failures := acctest.Rows(t, suite.Call(t, "history_list", map[string]any{"successful": false})["events"], "events")
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
