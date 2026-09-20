package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tools end to end against a canned Prowlarr: the requests a handler
// builds and the answer it projects, without a container. The live suite
// proves the canned shapes match a real Prowlarr; these pin what its fixtures
// cannot reach - a VIP already over (Prowlarr refuses to save one), a
// definition Prowlarr calls obsolete, an application added twice, a grab
// limit nearly spent - and the edges of the pure functions.

// fakeServer is a canned Prowlarr API: a fixed answer per route, plus a
// record of every request the tools made.
type fakeServer struct {
	srv *httptest.Server

	mu      sync.Mutex
	answers map[string]any
	seen    []string
	bodies  map[string][]string
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()

	f := &fakeServer{answers: map[string]any{}, bodies: map[string][]string{}}
	// what a new Prowlarr answers: nothing configured
	for _, route := range []string{
		"/api/v1/indexer", "/api/v1/indexerstatus", "/api/v1/applications", "/api/v1/indexerproxy", "/api/v1/downloadclient",
		"/api/v1/tag", "/api/v1/tag/detail", "/api/v1/health", "/api/v1/history/indexer", "/api/v1/indexer/schema", "/api/v1/notification",
	} {
		f.answers["GET "+route] = []any{}
	}
	f.answers["GET /api/v1/appprofile"] = []any{map[string]any{"id": 1, "name": "Standard", "enableRss": true, "enableAutomaticSearch": true, "enableInteractiveSearch": true, "minimumSeeders": 1}}
	f.answers["GET /api/v1/indexerstats"] = map[string]any{"indexers": []any{}, "userAgents": []any{}, "hosts": []any{}}
	f.answers["GET /api/v1/system/status"] = map[string]any{"appName": "Prowlarr", "version": "2.6.5.5623", "osName": "alpine", "isDocker": true}

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.seen = append(f.seen, key+"?"+r.URL.RawQuery)
		f.bodies[key] = append(f.bodies[key], string(body))
		answer, ok := f.answers[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		status := http.StatusOK
		switch r.Method {
		case http.MethodPost:
			status = http.StatusCreated
		case http.MethodPut:
			status = http.StatusAccepted
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

// answer sets what a route answers.
func (f *fakeServer) answer(route string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[route] = v
}

// requested reports the requests made to a route, with their queries.
func (f *fakeServer) requested(route string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seen {
		if strings.HasPrefix(s, route+"?") {
			out = append(out, s)
		}
	}

	return out
}

// sent is what was sent to a route, in order.
func (f *fakeServer) sent(route string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.bodies[route])
}

// session connects an MCP client to every tool, over the canned server.
func session(t *testing.T, f *fakeServer, opts Options) *mcp.ClientSession {
	t.Helper()

	client, err := prowlarr.New(f.srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	opts.Toolsets = []string{"all"}
	if _, err := RegisterAll(srv, client, opts); err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s: %v", name, res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: structured content is %T", name, res.StructuredContent)
	}

	return out
}

func callError(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("%s succeeded: %v", name, res.StructuredContent)
	}
	var msgs []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			msgs = append(msgs, tc.Text)
		}
	}

	return strings.Join(msgs, "; ")
}

// problems lists an audit's findings as subject/problem, sorted; found keeps
// the order the audit reported them in.
func problems(t *testing.T, out map[string]any) []string {
	t.Helper()

	got := found(t, out)
	slices.Sort(got)

	return got
}

func found(t *testing.T, out map[string]any) []string {
	t.Helper()

	raw, ok := out["findings"].([]any)
	if !ok {
		t.Fatalf("findings is %T, want a list", out["findings"])
	}
	got := make([]string, 0, len(raw))
	for _, f := range raw {
		m, ok := f.(map[string]any)
		if !ok {
			t.Fatalf("a finding is %T, want an object", f)
		}
		got = append(got, fmt.Sprint(m["subject"], "/", m["problem"]))
	}

	return got
}

func detailOf(t *testing.T, out map[string]any, subject, problem string) string {
	t.Helper()

	raw, ok := out["findings"].([]any)
	if !ok {
		t.Fatalf("findings is %T, want a list", out["findings"])
	}
	for _, f := range raw {
		m, ok := f.(map[string]any)
		if !ok {
			t.Fatalf("a finding is %T, want an object", f)
		}
		if m["subject"] == subject && m["problem"] == problem {
			return str(t, m["detail"], "detail")
		}
	}
	t.Errorf("no %s/%s in %v", subject, problem, problems(t, out))

	return ""
}

// str and num read a field of an answer, failing the test when it is not
// there or not what it should be.
func str(t *testing.T, v any, field string) string {
	t.Helper()

	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s is %T (%v), want a string", field, v, v)
	}

	return s
}

func num(t *testing.T, v any, field string) float64 {
	t.Helper()

	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%s is %T (%v), want a number", field, v, v)
	}

	return f
}

// torznab is a canned Torznab indexer.
func torznab(id int, name, privacy string, fields ...map[string]any) map[string]any { //nolint:unparam // public indexers are built by hand; the parameter keeps the field visible
	all := make([]any, 0, 1+len(fields))
	all = append(all, map[string]any{"name": "baseUrl", "value": "https://" + strings.ToLower(strings.ReplaceAll(name, " ", "")) + ".example/", "type": "textbox"})
	for _, f := range fields {
		all = append(all, f)
	}

	return map[string]any{
		"id": id, "name": name, "implementation": "Torznab", "definitionName": "Torznab", "protocol": "torrent", "privacy": privacy,
		"enable": true, "priority": 25, "appProfileId": 1, "tags": []int{}, "fields": all, "added": "2020-01-01T00:00:00Z",
		"capabilities": map[string]any{
			"categories":     []any{map[string]any{"id": 5000, "name": "TV", "subCategories": []any{map[string]any{"id": 5040, "name": "TV/HD"}}}},
			"searchParams":   []string{"q"},
			"tvSearchParams": []string{"q", "season", "ep"},
		},
	}
}

func vipField(v string) map[string]any {
	return map[string]any{"name": "vipExpiration", "value": v, "type": "textbox"}
}

func TestAuditVIPExpiredAndUnreadable(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{
		torznab(1, "Gone", "private", vipField(time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02"))),
		torznab(2, "Soon", "private", vipField(time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02"))),
		torznab(3, "Later", "private", vipField(time.Now().UTC().AddDate(0, 3, 0).Format("2006-01-02"))),
		torznab(4, "Garbled", "private", vipField("next tuesday")),
		torznab(5, "None", "private"),
	})
	out := mustCall(t, session(t, f, Options{}), "audit_vip", nil)
	if got, want := problems(t, out), []string{"Garbled/vip_unreadable", "Gone/vip_expired", "Soon/vip_expiring"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !strings.Contains(detailOf(t, out, "Gone", "vip_expired"), "10 days ago") {
		t.Errorf("expired detail = %q", detailOf(t, out, "Gone", "vip_expired"))
	}
	if out["scanned"] != float64(4) {
		t.Errorf("scanned %v, want the four with an expiry", out["scanned"])
	}
}

func TestAuditFailingHeldBack(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Broken", "private"), torznab(2, "Fine", "private")})
	f.answer("GET /api/v1/indexerstatus", []any{map[string]any{
		"indexerId": 1, "disabledTill": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"initialFailure": time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339), "mostRecentFailure": time.Now().UTC().Format(time.RFC3339),
	}})
	f.answer("GET /api/v1/history/indexer", []any{map[string]any{
		"indexerId": 1, "eventType": "indexerQuery", "successful": false, "date": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]string{"url": "https://broken.example/api?t=search&apikey=SECRET&q=x"},
	}})
	out := mustCall(t, session(t, f, Options{}), "audit_failing", nil)
	detail := detailOf(t, out, "Broken", "held_back_after_failures")
	if !strings.Contains(detail, "(3 days)") || !strings.Contains(detail, "REDACTED") || strings.Contains(detail, "SECRET") {
		t.Errorf("detail = %q", detail)
	}
	// the history answer is the same for every indexer here, so Fine's last
	// request failed too
	if got := problems(t, out); !slices.Contains(got, "Fine/last_request_failed") {
		t.Errorf("findings = %v", got)
	}
}

func stat(id int, name string, s map[string]any) map[string]any {
	s["indexerId"], s["indexerName"] = id, name
	return s
}

func TestAuditUnreliableKindsAndOrder(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Logins", "private"), torznab(2, "Grabs", "private"), torznab(3, "Fine", "private"), torznab(4, "Quiet", "private")})
	f.answer("GET /api/v1/indexerstats", map[string]any{"indexers": []any{
		stat(1, "Logins", map[string]any{"numberOfQueries": 20, "numberOfAuthQueries": 20, "numberOfFailedAuthQueries": 20}),
		stat(2, "Grabs", map[string]any{"numberOfQueries": 40, "numberOfGrabs": 10, "numberOfFailedGrabs": 10}),
		stat(3, "Fine", map[string]any{"numberOfQueries": 100, "numberOfFailedQueries": 1}),
		stat(4, "Quiet", map[string]any{"numberOfQueries": 2, "numberOfFailedQueries": 2}),
	}})
	out := mustCall(t, session(t, f, Options{}), "audit_unreliable", nil)
	order := found(t, out)
	// worst first: half of Logins' requests failed, a fifth of Grabs'; Quiet
	// has too few requests to judge, Fine too few failures
	if want := []string{"Logins/logins_failing", "Grabs/grabs_failing"}; !slices.Equal(order, want) {
		t.Errorf("findings = %v, want %v", order, want)
	}
	// the window is asked of Prowlarr, not filtered here
	if q := f.requested("GET /api/v1/indexerstats"); len(q) != 1 || !strings.Contains(q[0], "startDate=") {
		t.Errorf("stats requests = %v", q)
	}
}

func TestAuditSlowQueriesAndGrabs(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Sluggish", "private"), torznab(2, "Snappy", "private")})
	f.answer("GET /api/v1/indexerstats", map[string]any{"indexers": []any{
		stat(1, "Sluggish", map[string]any{"numberOfQueries": 50, "averageResponseTime": 9000, "numberOfGrabs": 3, "averageGrabResponseTime": 12000}),
		stat(2, "Snappy", map[string]any{"numberOfQueries": 50, "averageResponseTime": 300}),
	}})
	out := mustCall(t, session(t, f, Options{}), "audit_slow", nil)
	if got, want := problems(t, out), []string{"Sluggish/slow_grabs", "Sluggish/slow_queries"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !strings.Contains(detailOf(t, out, "Sluggish", "slow_queries"), "9.0 seconds") {
		t.Error("the detail does not give the time")
	}
}

func TestAuditUnusedAgesAndWindows(t *testing.T) {
	t.Parallel()

	young := torznab(3, "Young", "private")
	young["added"] = time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339)
	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Idle", "private"), torznab(2, "Busy", "private"), young})
	f.answer("GET /api/v1/indexerstats", map[string]any{"indexers": []any{
		stat(2, "Busy", map[string]any{"numberOfQueries": 50}),
	}})
	out := mustCall(t, session(t, f, Options{}), "audit_unused", nil)
	if got, want := problems(t, out), []string{"Busy/never_grabbed", "Idle/never_queried"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	// with no application, the one never queried reaches none
	if !strings.Contains(detailOf(t, out, "Idle", "never_queried"), "no application receives it") {
		t.Error("the detail does not say it reaches no application")
	}
	if !strings.Contains(str(t, out["note"], "note"), "1 indexers added within the last 7 days") {
		t.Errorf("note = %v", out["note"])
	}
	// judged from the day it was added, once it is old enough
	out = mustCall(t, session(t, f, Options{}), "audit_unused", map[string]any{"min_age_days": 1})
	if !strings.Contains(detailOf(t, out, "Young", "never_queried"), "in 2 days") {
		t.Errorf("young detail = %q", detailOf(t, out, "Young", "never_queried"))
	}
}

func TestAuditLimitsNearAndHourly(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{
		torznab(1, "Capped", "private",
			map[string]any{"name": "baseSettings.queryLimit", "value": 10, "type": "number"},
			map[string]any{"name": "baseSettings.grabLimit", "value": 5, "type": "number"},
			map[string]any{"name": "baseSettings.limitsUnit", "value": 1, "type": "select"}),
		torznab(2, "Unlimited", "private"),
	})
	// RSS syncs count towards the query limit; 4 of 5 grabs is near, and
	// the second indexer's 5 of 5 is reached
	f.answer("GET /api/v1/indexerstats", map[string]any{"indexers": []any{
		stat(1, "Capped", map[string]any{"numberOfQueries": 6, "numberOfRssQueries": 3, "numberOfGrabs": 4}),
	}})
	cs := session(t, f, Options{})
	out := mustCall(t, cs, "audit_limits", nil)
	if got, want := problems(t, out), []string{"Capped/grab_limit_near", "Capped/query_limit_near"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if d := detailOf(t, out, "Capped", "query_limit_near"); !strings.Contains(d, "9 of 10 queries in the last hour (90%)") {
		t.Errorf("detail = %q", d)
	}
	if d := detailOf(t, out, "Capped", "grab_limit_near"); !strings.Contains(d, "4 of 5 grabs in the last hour (80%)") {
		t.Errorf("grab detail = %q", d)
	}
	if out["scanned"] != float64(1) {
		t.Errorf("scanned %v, want the one with a limit", out["scanned"])
	}

	// at the limit, both are reached rather than near, and a day's limit
	// says so
	f.answer("GET /api/v1/indexer", []any{
		torznab(1, "Capped", "private",
			map[string]any{"name": "baseSettings.queryLimit", "value": 9, "type": "number"},
			map[string]any{"name": "baseSettings.grabLimit", "value": 4, "type": "number"}),
	})
	out = mustCall(t, session(t, f, Options{}), "audit_limits", nil)
	if got, want := problems(t, out), []string{"Capped/grab_limit_reached", "Capped/query_limit_reached"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if d := detailOf(t, out, "Capped", "grab_limit_reached"); !strings.Contains(d, "4 of 4 grabs in the last day") {
		t.Errorf("reached detail = %q", d)
	}
}

func app(id int, name, kind, base, prowlarrURL, level string, cats, tags []int) map[string]any {
	return map[string]any{
		"id": id, "name": name, "implementation": kind, "syncLevel": level, "tags": tags,
		"fields": []any{
			map[string]any{"name": "baseUrl", "value": base},
			map[string]any{"name": "prowlarrUrl", "value": prowlarrURL},
			map[string]any{"name": "apiKey", "value": "********", "privacy": "apiKey"},
			map[string]any{"name": "syncCategories", "value": cats},
		},
	}
}

func TestAuditAppsCanned(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/applications", []any{
		app(1, "Sonarr", "Sonarr", "http://sonarr:8989", "http://prowlarr:9696", "fullSync", []int{5000}, nil),
		app(2, "Sonarr Again", "Sonarr", "http://sonarr:8989/", "http://prowlarr:9696", "fullSync", []int{5000}, nil),
		app(3, "Paused", "Radarr", "http://radarr:7878", "http://prowlarr:9696", "disabled", []int{2000}, nil),
		app(4, "Empty", "Radarr", "http://radarr4k:7878", "http://prowlarr:9696", "fullSync", []int{}, nil),
		// on Prowlarr's own host, so localhost is right
		app(5, "Local", "Lidarr", "http://localhost:8686", "http://localhost:9696", "fullSync", []int{3000}, nil),
	})
	f.answer("GET /api/v1/health", []any{map[string]any{"source": "ApplicationStatusCheck", "type": "warning", "message": "Applications unavailable due to failures: Paused"}})
	want := []string{"Empty/no_sync_categories", "Paused/failing", "Paused/sync_disabled", "Sonarr Again/duplicate"}
	if got := problems(t, mustCall(t, session(t, f, Options{}), "audit_apps", nil)); !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
}

func TestAuditDefinitionsCanned(t *testing.T) {
	t.Parallel()

	cardigann := func(id int, name, file, base string, urls, legacy []string) map[string]any {
		return map[string]any{
			"id": id, "name": name, "implementation": "Cardigann", "definitionName": file, "protocol": "torrent", "privacy": "public",
			"enable": true, "indexerUrls": urls, "legacyUrls": legacy,
			"fields": []any{
				map[string]any{"name": "definitionFile", "value": file},
				map[string]any{"name": "baseUrl", "value": base},
			},
		}
	}
	old := cardigann(1, "Old Site", "oldsite", "https://old.example/", []string{"https://old.example/"}, nil)
	old["message"] = map[string]any{"message": "This indexer is deprecated", "type": "warning"}
	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{
		old,
		cardigann(2, "Moved", "moved", "https://moved.example/", []string{"https://moved.new/"}, []string{"https://moved.example/"}),
		cardigann(3, "Vanished", "vanished", "https://vanished.example/", []string{"https://vanished.example/"}, nil),
		cardigann(4, "Mirror", "mirror", "https://mirror.other/", []string{"https://mirror.example/"}, nil),
	})
	f.answer("GET /api/v1/indexer/schema", []any{
		map[string]any{"name": "Old Site", "implementation": "Cardigann", "definitionName": "oldsite"},
		map[string]any{"name": "Moved", "implementation": "Cardigann", "definitionName": "moved"},
		map[string]any{"name": "Mirror", "implementation": "Cardigann", "definitionName": "mirror"},
	})
	f.answer("GET /api/v1/health", []any{map[string]any{"source": "OutdatedDefinitionCheck", "type": "warning", "message": "Indexers are obsolete or have been updated: Old Site. Obsolete and/or changed indexers"}})
	out := mustCall(t, session(t, f, Options{}), "audit_definitions", nil)
	want := []string{"Mirror/unlisted_url", "Moved/retired_url", "Old Site/definition_notice", "Old Site/definition_obsolete", "Vanished/definition_gone"}
	if got := problems(t, out); !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !strings.Contains(detailOf(t, out, "Moved", "retired_url"), "https://moved.new/") {
		t.Error("the retired URL's finding does not name the current one")
	}
}

func TestAuditDownloadClientsCanned(t *testing.T) {
	t.Parallel()

	pinned := torznab(1, "Pinned", "private")
	pinned["downloadClientId"] = 9
	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{pinned})
	f.answer("GET /api/v1/downloadclient", []any{
		map[string]any{"id": 1, "name": "Hole", "implementation": "UsenetBlackhole", "enable": true},
		map[string]any{"id": 2, "name": "Off Hole", "implementation": "UsenetBlackhole", "enable": false},
		map[string]any{"id": 3, "name": "SAB", "implementation": "Sabnzbd", "enable": true},
	})
	f.answer("GET /api/v1/health", []any{map[string]any{"source": "DownloadClientStatusCheck", "type": "warning", "message": "Download clients are unavailable due to failures: SAB"}})
	want := []string{"Hole/blackhole_takes_no_usenet", "Pinned/pinned_client_missing", "SAB/client_failing"}
	if got := problems(t, mustCall(t, session(t, f, Options{}), "audit_download_clients", nil)); !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
}

func TestAuditTagsAndProfilesCanned(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/tag/detail", []any{
		map[string]any{"id": 1, "label": "stray"},
		map[string]any{"id": 2, "label": "4k", "applicationIds": []int{7}},
		map[string]any{"id": 3, "label": "movies", "applicationIds": []int{7}, "indexerIds": []int{1}},
	})
	f.answer("GET /api/v1/applications", []any{app(7, "Radarr 4K", "Radarr", "http://r:7878", "http://p:9696", "fullSync", []int{2000}, []int{2, 3})})
	f.answer("GET /api/v1/indexer", []any{torznab(1, "Films", "private")})
	cs := session(t, f, Options{})
	if got, want := problems(t, mustCall(t, cs, "audit_tags", nil)), []string{"4k/app_tag_matches_nothing", "stray/tag_unused"}; !slices.Equal(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}

	f.answer("GET /api/v1/appprofile", []any{
		map[string]any{"id": 1, "name": "Standard", "enableRss": true, "enableAutomaticSearch": true, "enableInteractiveSearch": true},
		map[string]any{"id": 2, "name": "Off", "enableRss": false, "enableAutomaticSearch": false, "enableInteractiveSearch": false},
	})
	if got, want := problems(t, mustCall(t, cs, "audit_profiles", nil)), []string{"Off/profile_unused", "Off/profile_uses_nothing"}; !slices.Equal(got, want) {
		t.Errorf("profiles = %v, want %v", got, want)
	}
}

// audit_all runs every audit over one read of Prowlarr and adds them up.
func TestAuditAllCanned(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "A", "private"), torznab(2, "A Copy", "private")})
	f.answer("GET /api/v1/indexer/schema", []any{})
	out := mustCall(t, session(t, f, Options{}), "audit_all", nil)
	rows, ok := out["audits"].([]any)
	if !ok || len(rows) != 16 {
		t.Fatalf("audits = %v", out["audits"])
	}
	counts := map[string]float64{}
	var total float64
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("an audit is %T, want an object", r)
		}
		name := str(t, m["audit"], "audit")
		counts[name] = num(t, m["findings"], "findings")
		total += counts[name]
	}
	// the two share a base URL on a generic definition; both lack seed goals
	if counts["audit_duplicates"] != 0 || counts["audit_seeding"] != 2 || out["total_findings"] != total {
		t.Errorf("counts = %v, total %v", counts, out["total_findings"])
	}
	// one read of the indexers for the lot, not one per audit
	if n := len(f.requested("GET /api/v1/indexer")); n != 1 {
		t.Errorf("the indexers were read %d times", n)
	}
}

func TestServerInfoReportsItsOwnBuild(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/indexer", []any{torznab(1, "On", "private"), map[string]any{"id": 2, "name": "Off", "enable": false, "protocol": "usenet"}})
	f.answer("GET /api/v1/indexerstatus", []any{map[string]any{"indexerId": 1, "disabledTill": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}})
	f.answer("GET /api/v1/health", []any{
		map[string]any{"source": "AllowedHostsCheck", "type": "warning", "message": "Allowed Hosts is not configured"},
		map[string]any{"source": "IndexerStatusCheck", "type": "error", "message": "Indexers unavailable due to failures: On"},
	})
	out := mustCall(t, session(t, f, Options{}), "server_info", nil)
	if out["version"] != "2.6.5.5623" || out["indexers"] != float64(2) || out["indexers_enabled"] != float64(1) || out["indexers_failing"] != float64(1) {
		t.Errorf("server_info = %v", out)
	}
	// errors before warnings, each naming what deals with it
	health, ok := out["health"].([]any)
	if !ok || len(health) == 0 {
		t.Fatalf("health = %v", out["health"])
	}
	first, ok := health[0].(map[string]any)
	if !ok {
		t.Fatalf("a health check is %T, want an object", health[0])
	}
	if first["source"] != "IndexerStatusCheck" || !strings.Contains(str(t, first["fix"], "fix"), "audit_failing") {
		t.Errorf("health = %v", health)
	}
	// the same string the version command prints and the MCP handshake
	// carries: stamped at build time, or the module version, or "dev"
	if got := str(t, out["prowlarr_mcp_version"], "prowlarr_mcp_version"); got == "" || got != version.Version {
		t.Errorf("prowlarr_mcp_version = %q, want %q", got, version.Version)
	}
}

// indexer_edit sends back the whole indexer with only what was asked
// changed, and a setting that is not there is refused before anything is
// sent.
func TestIndexerEditSendsTheWholeIndexer(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	idx := torznab(4, "Edit Me", "private",
		map[string]any{"name": "apiKey", "value": "********", "privacy": "apiKey", "type": "textbox"},
		map[string]any{"name": "torrentBaseSettings.seedRatio", "value": nil, "type": "number"},
		map[string]any{"name": "baseSettings.limitsUnit", "value": 0, "type": "select", "selectOptions": []any{map[string]any{"value": 0, "name": "Day"}, map[string]any{"value": 1, "name": "Hour"}}},
	)
	f.answer("GET /api/v1/indexer", []any{idx})
	f.answer("PUT /api/v1/indexer/4", idx)
	cs := session(t, f, Options{})

	mustCall(t, cs, "indexer_edit", map[string]any{"indexer": "edit me", "priority": 10, "seed_ratio": 1.5, "settings": map[string]any{"baseSettings.limitsUnit": "hour"}})
	sent := f.sent("PUT /api/v1/indexer/4")
	if len(sent) != 1 {
		t.Fatalf("sent %d updates", len(sent))
	}
	var got prowlarr.IndexerResource
	if err := json.Unmarshal([]byte(sent[0]), &got); err != nil {
		t.Fatal(err)
	}
	ratio, _ := fieldNumber(got.Fields, "torrentBaseSettings.seedRatio")
	unit, _ := fieldNumber(got.Fields, "baseSettings.limitsUnit")
	if got.Priority != 10 || ratio != 1.5 || unit != 1 || fieldString(got.Fields, "apiKey") != "********" || got.Name != "Edit Me" {
		t.Errorf("sent %s", sent[0])
	}
	if q := f.requested("PUT /api/v1/indexer/4"); !strings.Contains(q[0], "forceSave=false") {
		t.Errorf("the update was not asked to test: %v", q)
	}

	if msg := callError(t, cs, "indexer_edit", map[string]any{"indexer": "Edit Me", "settings": map[string]any{"seedRatio": 2}}); !strings.Contains(msg, "torrentBaseSettings.seedRatio") {
		t.Errorf("an unknown setting = %q", msg)
	}
	if msg := callError(t, cs, "indexer_edit", map[string]any{"indexer": "Edit Me", "priority": 99}); !strings.Contains(msg, "1 to 50") {
		t.Errorf("a priority out of range = %q", msg)
	}
	if n := len(f.sent("PUT /api/v1/indexer/4")); n != 1 {
		t.Errorf("a refused edit was sent anyway (%d updates)", n)
	}
}

// A forced add creates the indexer disabled, which Prowlarr does not test,
// then enables it with a forced update, which it does not test either.
func TestIndexerAddForced(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	entry := torznab(0, "Generic Torznab", "private")
	entry["added"] = "0001-01-01T00:00:00Z"
	f.answer("GET /api/v1/indexer/schema", []any{entry})
	made := torznab(7, "Generic Torznab", "private")
	made["enable"], made["added"] = false, "2026-09-19T12:00:00Z"
	f.answer("POST /api/v1/indexer", made)
	f.answer("PUT /api/v1/indexer/7", torznab(7, "Generic Torznab", "private"))
	cs := session(t, f, Options{})

	mustCall(t, cs, "indexer_add", map[string]any{"definition": "Generic Torznab", "base_url": "https://down.example/", "force": true})
	var posted, put prowlarr.IndexerResource
	_ = json.Unmarshal([]byte(f.sent("POST /api/v1/indexer")[0]), &posted)
	_ = json.Unmarshal([]byte(f.sent("PUT /api/v1/indexer/7")[0]), &put)
	if boolv(posted.Enable) || !boolv(put.Enable) || put.Id != 7 || put.Added != "2026-09-19T12:00:00Z" {
		t.Errorf("posted enable=%v, put enable=%v id=%d added=%q", boolv(posted.Enable), boolv(put.Enable), put.Id, put.Added)
	}
	if q := f.requested("PUT /api/v1/indexer/7"); !strings.Contains(q[0], "forceSave=true") {
		t.Errorf("the enabling update was not forced: %v", q)
	}
}
