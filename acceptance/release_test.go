//go:build integration

package acceptance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleaseSearch(t *testing.T) {
	out := search(t, "severance", idxUsenet, idxTorrent)
	found := rows(t, out["releases"], "releases")
	// two episodes on each site
	if num(t, out["total"], "total") != 4 || len(found) != 4 {
		t.Fatalf("found %v", out)
	}
	byIndexer := object(t, out["by_indexer"], "by_indexer")
	if byIndexer[idxUsenet] != float64(2) || byIndexer[idxTorrent] != float64(2) {
		t.Errorf("by_indexer = %v", byIndexer)
	}
	for _, r := range found {
		if !strings.Contains(str(r["title"]), "Severance") || str(r["guid"]) == "" || num(t, r["indexer_id"], "indexer_id") == 0 {
			t.Errorf("release = %v", r)
		}
		// torrents carry their swarm, usenet none
		_, seeded := r["seeders"]
		if seeded != (str(r["protocol"]) == "torrent") {
			t.Errorf("seeders on a %s release: %v", r["protocol"], r)
		}
	}
	// most seeders first: torrents ahead of usenet, the busier episode first
	if str(found[0]["protocol"]) != "torrent" || num(t, found[0]["seeders"], "seeders") < num(t, found[1]["seeders"], "seeders") {
		t.Errorf("not sorted by seeders: %v, %v", found[0], found[1])
	}

	// narrowed by protocol, category and kind of search
	for _, r := range rows(t, call(t, "release_search", map[string]any{"query": "dune", "protocol": "usenet", "indexers": []string{idxUsenet, idxTorrent}})["releases"], "releases") {
		if str(r["protocol"]) != "usenet" {
			t.Errorf("a usenet search found %v", r)
		}
	}
	films := rows(t, call(t, "release_search", map[string]any{"categories": []string{"Movies"}, "indexers": []string{idxTorrent}})["releases"], "releases")
	if len(films) != 2 {
		t.Errorf("films = %v", films)
	}
	for _, r := range films {
		if !slices.Contains(strs(t, r["categories"], "categories"), "Movies") {
			t.Errorf("a movies search found %v", r)
		}
	}
	tv := call(t, "release_search", map[string]any{"query": "expanse", "type": "tv", "indexers": []string{idxTorrent}})
	if n := num(t, tv["total"], "total"); n != 1 {
		t.Errorf("a TV search for the expanse found %d", n)
	}
	if msg := callErr(t, "release_search", map[string]any{"type": "podcast"}); !strings.Contains(msg, "tv, movie") {
		t.Errorf("an unknown kind = %q", msg)
	}
	if msg := callErr(t, "release_search", map[string]any{"categories": []string{"Knitting"}}); !strings.Contains(msg, "category_list") {
		t.Errorf("an unknown category = %q", msg)
	}
	// sorted by size and by age
	for _, sort := range []string{"size", "age", "grabs"} {
		call(t, "release_search", map[string]any{"query": "severance", "sort": sort, "indexers": []string{idxTorrent}})
	}
}

func TestReleaseGrab(t *testing.T) {
	before := sitesUp.Downloads(siteTorrent)
	out := grabFrom(t, idxTorrent, "arrival")
	if !strings.Contains(str(out["grabbed"]), "Arrival") || str(out["indexer"]) != idxTorrent {
		t.Errorf("grabbed = %v", out)
	}
	// Prowlarr fetched it from the site and the blackhole wrote it out
	if sitesUp.Downloads(siteTorrent) <= before {
		t.Error("the site was never asked for the torrent")
	}
	dir := filepath.Join(dataDir(), "downloads", "torrent")
	if !eventually(func() bool {
		entries, _ := os.ReadDir(dir)
		return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.Contains(e.Name(), "Arrival") })
	}) {
		t.Errorf("no Arrival torrent in %s", dir)
	}

	// a usenet grab always redirects, which a blackhole cannot take
	found := rows(t, search(t, "arrival", idxUsenet)["releases"], "releases")
	if msg := callErr(t, "release_grab", map[string]any{"indexer": idxUsenet, "guid": str(found[0]["guid"]), "download_client": clientUsenet}); !strings.Contains(msg, "redirect") {
		t.Errorf("a usenet grab to a blackhole = %q", msg)
	}
	// a release no search has turned up
	if msg := callErr(t, "release_grab", map[string]any{"indexer": idxTorrent, "guid": "nothing-found-this"}); !strings.Contains(msg, "search") {
		t.Errorf("grabbing an unknown release = %q", msg)
	}
}

func TestCategoryList(t *testing.T) {
	cats := rows(t, call(t, "category_list", nil)["categories"], "categories")
	byID := map[int]map[string]any{}
	for _, c := range cats {
		byID[num(t, c["id"], "id")] = c
	}
	if str(byID[2000]["name"]) != "Movies" || num(t, byID[5040]["parent"], "parent") != 5000 {
		t.Errorf("categories = %v %v", byID[2000], byID[5040])
	}
}

func TestHistoryList(t *testing.T) {
	grabFrom(t, idxTorrent, "dune")
	out := call(t, "history_list", map[string]any{"event": "grab", "indexer": idxTorrent})
	events := rows(t, out["events"], "events")
	if len(events) == 0 || str(events[0]["event"]) != "grab" || !strings.Contains(str(events[0]["title"]), "Dune") {
		t.Fatalf("grabs = %v", events)
	}

	search(t, "expanse", idxUsenet)
	queries := rows(t, call(t, "history_list", map[string]any{"event": "query", "indexer": idxUsenet, "limit": 5})["events"], "events")
	if len(queries) == 0 {
		t.Fatal("no queries")
	}
	q := queries[0]
	if str(q["query"]) != "expanse" || q["successful"] != true || q["results"] == nil {
		t.Errorf("query = %v", q)
	}
	// the indexer's key is kept out of the request it records
	if u := str(q["url"]); strings.Contains(u, siteKeys[siteUsenet]) || !strings.Contains(u, "REDACTED") {
		t.Errorf("url = %q", u)
	}

	// the failures; Prowlarr may be holding the indexer back already, which
	// fails the search itself, and either way it has failed before
	_, _ = invoke("release_search", map[string]any{"query": "failures", "indexers": []string{idxRevoked}})
	failures := rows(t, call(t, "history_list", map[string]any{"successful": false})["events"], "events")
	if len(failures) == 0 {
		t.Error("no failures recorded")
	}
	for _, e := range failures {
		if e["successful"] != false {
			t.Errorf("a failure listing held %v", e)
		}
	}
	if msg := callErr(t, "history_list", map[string]any{"event": "download"}); !strings.Contains(msg, "grab") {
		t.Errorf("an unknown event = %q", msg)
	}
}
