package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// One release goes as Prowlarr's own search page sends one; several from the
// same indexer go in its one bulk grab, and each is named from the history
// where it records a grab.
func TestReleaseGrabOneOrSeveral(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Site", "private")})
	f.answerOK("POST /api/v1/search", map[string]any{"guid": "g1", "indexerId": 1})
	f.answerOK("POST /api/v1/search/bulk", []any{map[string]any{"guid": "g1", "indexerId": 1}, map[string]any{"guid": "g2", "indexerId": 1}})
	f.answer("GET /api/v1/history/indexer", []any{
		map[string]any{"id": 2, "indexerId": 1, "date": "2026-10-10T00:00:02Z", "eventType": "releaseGrabbed", "data": map[string]any{"grabTitle": "Second.Show.S01E02"}},
		map[string]any{"id": 1, "indexerId": 1, "date": "2026-10-10T00:00:01Z", "eventType": "releaseGrabbed", "data": map[string]any{"grabTitle": "First.Show.S01E01"}},
	})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "release_grab", map[string]any{"indexer": "Site", "guid": "g1"})
	if grabbed := listOf(out["grabbed"]); len(grabbed) != 1 || grabbed[0] != "Second.Show.S01E02" || len(f.requested("POST /api/v1/search")) != 1 || len(f.requested("POST /api/v1/search/bulk")) != 0 {
		t.Errorf("one grab = %v, sent %d single and %d bulk", out, len(f.requested("POST /api/v1/search")), len(f.requested("POST /api/v1/search/bulk")))
	}

	out = mustCall(t, cs, "release_grab", map[string]any{"indexer": "Site", "guids": []string{"g1", "g2"}})
	grabbed := listOf(out["grabbed"])
	if len(grabbed) != 2 || grabbed[0] != "First.Show.S01E01" || grabbed[1] != "Second.Show.S01E02" || len(f.requested("POST /api/v1/search/bulk")) != 1 {
		t.Errorf("two grabs = %v, sent %d bulk", out, len(f.requested("POST /api/v1/search/bulk")))
	}
	if sent := f.sent("POST /api/v1/search/bulk"); len(sent) != 1 || !strings.Contains(sent[0], `"guid":"g1"`) || !strings.Contains(sent[0], `"guid":"g2"`) {
		t.Errorf("the bulk grab sent %v", sent)
	}

	if msg := callError(t, cs, "release_grab", map[string]any{"indexer": "Site"}); !strings.Contains(msg, "guid") {
		t.Errorf("a grab of nothing = %q", msg)
	}
}

// foundOn is a canned release as a search answers it, with the address Prowlarr fetches its file through, its own API key in it.
func foundOn(indexer int, guid, title string) map[string]any {
	return map[string]any{
		"guid": guid, "indexerId": indexer, "indexer": "Site", "title": title, "protocol": "torrent", "infoHash": "abc123",
		"downloadUrl": "http://prowlarr:9696/1/download?apikey=PROWLARRKEY&link=LINK-" + guid + "&file=" + title,
	}
}

// A torrent a search found comes back attached to the answer as a file, fetched through Prowlarr by the link the search gave and without the API
// key that link carried, which no answer shows.
func TestReleaseDownloadAttachesTheTorrent(t *testing.T) {
	t.Parallel()

	torrent := "d8:announce20:http://tracker.test/4:infod6:lengthi1e4:name4:show12:piece lengthi1e6:pieces0:ee"
	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Site", "private")})
	f.answer("GET /api/v1/search", []any{foundOn(1, "g1", "Some.Show.S01E01")})
	f.handle("GET /api/v1/indexer/1/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Header().Set("Content-Disposition", `attachment; filename="Some.Show.S01E01.torrent"`)
		_, _ = w.Write([]byte(torrent))
	})
	cs := session(t, f, Options{})

	// a release no search has turned up is not fetched
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Site", "guid": "g1"}); !strings.Contains(msg, "release_search") || len(f.requested("GET /api/v1/indexer/1/download")) != 0 {
		t.Errorf("a download before any search = %q", msg)
	}

	found, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_search", Arguments: map[string]any{"query": "show"}})
	if err != nil || found.IsError {
		t.Fatalf("the search = %v, %v", found, err)
	}
	for _, c := range found.Content {
		if said, ok := c.(*mcp.TextContent); ok && (strings.Contains(said.Text, "PROWLARRKEY") || strings.Contains(said.Text, "LINK-g1")) {
			t.Errorf("the search shows the address Prowlarr fetches through: %s", said.Text)
		}
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_download", Arguments: map[string]any{"indexer": "Site", "guid": "g1"}})
	if err != nil || res.IsError {
		t.Fatalf("the download = %v, %v", res, err)
	}
	out := objectOf(res.StructuredContent)
	if out["file"] != "Some.Show.S01E01.torrent" || out["mime_type"] != "application/x-bittorrent" || out["size"] != float64(len(torrent)) || out["title"] != "Some.Show.S01E01" || out["info_hash"] != "abc123" || out["magnet"] != nil {
		t.Errorf("the download answered %v", out)
	}

	var attached *mcp.ResourceContents
	for _, c := range res.Content {
		if file, ok := c.(*mcp.EmbeddedResource); ok {
			attached = file.Resource
		}
		if said, ok := c.(*mcp.TextContent); ok && strings.Contains(said.Text, "PROWLARRKEY") {
			t.Errorf("the download shows Prowlarr's key: %s", said.Text)
		}
	}
	if attached == nil || string(attached.Blob) != torrent || attached.MIMEType != "application/x-bittorrent" || attached.URI != "prowlarr://release/1/Some.Show.S01E01.torrent" {
		t.Errorf("attached = %+v, want the torrent as the site gave it", attached)
	}

	if asked := f.requested("GET /api/v1/indexer/1/download"); len(asked) != 1 || !strings.Contains(asked[0], "link=LINK-g1") || !strings.Contains(asked[0], "file=Some.Show.S01E01") || strings.Contains(asked[0], "PROWLARRKEY") {
		t.Errorf("Prowlarr was asked for %v", asked)
	}
}

// A release with no file answers its magnet link, which Prowlarr gives by sending its caller on to it. An indexer that sends downloads on to the
// site is not followed there, and says what to do instead; a file too big for one answer is refused.
func TestReleaseDownloadWithoutAFileToAttach(t *testing.T) {
	t.Parallel()

	magnet := "magnet:?xt=urn:btih:abc123&dn=Some.Show.S01E01"
	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Site", "public"), torznab(2, "Sends On", "private"), torznab(3, "Huge", "private")})
	f.answer("GET /api/v1/search", []any{foundOn(1, "g1", "Some.Show.S01E01"), foundOn(2, "g2", "Other.Show.S01E01"), foundOn(3, "g3", "Every.Show.Ever")})
	f.handle("GET /api/v1/indexer/1/download", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, magnet, http.StatusMovedPermanently) })
	f.handle("GET /api/v1/indexer/2/download", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.srv.URL+"/site/file.torrent?passkey=SITEKEY", http.StatusMovedPermanently)
	})
	f.handle("GET /api/v1/indexer/3/download", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, maxDownload+1)) })
	cs := session(t, f, Options{})
	mustCall(t, cs, "release_search", map[string]any{"query": "show"})

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_download", Arguments: map[string]any{"indexer": "Site", "guid": "g1"}})
	if err != nil || res.IsError {
		t.Fatalf("the download = %v, %v", res, err)
	}
	if out := objectOf(res.StructuredContent); out["magnet"] != magnet || out["file"] != nil || out["size"] != nil {
		t.Errorf("a release with only a magnet link answered %v", out)
	}
	for _, c := range res.Content {
		if _, ok := c.(*mcp.EmbeddedResource); ok {
			t.Error("a magnet link came with a file attached")
		}
	}

	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Sends On", "guid": "g2"}); !strings.Contains(msg, "redirect") || !strings.Contains(msg, "release_grab") || strings.Contains(msg, "SITEKEY") || len(f.requested("GET /site/file.torrent")) != 0 {
		t.Errorf("an indexer that sends downloads on = %q, and the site was asked %d times", msg, len(f.requested("GET /site/file.torrent")))
	}
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Huge", "guid": "g3"}); !strings.Contains(msg, "8.0 MiB") || !strings.Contains(msg, "release_grab") {
		t.Errorf("a file too big for one answer = %q", msg)
	}
}

// A file that comes with no name or kind of its own goes out under the release's name, with nothing in it that reads as a folder, and a release
// that has only a magnet address is fetched through that one.
func TestReleaseDownloadNamesTheFile(t *testing.T) {
	t.Parallel()

	magnet := "magnet:?xt=urn:btih:abc123&dn=Only.A.Magnet"
	onlyMagnet := foundOn(2, "g2", "Only.A.Magnet")
	onlyMagnet["magnetUrl"], onlyMagnet["downloadUrl"] = "http://prowlarr:9696/2/download?apikey=PROWLARRKEY&link=MAGNET-g2&file=Only.A.Magnet", ""

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Site", "private"), torznab(2, "Magnets", "public")})
	f.answer("GET /api/v1/search", []any{foundOn(1, "g1", `Some/Show\S01E01`), onlyMagnet})
	f.handle("GET /api/v1/indexer/1/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = w.Write([]byte("d4:infod4:name4:showee"))
	})
	f.handle("GET /api/v1/indexer/2/download", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, magnet, http.StatusMovedPermanently) })
	cs := session(t, f, Options{})
	mustCall(t, cs, "release_search", map[string]any{"query": "show"})

	// the guid is taken with the space a careless copy leaves around it
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "release_download", Arguments: map[string]any{"indexer": "1", "guid": " g1 "}})
	if err != nil || res.IsError {
		t.Fatalf("the download = %v, %v", res, err)
	}
	if out := objectOf(res.StructuredContent); out["file"] != "Some_Show_S01E01.torrent" || out["mime_type"] != "application/octet-stream" || out["indexer"] != "Site" {
		t.Errorf("a file with no name of its own answered %v", out)
	}
	for _, c := range res.Content {
		if file, ok := c.(*mcp.EmbeddedResource); ok && file.Resource.URI != "prowlarr://release/1/Some_Show_S01E01.torrent" {
			t.Errorf("attached as %q", file.Resource.URI)
		}
	}

	if out := mustCall(t, cs, "release_download", map[string]any{"indexer": "Magnets", "guid": "g2"}); out["magnet"] != magnet {
		t.Errorf("a release with only a magnet address answered %v", out)
	}
	if asked := f.requested("GET /api/v1/indexer/2/download"); len(asked) != 1 || !strings.Contains(asked[0], "link=MAGNET-g2") || strings.Contains(asked[0], "PROWLARRKEY") {
		t.Errorf("Prowlarr was asked for %v", asked)
	}
}

// What cannot be fetched says why, and shows no key in saying it: an indexer nobody added, a release whose address is not one of Prowlarr's own, a
// file that says up front it is too big, and Prowlarr failing.
func TestReleaseDownloadRefusals(t *testing.T) {
	t.Parallel()

	direct := foundOn(1, "g1", "Straight.From.The.Site")
	direct["downloadUrl"] = "https://site.example/file.torrent?passkey=SITEKEY"

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Direct", "private"), torznab(2, "Huge", "private"), torznab(3, "Down", "private")})
	f.answer("GET /api/v1/search", []any{direct, foundOn(2, "g2", "Every.Show.Ever"), foundOn(3, "g3", "Some.Show.S01E01")})
	f.handle("GET /api/v1/indexer/2/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(maxDownload+1))
		_, _ = w.Write(make([]byte, maxDownload+1))
	})
	f.answerStatus("GET /api/v1/indexer/3/download", http.StatusInternalServerError, map[string]any{"message": "the site did not answer"})
	cs := session(t, f, Options{})
	mustCall(t, cs, "release_search", map[string]any{"query": "show"})

	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Nobody", "guid": "g1"}); !strings.Contains(msg, "Nobody") {
		t.Errorf("an indexer nobody added = %q", msg)
	}
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Direct", "guid": "g1"}); !strings.Contains(msg, "download links") || strings.Contains(msg, "SITEKEY") || len(f.requested("GET /api/v1/indexer/1/download")) != 0 {
		t.Errorf("an address that is not Prowlarr's own = %q", msg)
	}
	// a release found on one indexer is not there under another
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Direct", "guid": "g2"}); !strings.Contains(msg, "release_search") {
		t.Errorf("another indexer's release = %q", msg)
	}
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Huge", "guid": "g2"}); !strings.Contains(msg, "the file is 8.0 MiB, more than the 8.0 MiB") || !strings.Contains(msg, "release_grab") {
		t.Errorf("a file that says it is too big = %q", msg)
	}
	if msg := callError(t, cs, "release_download", map[string]any{"indexer": "Down", "guid": "g3"}); !strings.Contains(msg, "fetching the release from Down") || strings.Contains(msg, "PROWLARRKEY") {
		t.Errorf("Prowlarr failing = %q", msg)
	}
}

// What a search found can be downloaded for half an hour, the time Prowlarr keeps it for a grab, and is let go of after.
func TestFoundReleasesAreKeptHalfAnHour(t *testing.T) {
	t.Parallel()

	var found foundReleases
	found.remember([]prowlarr.ReleaseResource{{IndexerId: 1, Guid: "old", Title: "Old"}})
	found.releases[foundKey{1, "old"}] = foundRelease{title: "Old", at: time.Now().Add(-foundFor - time.Minute)}
	if _, ok := found.get(1, "old"); ok {
		t.Error("a release found over half an hour ago can still be downloaded")
	}

	found.remember([]prowlarr.ReleaseResource{{IndexerId: 1, Guid: "new", Title: "New"}})
	if rel, ok := found.get(1, "new"); !ok || rel.title != "New" || len(found.releases) != 1 {
		t.Errorf("after the next search = %+v, %v, holding %d", rel, ok, len(found.releases))
	}
	if _, ok := found.get(2, "new"); ok {
		t.Error("a release was found under another indexer's id")
	}
}
