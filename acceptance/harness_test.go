//go:build integration

// The harness: scripts/testenv.sh brings the container up and exports
// PROWLARR_SERVER, PROWLARR_TOKEN and PROWLARR_TEST_*; the suite starts the
// fakes Prowlarr talks to, then builds every fixture through the MCP tools
// rather than the HTTP API, so the setup is itself a test of indexer_add,
// app_add, proxy_add and the rest.
package acceptance

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/internal/fakes/newznab"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/katbyte/prowlarr-mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ctx     context.Context
	session *mcp.ClientSession
	ready   bool
	sdk     *prowlarr.Client
	sitesUp *newznab.Server
	appsUp  *servarr.Server

	calledMu sync.Mutex
	called   = map[string]bool{}
)

// configured reports whether the container environment is present.
func configured() bool {
	return os.Getenv("PROWLARR_SERVER") != "" && os.Getenv("PROWLARR_TOKEN") != ""
}

// dataDir is the host path the container's /config and /downloads are
// bind-mounted from, so a test can look at what Prowlarr wrote, or take a
// definition away.
func dataDir() string { return os.Getenv("PROWLARR_TEST_DATA") }

// testHost is the address the container reaches the fakes on.
func testHost() string {
	if h := os.Getenv("PROWLARR_TEST_HOST"); h != "" {
		return h
	}

	return "host.docker.internal"
}

func envPort(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}

	return def
}

func testMain(m *testing.M) {
	if !configured() {
		os.Exit(m.Run()) // every test skips
	}
	if err := start(); err != nil {
		stop()
		fmt.Fprintln(os.Stderr, "acceptance setup:", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()

	// every registered tool must have been called by something above. Only a
	// whole-suite run can say that, so a -run filter skips the check.
	if f := flag.Lookup("test.run"); f == nil || f.Value.String() == "" {
		missing, err := uncovered()
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "\ntool coverage: could not list tools:", err)
			code = 1
		case len(missing) > 0:
			fmt.Fprintf(os.Stderr, "\n%d registered tool(s) are never called by this suite:\n", len(missing))
			for _, name := range missing {
				fmt.Fprintln(os.Stderr, "  "+name)
			}
			fmt.Fprintln(os.Stderr, "every tool needs a test; add one or remove the tool")
			code = 1
		}
	}

	os.Exit(code)
}

func uncovered() ([]string, error) {
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}

	calledMu.Lock()
	defer calledMu.Unlock()

	var missing []string
	for _, tool := range res.Tools {
		if !called[tool.Name] {
			missing = append(missing, tool.Name)
		}
	}
	slices.Sort(missing)

	return missing, nil
}

// start runs the fakes, connects an MCP session to the tools, and seeds the
// fixtures.
func start() error {
	var err error
	sitesUp, err = newznab.New(newznab.Options{
		Addr: ":" + strconv.Itoa(envPort("PROWLARR_TEST_INDEXER_PORT", 19691)), PublicHost: testHost(), Sites: sites(),
	})
	if err != nil {
		return err
	}
	appsUp, err = servarr.New(servarr.Options{
		Addr: ":" + strconv.Itoa(envPort("PROWLARR_TEST_APP_PORT", 19692)), PublicHost: testHost(), Apps: fakeApps,
	})
	if err != nil {
		return err
	}
	if sdk, err = prowlarr.New(os.Getenv("PROWLARR_SERVER"), os.Getenv("PROWLARR_TOKEN")); err != nil {
		return err
	}

	ctx = context.Background()
	srv := mcp.NewServer(&mcp.Implementation{Name: "prowlarr-mcp", Version: "test"}, nil)
	if _, err := tools.RegisterAll(srv, sdk, tools.Options{EnableDelete: true}); err != nil {
		return err
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		return err
	}
	if session, err = mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil); err != nil {
		return err
	}
	ready = true

	return seed()
}

func stop() {
	if sitesUp != nil {
		_ = sitesUp.Close()
	}
	if appsUp != nil {
		_ = appsUp.Close()
	}
}

// seed builds the fixtures through the tools. It is idempotent: what already
// exists is left alone, so the suite can be re-run against a container that
// is still up.
func seed() error {
	have := map[string]bool{}
	list, err := invoke("indexer_list", nil)
	if err != nil {
		return err
	}
	for _, row := range rowsOf(list["indexers"]) {
		have[str(row["name"])] = true
	}

	for _, label := range []string{tagStray} {
		if _, err := invoke("tag_create", map[string]any{"label": label}); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	for _, g := range genericIndexers {
		if have[g.Name] {
			continue
		}
		def := "Generic Torznab"
		if strings.HasPrefix(g.Site, "nzb-") {
			def = "Generic Newznab"
		}
		args := map[string]any{
			"definition": def, "name": g.Name, "base_url": sitesUp.URL(g.Site),
			"settings": map[string]any{"apiKey": siteKeys[g.Site]},
		}
		if len(g.Tags) > 0 {
			args["tags"] = g.Tags
		}
		if _, err := invoke("indexer_add", args); err != nil {
			return fmt.Errorf("adding %s: %w", g.Name, err)
		}
	}
	// the catalogue's own: offline, so forced; 1337x on an address the site
	// has left and with no FlareSolverr, Milkie on one its definition does
	// not know, Across The Tasman disabled
	for _, c := range []map[string]any{
		{"definition": "1337x", "base_url": retiredURL1337x, "force": true},
		{"definition": "Milkie", "base_url": unlistedURLMilki, "force": true, "settings": map[string]any{"apikey": "milkiekey"}},
		{"definition": "Across The Tasman", "disabled": true, "force": true, "settings": map[string]any{"username": "u", "password": "p"}},
	} {
		if have[str(c["definition"])] {
			continue
		}
		if _, err := invoke("indexer_add", c); err != nil {
			return fmt.Errorf("adding %s: %w", c["definition"], err)
		}
	}

	if err := seedApps(); err != nil {
		return err
	}
	if err := seedProviders(); err != nil {
		return err
	}

	return arrange()
}

// arrange puts the fixtures in the state the audits look at, once everything
// exists: the main usenet indexer's VIP running out (only the generic Newznab
// and Torznab indexers record one), the anime indexer on the profile that
// turns everything off, the copy pinned to a download client that is then
// disabled, and the broken sites broken. The sites break only now because
// Prowlarr tests an indexer against its site before it will add it.
func arrange() error {
	for _, step := range []struct {
		tool string
		args map[string]any
	}{
		{"indexer_edit", map[string]any{"indexer": idxUsenet, "vip_expiration": vipIn(5)}},
		{"indexer_edit", map[string]any{"indexer": idxAnime, "sync_profile": profileNothing}},
		{"indexer_edit", map[string]any{"indexer": idxTorrentCopy, "download_client": clientSpare}},
		{"downloadclient_edit", map[string]any{"client": clientSpare, "enable": false}},
	} {
		if _, err := invoke(step.tool, step.args); err != nil {
			return err
		}
	}
	for site, f := range brokenSites {
		sitesUp.SetFailure(site, f)
	}

	return nil
}

// brokenSites are the sites arrange breaks, and how.
var brokenSites = map[string]newznab.Failure{
	siteDown:    {Status: 503},
	siteFlaky:   {Status: 503, Every: 2},
	siteRevoked: {Code: newznab.ErrIncorrectCredentials, Description: "Incorrect user credentials"},
}

func seedApps() error {
	have := map[string]bool{}
	list, err := invoke("app_list", nil)
	if err != nil {
		return err
	}
	for _, row := range rowsOf(list["apps"]) {
		have[str(row["name"])] = true
	}
	for _, a := range []map[string]any{
		{"kind": "Sonarr", "base_url": appsUp.URL("sonarr"), "api_key": "sonarrkey", "prowlarr_url": "http://" + os.Getenv("PROWLARR_TEST_CONTAINER") + ":9696"},
		// told to reach Prowlarr at localhost: what audit_apps finds
		{"kind": "Radarr", "base_url": appsUp.URL("radarr"), "api_key": "radarrkey", "prowlarr_url": "http://localhost:9696", "tags": []string{tagMovies}},
		// not served at all, and tagged for indexers that do not exist
		{"kind": "Lidarr", "base_url": appsUp.URL("lidarr"), "api_key": "lidarrkey", "prowlarr_url": "http://prowlarr:9696", "tags": []string{tagMusicOnly}, "force": true},
	} {
		name := str(a["kind"])
		if have[name] {
			continue
		}
		if _, err := invoke("app_add", a); err != nil {
			return fmt.Errorf("adding %s: %w", name, err)
		}
	}

	return nil
}

func seedProviders() error {
	profiles, err := invoke("profile_list", nil)
	if err != nil {
		return err
	}
	haveProfile := map[string]bool{}
	for _, row := range rowsOf(profiles["profiles"]) {
		haveProfile[str(row["name"])] = true
	}
	for _, p := range []map[string]any{
		{"name": profileNothing, "rss": false, "automatic_search": false, "interactive_search": false},
		{"name": profileSpare},
	} {
		if haveProfile[str(p["name"])] {
			continue
		}
		if _, err := invoke("profile_create", p); err != nil {
			return err
		}
	}

	proxies, err := invoke("proxy_list", nil)
	if err != nil {
		return err
	}
	haveProxy := map[string]bool{}
	for _, row := range rowsOf(proxies["proxies"]) {
		haveProxy[str(row["name"])] = true
	}
	for _, p := range []map[string]any{
		// nothing listens there, so it fails its test: forced
		{"kind": "FlareSolverr", "name": proxyFlare, "host": "http://" + testHost() + ":1", "tags": []string{tagFlaresolverr}, "force": true},
		{"kind": "Http", "name": proxyIdle, "host": "proxy.invalid", "port": 3128, "tags": []string{tagIdle}, "force": true},
	} {
		if haveProxy[str(p["name"])] {
			continue
		}
		if _, err := invoke("proxy_add", p); err != nil {
			return err
		}
	}

	clients, err := invoke("downloadclient_list", nil)
	if err != nil {
		return err
	}
	haveClient := map[string]bool{}
	for _, row := range rowsOf(clients["download_clients"]) {
		haveClient[str(row["name"])] = true
	}
	for _, c := range []map[string]any{
		{"kind": "TorrentBlackhole", "name": clientTorrent, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}},
		{"kind": "UsenetBlackhole", "name": clientUsenet, "settings": map[string]any{"nzbFolder": "/downloads/usenet"}},
		{"kind": "TorrentBlackhole", "name": clientSpare, "priority": 2, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}},
	} {
		if haveClient[str(c["name"])] {
			continue
		}
		if _, err := invoke("downloadclient_add", c); err != nil {
			return err
		}
	}

	return nil
}

// invoke calls a tool, recording that it was called.
func invoke(name string, args map[string]any) (map[string]any, error) {
	calledMu.Lock()
	called[name] = true
	calledMu.Unlock()

	if args == nil {
		args = map[string]any{}
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if res.IsError {
		var msgs []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msgs = append(msgs, tc.Text)
			}
		}
		return nil, fmt.Errorf("%s: %s", name, strings.Join(msgs, "; "))
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: structured content is %T", name, res.StructuredContent)
	}

	return out, nil
}

// call invokes a tool, skipping the test when the container is not configured
// and failing it when the tool errors.
func call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	if !ready {
		t.Skip("PROWLARR_SERVER and PROWLARR_TOKEN are not set; run: eval \"$(scripts/testenv.sh up)\"")
	}
	out, err := invoke(name, args)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// callErr invokes a tool expecting it to fail, and returns the error message.
func callErr(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	if !ready {
		t.Skip("PROWLARR_SERVER and PROWLARR_TOKEN are not set")
	}
	out, err := invoke(name, args)
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded: %v", name, out)
	}

	return err.Error()
}

// rowsOf reads a list of objects out of a decoded JSON value, nil when it is
// not one.
func rowsOf(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if row, ok := e.(map[string]any); ok {
			out = append(out, row)
		}
	}

	return out
}

// rows pulls a list of objects out of a decoded JSON field.
func rows(t *testing.T, v any, field string) []map[string]any {
	t.Helper()

	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a list", field, v)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		row, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%s contains %T, want objects", field, e)
		}
		out = append(out, row)
	}

	return out
}

// strs pulls a []string out of a decoded JSON field.
func strs(t *testing.T, v any, field string) []string {
	t.Helper()

	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a list", field, v)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("%s contains %T, want strings", field, e)
		}
		out = append(out, s)
	}

	return out
}

// object reads a nested object.
func object(t *testing.T, v any, field string) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T (%v), want an object", field, v, v)
	}

	return m
}

func num(t *testing.T, v any, field string) int {
	t.Helper()

	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%s is %T (%v), want a number", field, v, v)
	}

	return int(f)
}

// str pulls a string out of a decoded field, "" when absent.
func str(v any) string {
	s, _ := v.(string)
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }

// vipIn is a VIP expiry some days from today, as indexer_edit takes it. The
// day is UTC's, which is the day the audit counts from.
func vipIn(days int) string { return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02") }

// findings are an audit's findings as subject/problem pairs.
func findings(t *testing.T, out map[string]any) []string {
	t.Helper()

	var got []string
	for _, f := range rows(t, out["findings"], "findings") {
		got = append(got, str(f["subject"])+"/"+str(f["problem"]))
	}
	slices.Sort(got)

	return got
}

// finding reports whether an audit found a problem with a subject, and its
// detail.
func finding(t *testing.T, out map[string]any, subject, problem string) (string, bool) {
	t.Helper()

	for _, f := range rows(t, out["findings"], "findings") {
		if str(f["subject"]) == subject && str(f["problem"]) == problem {
			return str(f["detail"]), true
		}
	}

	return "", false
}

// indexerRow is one indexer as indexer_list shows it.
func indexerRow(t *testing.T, name string) map[string]any {
	t.Helper()

	for _, row := range rows(t, call(t, "indexer_list", nil)["indexers"], "indexers") {
		if str(row["name"]) == name {
			return row
		}
	}
	t.Fatalf("no indexer %q", name)

	return nil
}

// indexerID is an indexer's id.
func indexerID(t *testing.T, name string) int {
	t.Helper()

	return num(t, indexerRow(t, name)["id"], "id")
}

// search runs a search against some indexers, the way an application would,
// which gives them history and statistics to judge.
func search(t *testing.T, query string, indexers ...string) map[string]any {
	t.Helper()

	return call(t, "release_search", map[string]any{"query": query, "indexers": indexers})
}

// eventually polls a condition for up to 30 seconds.
func eventually(cond func() bool) bool {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if cond() {
			return true
		}
	}

	return cond()
}

// definitionsDir is where the container reads its tracker definitions from.
func definitionsDir() string { return filepath.Join(dataDir(), "config", "Definitions") }

// freePort finds a port nothing listens on, for a server a test starts.
func freePort(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	a, _ := ln.Addr().(*net.TCPAddr)

	return a.Port
}
