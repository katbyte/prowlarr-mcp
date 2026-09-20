package tools

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/client"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
)

// The rules syncs works out which indexers reach which application by, one
// at a time, and the sentence whyNothing makes of them. The live suite checks
// them against what Prowlarr really pushes; these pin each rule's edge.
func TestSyncsRules(t *testing.T) {
	t.Parallel()

	cats := func(ids ...int) *prowlarr.IndexerCapabilityResource {
		c := &prowlarr.IndexerCapabilityResource{SearchParams: []prowlarr.SearchParam{"q"}}
		for _, id := range ids {
			c.Categories = append(c.Categories, prowlarr.IndexerCategory{Id: id / 1000 * 1000, SubCategories: []prowlarr.IndexerCategory{{Id: id}}})
		}
		return c
	}
	sonarr := prowlarr.ApplicationResource{Name: "Sonarr", Implementation: "Sonarr", SyncLevel: prowlarr.ApplicationSyncLevelFullSync, Fields: []prowlarr.Field{
		{Name: "syncCategories", Value: []any{float64(5000), float64(5040)}}, {Name: "animeSyncCategories", Value: []any{float64(5070)}},
	}}
	tags := map[int]string{1: "tv", 2: "films"}
	tvOnly := &prowlarr.IndexerCapabilityResource{TvSearchParams: []prowlarr.TvSearchParam{"q"}, Categories: []prowlarr.IndexerCategory{{Id: 5000}}}
	musicOnly := &prowlarr.IndexerCapabilityResource{MusicSearchParams: []prowlarr.MusicSearchParam{"q"}, Categories: []prowlarr.IndexerCategory{{Id: 5000}}}

	for _, c := range []struct {
		name string
		app  func(a *prowlarr.ApplicationResource)
		idx  prowlarr.IndexerResource
		want string // the rule failed, "" for synced
	}{
		{"a TV indexer", nil, prowlarr.IndexerResource{Name: "tv", Enable: new(true), Capabilities: cats(5040)}, ""},
		{"only a subcategory it syncs", nil, prowlarr.IndexerResource{Name: "hd", Enable: new(true), Capabilities: cats(5040)}, ""},
		{"anime by the anime categories", nil, prowlarr.IndexerResource{Name: "anime", Enable: new(true), Capabilities: cats(5070)}, ""},
		{"TV search without a plain one", nil, prowlarr.IndexerResource{Name: "tvs", Enable: new(true), Capabilities: tvOnly}, ""},
		{"films", nil, prowlarr.IndexerResource{Name: "films", Enable: new(true), Capabilities: cats(2040)}, "no_shared_category"},
		{"music search only", nil, prowlarr.IndexerResource{Name: "music", Enable: new(true), Capabilities: musicOnly}, "no_search_type"},
		{"no capabilities", nil, prowlarr.IndexerResource{Name: "bare", Enable: new(true)}, "no_search_type"},
		{"disabled", nil, prowlarr.IndexerResource{Name: "off", Enable: new(false), Capabilities: cats(5040)}, "indexer_disabled"},
		{"a tag it lacks", func(a *prowlarr.ApplicationResource) { a.Tags = []int{1} }, prowlarr.IndexerResource{Name: "untagged", Enable: new(true), Capabilities: cats(5040)}, "no_shared_tag"},
		{"a tag it shares", func(a *prowlarr.ApplicationResource) { a.Tags = []int{1, 2} }, prowlarr.IndexerResource{Name: "tagged", Enable: new(true), Tags: []int{2}, Capabilities: cats(5040)}, ""},
		{"sync off", func(a *prowlarr.ApplicationResource) { a.SyncLevel = prowlarr.ApplicationSyncLevelDisabled }, prowlarr.IndexerResource{Name: "tv", Enable: new(true), Capabilities: cats(5040)}, "app_sync_disabled"},
	} {
		app := sonarr
		app.Fields = slices.Clone(sonarr.Fields)
		if c.app != nil {
			c.app(&app)
		}
		v := syncs(&app, &c.idx, tags)
		if v.Rule != c.want || v.Synced != (c.want == "") {
			t.Errorf("%s: %+v, want rule %q", c.name, v, c.want)
		}
		if !v.Synced && v.Reason == "" {
			t.Errorf("%s: no reason given", c.name)
		}
	}

	// a clause per rule, not per indexer
	why := whyNothing(&sonarr, map[string][]string{"no_shared_category": {"a", "b", "c", "d"}, "no_search_type": {"e"}}, tags)
	if why != "1 (e) lack the tv search it needs; 4 (a, b, c, 1 more) carry none of the categories it syncs (5000, 5040, 5070)" {
		t.Errorf("whyNothing = %q", why)
	}
}

func TestCategoryRanges(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in   []int
		want string
	}{
		{nil, "none"},
		{[]int{5000}, "5000"},
		{[]int{5040, 5000, 5010, 5020, 5030}, "5000-5040"},
		{[]int{2000, 2010, 5000, 5070}, "2000-2010, 5000, 5070"},
		{[]int{100001, 100002}, "100001-100002"},
	} {
		if got := categoryRanges(c.in); got != c.want {
			t.Errorf("categoryRanges(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{ //nolint:gosec // made-up keys: hiding them is what the test checks
		"https://site.example/api?t=search&apikey=SECRET&q=dune": "https://site.example/api?apikey=REDACTED&q=dune&t=search",
		"https://site.example/rss?passkey=abc":                   "https://site.example/rss?passkey=REDACTED",
		"https://user:pass@site.example/api?q=x":                 "https://site.example/api?q=x",
		"https://site.example/api?q=dune":                        "https://site.example/api?q=dune",
		"":                                                       "",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A health check names providers inside a sentence; a name must be matched
// whole, so "Sonarr" is not found in "Sonarr 4K".
func TestMentions(t *testing.T) {
	t.Parallel()

	msg := "Applications unavailable due to failures: Sonarr 4K, Radarr"
	for name, want := range map[string]bool{"Sonarr 4K": true, "Radarr": true, "Sonarr": false, "4K": false, "": false, "Lidarr": false} {
		if got := mentions(msg, name); got != want {
			t.Errorf("mentions(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestURLComparisons(t *testing.T) {
	t.Parallel()

	if !sameURL("http://Sonarr:8989/", "http://sonarr:8989") || sameURL("", "") || sameURL("http://a", "http://b") {
		t.Error("sameURL")
	}
	if hostOf("http://Nas:9696/prowlarr") != "nas" || hostOf("::") != "" {
		t.Error("hostOf")
	}
	// a Jackett serves many sites from one host, a path each
	a := siteAddress("http://jackett:9117/api/v2.0/indexers/1337x/results/torznab/", "/api")
	b := siteAddress("http://jackett:9117/api/v2.0/indexers/eztv/results/torznab", "/api")
	if a == b || a != siteAddress("http://JACKETT:9117/api/v2.0/indexers/1337x/results/torznab", "api/") || siteAddress("nowhere", "/api") != "" {
		t.Errorf("siteAddress: %q %q", a, b)
	}
}

func TestFieldValues(t *testing.T) {
	t.Parallel()

	fields := []prowlarr.Field{
		{Name: "baseUrl", Type: "select"},
		{Name: "torrentBaseSettings.seedRatio", Type: "number"},
		{Name: "enabled", Type: "checkbox"},
		{Name: "baseSettings.limitsUnit", Type: "select", SelectOptions: []prowlarr.SelectOption{{Value: 0, Name: "Day"}, {Value: 1, Name: "Hour"}}},
		{Name: "info_note", Type: "info"},
	}
	if err := setFields(fields, map[string]any{
		"baseUrl": "https://x/", "torrentBaseSettings.seedRatio": "1.5", "Enabled": "true", "baseSettings.limitsUnit": "hour",
	}); err != nil {
		t.Fatal(err)
	}
	on, isBool := fields[2].Value.(bool)
	if fields[0].Value != "https://x/" || fields[1].Value != 1.5 || !isBool || !on || fields[3].Value != 1 {
		t.Errorf("values = %v %v %v %v", fields[0].Value, fields[1].Value, fields[2].Value, fields[3].Value)
	}
	// the select also takes the option's number
	if err := setFields(fields, map[string]any{"baseSettings.limitsUnit": "0"}); err != nil || fields[3].Value != 0 {
		t.Errorf("by number = %v, %v", fields[3].Value, err)
	}
	for _, bad := range []map[string]any{
		{"seedRatio": 1},
		{"torrentBaseSettings.seedRatio": "lots"},
		{"enabled": "maybe"},
		{"baseSettings.limitsUnit": "Fortnight"},
	} {
		err := setFields(fields, bad)
		if err == nil {
			t.Errorf("%v accepted", bad)
		}
		// an unknown name lists the ones there are, but not the notes
		if _, unknown := bad["seedRatio"]; unknown && (err == nil || !strings.Contains(err.Error(), "torrentBaseSettings.seedRatio") || strings.Contains(err.Error(), "info_note")) {
			t.Errorf("unknown setting error = %v", err)
		}
	}
	if n, ok := fieldNumber([]prowlarr.Field{{Name: "x", Value: "3"}}, "x"); !ok || n != 3 {
		t.Error("fieldNumber of a string")
	}
	if got := fieldInts([]prowlarr.Field{{Name: "c", Value: []any{float64(2000), "x", float64(2040)}}}, "c"); !slices.Equal(got, []int{2000, 2040}) {
		t.Errorf("fieldInts = %v", got)
	}
}

// Credentials are named, never shown: the ones Prowlarr marks private, and a
// catalogue definition's own, which it marks normal.
func TestSettingsHideCredentials(t *testing.T) {
	t.Parallel()

	got, secrets := settings([]prowlarr.Field{
		{Name: "baseUrl", Value: "https://x/"},
		{Name: "apiKey", Value: "********", Privacy: prowlarr.PrivacyLevelApiKey},
		{Name: "password", Value: "hunter2", Type: "password"},
		{Name: "cookie", Value: "uid=1; pass=2"},
		{Name: "2facode", Value: "123456"},
		{Name: "username", Value: "kt"},
		{Name: "freeleech", Value: false},
		{Name: "empty", Value: ""},
		{Name: "list", Value: []any{}},
		{Name: "info_flaresolverr", Value: "a note", Type: "info"},
	})
	free, isBool := got["freeleech"].(bool)
	if len(got) != 2 || got["baseUrl"] != "https://x/" || !isBool || free {
		t.Errorf("settings = %v", got)
	}
	if want := []string{"2facode", "apiKey", "cookie", "password", "username"}; !slices.Equal(secrets, want) {
		t.Errorf("secrets = %v, want %v", secrets, want)
	}
}

func TestEditTags(t *testing.T) {
	t.Parallel()

	if got := editTags([]int{1, 2}, nil, []int{3, 1}, []int{2}, false); !slices.Equal(got, []int{1, 3}) {
		t.Errorf("add and remove = %v", got)
	}
	if got := editTags([]int{1, 2}, []int{5}, nil, nil, true); !slices.Equal(got, []int{5}) {
		t.Errorf("replace = %v", got)
	}
	if got := editTags([]int{1}, []int{}, nil, nil, true); got == nil || len(got) != 0 {
		t.Errorf("clearing = %#v, want an empty list that is sent", got)
	}
}

func TestSmallHelpers(t *testing.T) {
	t.Parallel()

	now := time.Now()
	for d, want := range map[time.Duration]string{30 * time.Minute: "30 minutes", 5 * time.Hour: "5 hours", 100 * time.Hour: "4 days"} {
		if got := sinceText(now, now.Add(-d)); got != want {
			t.Errorf("sinceText(%v) = %q, want %q", d, got, want)
		}
	}
	if sinceText(now, time.Time{}) != "for an unknown time" {
		t.Error("sinceText of nothing")
	}
	if l, err := logLevels(""); err != nil || !l["warn"] || !l["error"] || l["info"] {
		t.Errorf("default levels = %v, %v", l, err)
	}
	if l, _ := logLevels("warning"); !l["warn"] {
		t.Error("warning is warn")
	}
	if _, err := logLevels("loud"); err == nil {
		t.Error("an unknown level was accepted")
	}
	if stripMarkdownLink("[linuxserver.io](https://www.linuxserver.io/)") != "linuxserver.io" || stripMarkdownLink("plain") != "plain" {
		t.Error("stripMarkdownLink")
	}
	if parseTime("2026-09-19T12:00:00Z").IsZero() || !parseTime("never").IsZero() || parseTime("2026-09-19").IsZero() {
		t.Error("parseTime")
	}
	if percent(1, 3) != 33 || percent(1, 0) != 0 {
		t.Error("percent")
	}
	s := prowlarr.IndexerStatistics{NumberOfQueries: 8, NumberOfFailedQueries: 2, NumberOfRssQueries: 1, NumberOfGrabs: 1, NumberOfFailedGrabs: 1}
	if requests(&s) != 10 || failures(&s) != 3 || failedPercent(&s) != 30 {
		t.Errorf("stats sums = %d %d %d", requests(&s), failures(&s), failedPercent(&s))
	}
	if definitionOf(&prowlarr.IndexerResource{Implementation: "Cardigann", Fields: []prowlarr.Field{{Name: "definitionFile", Value: "1337x"}}}) != "1337x" {
		t.Error("definitionOf a Cardigann indexer with no definition name")
	}
	if got := nonEmpty([]string{"", " ", "a"}); !slices.Equal(got, []string{"a"}) {
		t.Errorf("nonEmpty = %v", got)
	}
	if humanSize(1<<30) != "1.0 GiB" || humanSize(512) != "512 B" {
		t.Error("humanSize")
	}
	if d := waitFor(0); d != commandWait || waitFor(-1) != 0 || waitFor(5) != 5*time.Second {
		t.Error("waitFor")
	}
}

func TestSortReleases(t *testing.T) {
	t.Parallel()

	seeders := func(n int) *int { return &n }
	rows := []releaseRow{
		{Title: "usenet", AgeHours: 1, Size: 3},
		{Title: "few", Seeders: seeders(2), AgeHours: 5, Size: 1, Grabs: 9},
		{Title: "many", Seeders: seeders(90), AgeHours: 9, Size: 2},
	}
	order := func() []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Title)
		}

		return out
	}
	for by, want := range map[string][]string{
		"":      {"many", "few", "usenet"},
		"age":   {"usenet", "few", "many"},
		"size":  {"usenet", "many", "few"},
		"grabs": {"few", "usenet", "many"},
	} {
		if err := sortReleases(rows, by); err != nil {
			t.Fatal(err)
		}
		if got := order(); !slices.Equal(got, want) {
			t.Errorf("sort %q = %v, want %v", by, got, want)
		}
	}
	if err := sortReleases(rows, "colour"); err == nil {
		t.Error("an unknown sort was accepted")
	}
}

// A forced add creates what it adds switched off, then switches it on with
// an update; an unforced one only creates.
func TestForcedAdd(t *testing.T) {
	t.Parallel()

	var steps []string
	on := func(v bool) { steps = append(steps, map[bool]string{true: "on", false: "off"}[v]) }
	create := func() (*int, error) { steps = append(steps, "create"); return new(1), nil }
	update := func(made *int) (*int, error) { steps = append(steps, "update"); return made, nil }

	if _, err := forcedAdd(true, on, create, update); err != nil || !slices.Equal(steps, []string{"off", "create", "on", "update"}) {
		t.Errorf("forced = %v, %v", steps, err)
	}
	steps = nil
	if _, err := forcedAdd(false, on, create, update); err != nil || !slices.Equal(steps, []string{"create"}) {
		t.Errorf("unforced = %v, %v", steps, err)
	}
	steps = nil
	if _, err := forcedAdd(true, on, func() (*int, error) { return nil, errors.New("refused") }, update); err == nil || slices.Contains(steps, "update") {
		t.Errorf("a failed create went on to update: %v", steps)
	}
}

// Testing every provider answers 400 with the results when any fails; that
// is an answer, and a failed single test is a row, not an error.
func TestTestAnswers(t *testing.T) {
	t.Parallel()

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`[{"id":1,"isValid":true},{"id":2,"isValid":false,"validationFailures":[{"propertyName":"BaseUrl","errorMessage":"Unable to connect"}]}]`))}
	results, err := testAllAnswer(resp, nil, &client.StatusError{StatusCode: http.StatusBadRequest})
	if err != nil || len(results) != 2 {
		t.Fatalf("400 with results = %v, %v", results, err)
	}
	rows := testResults(results, map[int]string{1: "b", 2: "a"})
	if rows[0].Name != "a" || rows[0].OK || rows[0].Problems[0] != "BaseUrl: Unable to connect" || !rows[1].OK {
		t.Errorf("rows = %+v", rows)
	}
	if _, err := testAllAnswer(nil, nil, &client.StatusError{StatusCode: http.StatusInternalServerError}); err == nil {
		t.Error("a 500 was taken for an answer")
	}

	row, err := testOne("x", &client.StatusError{StatusCode: http.StatusBadRequest, Messages: []string{"ApiKey: Invalid"}})
	if err != nil || row.OK || row.Problems[0] != "ApiKey: Invalid" {
		t.Errorf("a failed test = %+v, %v", row, err)
	}
	if _, err := testOne("x", errors.New("connection refused")); err == nil {
		t.Error("a connection error was taken for a failed test")
	}
	if row, err := testOne("x", nil); err != nil || !row.OK {
		t.Error("a passed test")
	}
}
