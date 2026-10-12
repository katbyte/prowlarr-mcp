//go:build acceptance

package tools_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

func TestProxies(t *testing.T) {
	byName := map[string]map[string]any{}
	for _, p := range acctest.Rows(t, suite.Call(t, "proxy_list", nil)["proxies"], "proxies") {
		byName[acctest.Str(p["name"])] = p
	}
	flare, idle := byName[proxyFlare], byName[proxyIdle]
	if flare == nil || acctest.Str(flare["kind"]) != "FlareSolverr" || !slices.Contains(acctest.Strs(t, flare["tags"], "tags"), tagFlaresolverr) {
		t.Errorf("%s = %v", proxyFlare, flare)
	}
	if idle == nil || len(acctest.Strs(t, idle["indexers"], "indexers")) != 0 || acctest.Object(t, idle["settings"], "settings")["host"] != "proxy.invalid" {
		t.Errorf("%s = %v", proxyIdle, idle)
	}

	// nothing listens where FlareSolverr is said to be
	res := acctest.Rows(t, suite.Call(t, "proxy_test", map[string]any{"proxy": proxyFlare})["results"], "results")
	if len(res) != 1 || res[0]["ok"] != false {
		t.Errorf("testing %s = %v", proxyFlare, res)
	}
	suite.Call(t, "proxy_test", nil)

	edited := suite.Call(t, "proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 8080}, "add_tags": []string{"edit-proxy"}, "force": true})
	if acctest.Object(t, edited["settings"], "settings")["port"] != float64(8080) || !slices.Contains(acctest.Strs(t, edited["tags"], "tags"), "edit-proxy") {
		t.Errorf("edited = %v", edited)
	}
	suite.Call(t, "proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 3128}, "remove_tags": []string{"edit-proxy"}, "force": true})
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "edit-proxy"})

	suite.Call(t, "proxy_add", map[string]any{"kind": "Socks5", "name": "Temporary Proxy", "host": "socks.invalid", "port": 1080, "tags": []string{"temp-proxy"}, "force": true})
	if got := suite.Call(t, "proxy_delete", map[string]any{"proxy": "Temporary Proxy", "confirm": true}); acctest.Str(got["deleted"]) != "Temporary Proxy" {
		t.Errorf("deleted = %v", got)
	}
	_, _ = suite.Invoke("tag_delete", map[string]any{"label": "temp-proxy"})
	if msg := suite.CallErr(t, "proxy_add", map[string]any{"kind": "Socks5", "host": "x", "port": 1}); !strings.Contains(msg, "tag") {
		t.Errorf("a proxy with no tag = %q", msg)
	}
	if msg := suite.CallErr(t, "proxy_add", map[string]any{"kind": "Carrier Pigeon", "tags": []string{"x"}}); !strings.Contains(msg, "FlareSolverr") {
		t.Errorf("an unknown kind = %q", msg)
	}
}

func TestDownloadClients(t *testing.T) {
	byName := map[string]map[string]any{}
	for _, c := range acctest.Rows(t, suite.Call(t, "downloadclient_list", nil)["download_clients"], "download_clients") {
		byName[acctest.Str(c["name"])] = c
	}
	torrent, spare := byName[clientTorrent], byName[clientSpare]
	if torrent == nil || acctest.Str(torrent["protocol"]) != "torrent" || torrent["enabled"] != true ||
		acctest.Object(t, torrent["settings"], "settings")["torrentFolder"] != "/downloads/torrent" {
		t.Errorf("%s = %v", clientTorrent, torrent)
	}
	if spare == nil || spare["enabled"] != false || !slices.Contains(acctest.Strs(t, spare["indexers"], "indexers"), idxTorrentCopy) {
		t.Errorf("%s = %v", clientSpare, spare)
	}

	res := acctest.Rows(t, suite.Call(t, "downloadclient_test", map[string]any{"client": clientTorrent})["results"], "results")
	if len(res) != 1 || res[0]["ok"] != true {
		t.Errorf("testing %s = %v", clientTorrent, res)
	}
	suite.Call(t, "downloadclient_test", nil)

	edited := suite.Call(t, "downloadclient_edit", map[string]any{"client": clientTorrent, "priority": 3})
	if acctest.Num(t, edited["priority"], "priority") != 3 {
		t.Errorf("edited = %v", edited)
	}
	suite.Call(t, "downloadclient_edit", map[string]any{"client": clientTorrent, "priority": 1})
	if msg := suite.CallErr(t, "downloadclient_edit", map[string]any{"client": clientTorrent}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("an edit of nothing = %q", msg)
	}

	suite.Call(t, "downloadclient_add", map[string]any{"kind": "TorrentBlackhole", "name": "Temporary Client", "settings": map[string]any{"torrentFolder": "/downloads/torrent"}})
	if got := suite.Call(t, "downloadclient_delete", map[string]any{"client": "Temporary Client", "confirm": true}); acctest.Str(got["deleted"]) != "Temporary Client" {
		t.Errorf("deleted = %v", got)
	}
	if msg := suite.CallErr(t, "downloadclient_add", map[string]any{"kind": "Sabnzbd", "settings": map[string]any{"hots": "x"}}); !strings.Contains(msg, "host") {
		t.Errorf("a misspelt setting = %q", msg)
	}
}

func TestTags(t *testing.T) {
	byLabel := map[string]map[string]any{}
	for _, tag := range acctest.Rows(t, suite.Call(t, "tag_list", nil)["tags"], "tags") {
		byLabel[acctest.Str(tag["label"])] = tag
	}
	movies := byLabel[tagMovies]
	if movies == nil || !slices.Equal(acctest.Strs(t, movies["indexers"], "indexers"), []string{idxTorrent, idxUsenet}) ||
		!slices.Equal(acctest.Strs(t, movies["apps"], "apps"), []string{appRadarr}) || movies["unused"] != false {
		t.Errorf("%s = %v", tagMovies, movies)
	}
	if byLabel[tagStray]["unused"] != true {
		t.Errorf("%s = %v", tagStray, byLabel[tagStray])
	}

	made := suite.Call(t, "tag_create", map[string]any{"label": "Temporary-Tag"})
	if acctest.Str(made["label"]) != "temporary-tag" {
		t.Errorf("created = %v", made)
	}
	if msg := suite.CallErr(t, "tag_create", map[string]any{"label": "temporary-tag"}); !strings.Contains(msg, "already exists") {
		t.Errorf("a second one = %q", msg)
	}
	// renamed, though not onto a label another tag has
	if renamed := suite.Call(t, "tag_edit", map[string]any{"tag": "temporary-tag", "label": "Renamed-Tag"}); acctest.Str(renamed["label"]) != "renamed-tag" || renamed["unused"] != true {
		t.Errorf("renamed = %v", renamed)
	}
	if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "renamed-tag", "label": tagMovies}); !strings.Contains(msg, "already exists") {
		t.Errorf("renaming onto a tag there is = %q", msg)
	}
	if got := suite.Call(t, "tag_delete", map[string]any{"label": "renamed-tag"}); acctest.Str(got["deleted"]) != "renamed-tag" {
		t.Errorf("deleted = %v", got)
	}
	// one in use is refused, naming what carries it
	if msg := suite.CallErr(t, "tag_delete", map[string]any{"label": tagMovies}); !strings.Contains(msg, appRadarr) {
		t.Errorf("deleting a tag in use = %q", msg)
	}
}
