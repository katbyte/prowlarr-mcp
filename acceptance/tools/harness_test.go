//go:build acceptance

// The harness: scripts/testenv.sh brings the container up and exports
// PROWLARR_SERVER, PROWLARR_TOKEN and PROWLARR_TEST_*; the suite starts the
// fakes Prowlarr talks to, then builds every fixture through the MCP tools
// rather than the HTTP API, so the setup is itself a test of indexer_add,
// app_add, proxy_add and the rest.
package tools_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/go-kt/test/services/newznab"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/katbyte/prowlarr-mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// suite drives the tools through an MCP session; until start connects
	// it, every call skips
	suite   = &acctest.Suite{NotReady: testenv.NotConfigured}
	sdk     *prowlarr.Client
	sitesUp *newznab.Server
	appsUp  *servarr.Server
	// home is the server Prowlarr calls home to, as the suite plays it: the
	// update server, the clock and the notices
	home  = arrserver.New()
	proxy *testenv.Proxy
)

// The ports the fake sites and applications listen on when the environment
// names none.
const (
	defaultIndexerPort = 19691
	defaultAppPort     = 19692
)

// runSuite starts the proxy Prowlarr reaches the world through, connects, seeds the fixtures and runs the tests, answering the exit code.
func runSuite(m *testing.M) int {
	if !testenv.Configured() {
		return m.Run() // every test skips
	}
	p, err := testenv.StartProxy(context.Background(), home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proxy:", err)
		return 1
	}
	proxy = p
	// the trackers the catalogue's own indexers are pointed at are sites the
	// suite has no fake of: they are down, which is what the audits of
	// failing indexers are given to find
	for _, site := range []string{currentURL1337x, retiredURL1337x, unlistedURLMilki} {
		u, err := url.Parse(site)
		if err != nil {
			fmt.Fprintln(os.Stderr, "acceptance/tools setup:", err)
			return 1
		}
		proxy.Serve(u.Hostname(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "the site is down", http.StatusServiceUnavailable)
		}))
	}
	if err := start(); err != nil {
		stop()
		fmt.Fprintln(os.Stderr, "acceptance/tools setup:", err)
		return 1
	}

	code := m.Run()
	stop()

	// a request Prowlarr made that the suite does not answer is said loudly
	// even when the assertions happened to survive it: it went unanswered,
	// and Prowlarr's health checks stop at the first that cannot be answered
	report := proxy.Report() + home.Report()
	if report != "" {
		fmt.Fprint(os.Stderr, report)
		if code == 0 {
			code = 1
		}
	}

	// a key shown in any answer fails the run, whole or filtered
	if leaks := suite.LeakReport(); leaks != "" {
		fmt.Fprint(os.Stderr, "\n"+leaks)
		code = 1
	}

	// every registered tool must have answered something above, not only
	// refused it. Only a whole-suite run can say that, so a -run or -skip
	// filter skips the check.
	if acctest.WholeRun() {
		report, err := suite.CoverageReport()
		if err != nil {
			fmt.Fprintln(os.Stderr, "\n"+err.Error())
			code = 1
		}
		if report != "" {
			fmt.Fprint(os.Stderr, report)
			code = 1
		}
	}

	return code
}

// start runs the fakes, connects an MCP session to the tools, and seeds the
// fixtures.
func start() error {
	indexerPort, err := testenv.IndexerPort(defaultIndexerPort)
	if err != nil {
		return err
	}
	appPort, err := testenv.AppPort(defaultAppPort)
	if err != nil {
		return err
	}
	sitesUp, err = newznab.New(newznab.Options{Addr: ":" + strconv.Itoa(indexerPort), PublicHost: testenv.Host(), Sites: sites()})
	if err != nil {
		return err
	}
	appsUp, err = servarr.New(servarr.Options{Addr: ":" + strconv.Itoa(appPort), PublicHost: testenv.Host(), Apps: fakeApps})
	if err != nil {
		return err
	}
	if sdk, err = prowlarr.New(testenv.Server(), testenv.Token()); err != nil {
		return err
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "prowlarr-mcp", Version: "test"}, nil)
	if _, err := tools.RegisterAll(srv, sdk, tools.Options{EnableDelete: true}); err != nil {
		return err
	}
	connected, err := acctest.Connect(context.Background(), srv)
	if err != nil {
		return err
	}
	connected.NotReady = testenv.NotConfigured
	suite = connected
	// every key the suite gives Prowlarr, and Prowlarr's own: nothing a
	// tool answers or refuses with may carry one (LeakReport, in testMain)
	suite.Secret(testenv.Token())
	for _, key := range siteKeys {
		suite.Secret(key)
	}
	for _, key := range []string{"sonarrkey", "radarrkey", "lidarrkey", "milkiekey"} {
		suite.Secret(key)
	}

	return seed()
}

func stop() {
	if sitesUp != nil {
		_ = sitesUp.Close()
	}
	if appsUp != nil {
		_ = appsUp.Close()
	}
	if err := proxy.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// seed builds the fixtures through the tools. It is idempotent: what already
// exists is left alone, so the suite can be re-run against a container that
// is still up.
func seed() error {
	// Prowlarr checks its health as it starts, before the suite's proxy is
	// there to answer its update check, and a check that cannot be answered
	// ends the run of them: so they are run again now that it can
	if _, err := suite.Invoke("task_run", map[string]any{"name": "CheckHealth"}); err != nil {
		return err
	}

	have := map[string]bool{}
	list, err := suite.Invoke("indexer_list", nil)
	if err != nil {
		return err
	}
	for _, row := range acctest.RowsOf(list["indexers"]) {
		have[acctest.Str(row["name"])] = true
	}

	for _, label := range []string{tagStray} {
		if _, err := suite.Invoke("tag_create", map[string]any{"label": label}); err != nil && !strings.Contains(err.Error(), "already exists") {
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
			"definition": def, "name": g.Name, "base_url": sitesUp.Indexer(g.Site).URL(),
			"settings": map[string]any{"apiKey": siteKeys[g.Site]},
		}
		if len(g.Tags) > 0 {
			args["tags"] = g.Tags
		}
		if _, err := suite.Invoke("indexer_add", args); err != nil {
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
		if have[acctest.Str(c["definition"])] {
			continue
		}
		if _, err := suite.Invoke("indexer_add", c); err != nil {
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
		if _, err := suite.Invoke(step.tool, step.args); err != nil {
			return err
		}
	}
	for site, f := range brokenSites {
		sitesUp.Indexer(site).SetFailure(f)
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
	list, err := suite.Invoke("app_list", nil)
	if err != nil {
		return err
	}
	for _, row := range acctest.RowsOf(list["apps"]) {
		have[acctest.Str(row["name"])] = true
	}
	for _, a := range []map[string]any{
		{"kind": "Sonarr", "base_url": appsUp.URL("sonarr"), "api_key": "sonarrkey", "prowlarr_url": "http://" + testenv.Container() + ":9696"},
		// told to reach Prowlarr at localhost: what audit_apps finds
		{"kind": "Radarr", "base_url": appsUp.URL("radarr"), "api_key": "radarrkey", "prowlarr_url": "http://localhost:9696", "tags": []string{tagMovies}},
		// not served at all, and tagged for indexers that do not exist
		{"kind": "Lidarr", "base_url": appsUp.URL("lidarr"), "api_key": "lidarrkey", "prowlarr_url": "http://prowlarr:9696", "tags": []string{tagMusicOnly}, "force": true},
	} {
		name := acctest.Str(a["kind"])
		if have[name] {
			continue
		}
		if _, err := suite.Invoke("app_add", a); err != nil {
			return fmt.Errorf("adding %s: %w", name, err)
		}
	}

	return nil
}

func seedProviders() error {
	profiles, err := suite.Invoke("profile_list", nil)
	if err != nil {
		return err
	}
	haveProfile := map[string]bool{}
	for _, row := range acctest.RowsOf(profiles["profiles"]) {
		haveProfile[acctest.Str(row["name"])] = true
	}
	for _, p := range []map[string]any{
		{"name": profileNothing, "rss": false, "automatic_search": false, "interactive_search": false},
		{"name": profileSpare},
	} {
		if haveProfile[acctest.Str(p["name"])] {
			continue
		}
		if _, err := suite.Invoke("profile_create", p); err != nil {
			return err
		}
	}

	proxies, err := suite.Invoke("proxy_list", nil)
	if err != nil {
		return err
	}
	haveProxy := map[string]bool{}
	for _, row := range acctest.RowsOf(proxies["proxies"]) {
		haveProxy[acctest.Str(row["name"])] = true
	}
	for _, p := range []map[string]any{
		// nothing listens there, so it fails its test: forced
		{"kind": "FlareSolverr", "name": proxyFlare, "host": "http://" + testenv.Host() + ":1", "tags": []string{tagFlaresolverr}, "force": true},
		{"kind": "Http", "name": proxyIdle, "host": "proxy.invalid", "port": 3128, "tags": []string{tagIdle}, "force": true},
	} {
		if haveProxy[acctest.Str(p["name"])] {
			continue
		}
		if _, err := suite.Invoke("proxy_add", p); err != nil {
			return err
		}
	}

	clients, err := suite.Invoke("downloadclient_list", nil)
	if err != nil {
		return err
	}
	haveClient := map[string]bool{}
	for _, row := range acctest.RowsOf(clients["download_clients"]) {
		haveClient[acctest.Str(row["name"])] = true
	}
	for _, c := range []map[string]any{
		{"kind": "TorrentBlackhole", "name": clientTorrent, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}},
		{"kind": "UsenetBlackhole", "name": clientUsenet, "settings": map[string]any{"nzbFolder": "/downloads/usenet"}},
		{"kind": "TorrentBlackhole", "name": clientSpare, "priority": 2, "settings": map[string]any{"torrentFolder": "/downloads/torrent"}},
	} {
		if haveClient[acctest.Str(c["name"])] {
			continue
		}
		if _, err := suite.Invoke("downloadclient_add", c); err != nil {
			return err
		}
	}

	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// allowedHostsNamed reports whether Prowlarr has been told which host names
// it answers to. It starts with none, which its health checks warn about; the
// settings test names them, and nothing but writing its config file by hand
// takes them away again, so a suite run again against a container still up
// finds the warning gone.
func allowedHostsNamed(t *testing.T) bool {
	t.Helper()

	host, err := sdk.GetConfigHost(suite.Ctx)
	if err != nil {
		t.Fatal(err)
	}

	return host.Answer.AllowedHosts != ""
}

// vipIn is a VIP expiry some days from today, as indexer_edit takes it. The
// day is UTC's, which is the day the audit counts from.
func vipIn(days int) string { return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02") }

// findings are an audit's findings as subject/problem pairs.
func findings(t *testing.T, out map[string]any) []string {
	t.Helper()

	rows := acctest.Rows(t, out["findings"], "findings")
	got := make([]string, 0, len(rows))
	for _, f := range rows {
		got = append(got, acctest.Str(f["subject"])+"/"+acctest.Str(f["problem"]))
	}
	slices.Sort(got)

	return got
}

// finding reports whether an audit found a problem with a subject, and its
// detail.
func finding(t *testing.T, out map[string]any, subject, problem string) (string, bool) {
	t.Helper()

	for _, f := range acctest.Rows(t, out["findings"], "findings") {
		if acctest.Str(f["subject"]) == subject && acctest.Str(f["problem"]) == problem {
			return acctest.Str(f["detail"]), true
		}
	}

	return "", false
}

// indexerRow is one indexer as indexer_list shows it.
func indexerRow(t *testing.T, name string) map[string]any {
	t.Helper()

	for _, row := range acctest.Rows(t, suite.Call(t, "indexer_list", nil)["indexers"], "indexers") {
		if acctest.Str(row["name"]) == name {
			return row
		}
	}
	t.Fatalf("no indexer %q", name)

	return nil
}

// indexerID is an indexer's id.
func indexerID(t *testing.T, name string) int {
	t.Helper()

	return acctest.Num(t, indexerRow(t, name)["id"], "id")
}

// search runs a search against some indexers, the way an application would,
// which gives them history and statistics to judge.
func search(t *testing.T, query string, indexers ...string) map[string]any {
	t.Helper()

	return suite.Call(t, "release_search", map[string]any{"query": query, "indexers": indexers})
}

// patience is how long a wait on something Prowlarr does in the background
// is given: a sync to an application, a failure recorded.
const patience = 30 * time.Second

// definitionsDir is where the container reads its tracker definitions from.
func definitionsDir() string { return filepath.Join(testenv.DataDir(), "config", "Definitions") }
