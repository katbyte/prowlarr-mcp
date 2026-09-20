//go:build integration

package acceptance

import (
	"slices"
	"strings"
	"testing"
)

func TestProxies(t *testing.T) {
	byName := map[string]map[string]any{}
	for _, p := range rows(t, call(t, "proxy_list", nil)["proxies"], "proxies") {
		byName[str(p["name"])] = p
	}
	flare, idle := byName[proxyFlare], byName[proxyIdle]
	if flare == nil || str(flare["kind"]) != "FlareSolverr" || !slices.Contains(strs(t, flare["tags"], "tags"), tagFlaresolverr) {
		t.Errorf("%s = %v", proxyFlare, flare)
	}
	if idle == nil || len(strs(t, idle["indexers"], "indexers")) != 0 || object(t, idle["settings"], "settings")["host"] != "proxy.invalid" {
		t.Errorf("%s = %v", proxyIdle, idle)
	}

	// nothing listens where FlareSolverr is said to be
	res := rows(t, call(t, "proxy_test", map[string]any{"proxy": proxyFlare})["results"], "results")
	if len(res) != 1 || res[0]["ok"] != false {
		t.Errorf("testing %s = %v", proxyFlare, res)
	}
	call(t, "proxy_test", nil)

	edited := call(t, "proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 8080}, "add_tags": []string{"edit-proxy"}, "force": true})
	if object(t, edited["settings"], "settings")["port"] != float64(8080) || !slices.Contains(strs(t, edited["tags"], "tags"), "edit-proxy") {
		t.Errorf("edited = %v", edited)
	}
	call(t, "proxy_edit", map[string]any{"proxy": proxyIdle, "settings": map[string]any{"port": 3128}, "remove_tags": []string{"edit-proxy"}, "force": true})
	_, _ = invoke("tag_delete", map[string]any{"label": "edit-proxy"})

	call(t, "proxy_add", map[string]any{"kind": "Socks5", "name": "Temporary Proxy", "host": "socks.invalid", "port": 1080, "tags": []string{"temp-proxy"}, "force": true})
	if got := call(t, "proxy_delete", map[string]any{"proxy": "Temporary Proxy"}); str(got["deleted"]) != "Temporary Proxy" {
		t.Errorf("deleted = %v", got)
	}
	_, _ = invoke("tag_delete", map[string]any{"label": "temp-proxy"})
	if msg := callErr(t, "proxy_add", map[string]any{"kind": "Socks5", "host": "x", "port": 1}); !strings.Contains(msg, "tag") {
		t.Errorf("a proxy with no tag = %q", msg)
	}
	if msg := callErr(t, "proxy_add", map[string]any{"kind": "Carrier Pigeon", "tags": []string{"x"}}); !strings.Contains(msg, "FlareSolverr") {
		t.Errorf("an unknown kind = %q", msg)
	}
}

func TestDownloadClients(t *testing.T) {
	byName := map[string]map[string]any{}
	for _, c := range rows(t, call(t, "downloadclient_list", nil)["download_clients"], "download_clients") {
		byName[str(c["name"])] = c
	}
	torrent, spare := byName[clientTorrent], byName[clientSpare]
	if torrent == nil || str(torrent["protocol"]) != "torrent" || torrent["enabled"] != true ||
		object(t, torrent["settings"], "settings")["torrentFolder"] != "/downloads/torrent" {
		t.Errorf("%s = %v", clientTorrent, torrent)
	}
	if spare == nil || spare["enabled"] != false || !slices.Contains(strs(t, spare["indexers"], "indexers"), idxTorrentCopy) {
		t.Errorf("%s = %v", clientSpare, spare)
	}

	res := rows(t, call(t, "downloadclient_test", map[string]any{"client": clientTorrent})["results"], "results")
	if len(res) != 1 || res[0]["ok"] != true {
		t.Errorf("testing %s = %v", clientTorrent, res)
	}
	call(t, "downloadclient_test", nil)

	edited := call(t, "downloadclient_edit", map[string]any{"client": clientTorrent, "priority": 3})
	if num(t, edited["priority"], "priority") != 3 {
		t.Errorf("edited = %v", edited)
	}
	call(t, "downloadclient_edit", map[string]any{"client": clientTorrent, "priority": 1})
	if msg := callErr(t, "downloadclient_edit", map[string]any{"client": clientTorrent}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("an edit of nothing = %q", msg)
	}

	call(t, "downloadclient_add", map[string]any{"kind": "TorrentBlackhole", "name": "Temporary Client", "settings": map[string]any{"torrentFolder": "/downloads/torrent"}})
	if got := call(t, "downloadclient_delete", map[string]any{"client": "Temporary Client"}); str(got["deleted"]) != "Temporary Client" {
		t.Errorf("deleted = %v", got)
	}
	if msg := callErr(t, "downloadclient_add", map[string]any{"kind": "Sabnzbd", "settings": map[string]any{"hots": "x"}}); !strings.Contains(msg, "host") {
		t.Errorf("a misspelt setting = %q", msg)
	}
}

func TestNotifications(t *testing.T) {
	// none are set up; the listing and the test of every one still answer
	if got := rows(t, call(t, "notification_list", nil)["notifications"], "notifications"); len(got) != 0 {
		t.Errorf("notifications = %v", got)
	}
	if got := rows(t, call(t, "notification_test", nil)["results"], "results"); len(got) != 0 {
		t.Errorf("testing none = %v", got)
	}
	if msg := callErr(t, "notification_test", map[string]any{"notification": "Discord"}); !strings.Contains(msg, "has none") {
		t.Errorf("testing one that is not there = %q", msg)
	}
}

func TestTags(t *testing.T) {
	byLabel := map[string]map[string]any{}
	for _, tag := range rows(t, call(t, "tag_list", nil)["tags"], "tags") {
		byLabel[str(tag["label"])] = tag
	}
	movies := byLabel[tagMovies]
	if movies == nil || !slices.Equal(strs(t, movies["indexers"], "indexers"), []string{idxTorrent, idxUsenet}) ||
		!slices.Equal(strs(t, movies["apps"], "apps"), []string{appRadarr}) || movies["unused"] != false {
		t.Errorf("%s = %v", tagMovies, movies)
	}
	if byLabel[tagStray]["unused"] != true {
		t.Errorf("%s = %v", tagStray, byLabel[tagStray])
	}

	made := call(t, "tag_create", map[string]any{"label": "Temporary-Tag"})
	if str(made["label"]) != "temporary-tag" {
		t.Errorf("created = %v", made)
	}
	if msg := callErr(t, "tag_create", map[string]any{"label": "temporary-tag"}); !strings.Contains(msg, "already exists") {
		t.Errorf("a second one = %q", msg)
	}
	if got := call(t, "tag_delete", map[string]any{"label": "temporary-tag"}); str(got["deleted"]) != "temporary-tag" {
		t.Errorf("deleted = %v", got)
	}
	// one in use is refused, naming what carries it
	if msg := callErr(t, "tag_delete", map[string]any{"label": tagMovies}); !strings.Contains(msg, appRadarr) {
		t.Errorf("deleting a tag in use = %q", msg)
	}
}
