package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/client"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// indexerRow is an indexer as the listings show it.
type indexerRow struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Definition   string   `json:"definition"              jsonschema:"the tracker definition it was added from, e.g. 1337x, or Newznab and Torznab for a generic one"`
	Protocol     string   `json:"protocol"                jsonschema:"usenet or torrent"`
	Privacy      string   `json:"privacy"                 jsonschema:"public, semiPrivate or private"`
	Enabled      bool     `json:"enabled"`
	Priority     int      `json:"priority"                jsonschema:"1 to 50, lower is preferred by the applications; 25 is the default"`
	Tags         []string `json:"tags"`
	SyncProfile  string   `json:"sync_profile"            jsonschema:"the sync profile deciding whether the applications use it for RSS, automatic and interactive search"`
	BaseURL      string   `json:"base_url,omitempty"`
	FailingUntil string   `json:"failing_until,omitempty" jsonschema:"Prowlarr has disabled it after failures until then"`
}

// lookups are the names a projection needs, read once per call.
type lookups struct {
	tags     map[int]string
	profiles map[int]string
	failing  map[int]prowlarr.IndexerStatusResource
}

func (r *registry) lookups(ctx context.Context) (*lookups, error) {
	tags, err := r.tagLabels(ctx)
	if err != nil {
		return nil, err
	}
	profiles, err := r.profileNames(ctx)
	if err != nil {
		return nil, err
	}
	failing, err := r.failingNow(ctx)
	if err != nil {
		return nil, err
	}

	return &lookups{tags: tags, profiles: profiles, failing: failing}, nil
}

func (l *lookups) indexerRow(i *prowlarr.IndexerResource) indexerRow {
	row := indexerRow{
		ID: i.Id, Name: i.Name, Definition: definitionOf(i), Protocol: string(i.Protocol), Privacy: string(i.Privacy),
		Enabled: boolv(i.Enable), Priority: i.Priority, Tags: labels(i.Tags, l.tags), SyncProfile: l.profiles[i.AppProfileId],
		BaseURL: fieldString(i.Fields, "baseUrl"),
	}
	if s, ok := l.failing[i.Id]; ok {
		row.FailingUntil = s.DisabledTill
	}

	return row
}

// definitionOf names the definition an indexer was added from: the Cardigann
// definition file, or the implementation for a native one.
func definitionOf(i *prowlarr.IndexerResource) string {
	if i.DefinitionName != "" {
		return i.DefinitionName
	}
	if f := fieldString(i.Fields, "definitionFile"); f != "" {
		return f
	}

	return i.Implementation
}

// indexerMatches applies the listing filters.
type indexerFilter struct {
	Name     string `json:"name,omitempty"     jsonschema:"only indexers whose name contains this"`
	Protocol string `json:"protocol,omitempty" jsonschema:"usenet or torrent"`
	Privacy  string `json:"privacy,omitempty"  jsonschema:"public, semiPrivate or private"`
	Enabled  *bool  `json:"enabled,omitempty"  jsonschema:"true for only the enabled ones, false for only the disabled ones"`
	Tag      string `json:"tag,omitempty"      jsonschema:"only indexers carrying this tag"`
	Failing  bool   `json:"failing,omitempty"  jsonschema:"only the ones Prowlarr has disabled after failures"`
}

func (f *indexerFilter) keep(row *indexerRow) bool {
	switch {
	case f.Name != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(f.Name)):
	case f.Protocol != "" && !strings.EqualFold(f.Protocol, row.Protocol):
	case f.Privacy != "" && !strings.EqualFold(f.Privacy, row.Privacy):
	case f.Enabled != nil && *f.Enabled != row.Enabled:
	case f.Tag != "" && !slices.ContainsFunc(row.Tags, func(t string) bool { return strings.EqualFold(t, f.Tag) }):
	case f.Failing && row.FailingUntil == "":
	default:
		return true
	}

	return false
}

func registerIndexerTools(r *registry) {
	pc := r.client

	type listOut struct {
		Total    int          `json:"total"    jsonschema:"indexers matching the filters"`
		Enabled  int          `json:"enabled"  jsonschema:"of those, how many are enabled"`
		Indexers []indexerRow `json:"indexers"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "indexer_list",
		Description: "Prowlarr's indexers: each one's definition, usenet or torrent, public or private, whether it is enabled, its priority, tags, sync profile and URL, and whether Prowlarr has disabled it after failures. Filter by name, protocol, privacy, tag, enabled or failing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in indexerFilter) (*mcp.CallToolResult, listOut, error) {
		all, err := r.indexers(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		l, err := r.lookups(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{}
		for i := range all {
			row := l.indexerRow(&all[i])
			if !in.keep(&row) {
				continue
			}
			out.Total++
			if row.Enabled {
				out.Enabled++
			}
			out.Indexers = append(out.Indexers, row)
		}
		slices.SortFunc(out.Indexers, func(a, b indexerRow) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })

		return nil, out, nil
	})

	type appSync struct {
		App    string `json:"app"`
		Synced bool   `json:"synced"`
		Why    string `json:"why_not,omitempty"`
	}
	type statusOut struct {
		DisabledTill      string `json:"disabled_till,omitempty"`
		InitialFailure    string `json:"initial_failure,omitempty"`
		MostRecentFailure string `json:"most_recent_failure,omitempty"`
	}
	type statsOut struct {
		Days              int `json:"days"`
		Queries           int `json:"queries"`
		FailedQueries     int `json:"failed_queries"`
		RSSQueries        int `json:"rss_queries"`
		FailedRSSQueries  int `json:"failed_rss_queries"`
		AuthQueries       int `json:"auth_queries"`
		FailedAuthQueries int `json:"failed_auth_queries"`
		Grabs             int `json:"grabs"`
		FailedGrabs       int `json:"failed_grabs"`
		AvgResponseMs     int `json:"avg_response_ms"`
		AvgGrabResponseMs int `json:"avg_grab_response_ms"`
	}
	type getOut struct {
		indexerRow
		Implementation string          `json:"implementation"`
		Description    string          `json:"description,omitempty"`
		Language       string          `json:"language,omitempty"`
		InfoLink       string          `json:"info_link,omitempty"`
		Redirect       bool            `json:"redirect"                  jsonschema:"the applications download releases straight from the indexer rather than through Prowlarr"`
		IndexerURLs    []string        `json:"indexer_urls"              jsonschema:"the addresses the definition knows the site by; base_url should be one of them"`
		LegacyURLs     []string        `json:"legacy_urls"               jsonschema:"addresses the site used to have; a base_url among them needs moving"`
		Settings       map[string]any  `json:"settings"                  jsonschema:"everything but the credentials"`
		SecretsSet     []string        `json:"secrets_set"               jsonschema:"the credentials that are set, by name, without their values"`
		Categories     []int           `json:"categories"                jsonschema:"top-level categories it carries: 1000 console, 2000 movies, 3000 audio, 4000 PC, 5000 TV, 6000 XXX, 7000 books, 8000 other"`
		Searches       []string        `json:"searches"                  jsonschema:"the kinds of search it offers: search, tv, movie, music, book"`
		DownloadClient string          `json:"download_client,omitempty" jsonschema:"the download client Prowlarr's own grabs from this indexer go to, when pinned"`
		Proxies        []string        `json:"proxies"                   jsonschema:"the proxies its requests go through, those sharing one of its tags"`
		Apps           []appSync       `json:"apps"                      jsonschema:"which applications receive it, and why not for those that do not"`
		Status         *statusOut      `json:"status,omitempty"          jsonschema:"its failure record, when it has one"`
		Stats          statsOut        `json:"stats"                     jsonschema:"its traffic over the last 30 days"`
		Recent         []historyRow    `json:"recent"                    jsonschema:"its last ten queries and grabs, newest first"`
		Message        json.RawMessage `json:"message,omitempty"         jsonschema:"a notice the definition carries, such as a deprecation"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "indexer_get",
		Description: "Everything about one indexer, by name or id: its settings (credentials only named, never shown), categories and kinds of search, its URL against the ones the definition knows, which applications receive it and why not, its proxies and download client, its failure record, its last 30 days of queries, grabs and failures, and its last ten events.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Indexer string `json:"indexer" jsonschema:"the indexer's name or id"`
	},
	) (*mcp.CallToolResult, getOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, getOut{}, err
		}
		l, err := r.lookups(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		out := getOut{
			indexerRow: l.indexerRow(i), Implementation: i.Implementation, Description: i.Description, Language: i.Language,
			InfoLink: i.InfoLink, Redirect: boolv(i.Redirect), IndexerURLs: nonEmpty(i.IndexerUrls), LegacyURLs: nonEmpty(i.LegacyUrls),
			Categories: topCategories(i),
		}
		out.Settings, out.SecretsSet = settings(i.Fields)
		have := searches(i)
		for _, k := range []string{"search", "tv", "movie", "music", "book"} {
			if have[k] {
				out.Searches = append(out.Searches, k)
			}
		}
		if i.Message != nil && i.Message.Message != "" {
			out.Message, _ = json.Marshal(i.Message)
		}
		if i.DownloadClientId > 0 {
			dc, clientErr := pc.GetDownloadClientById(ctx, i.DownloadClientId)
			switch {
			case client.IsNotFound(clientErr):
				out.DownloadClient = fmt.Sprintf("id %d, which no longer exists", i.DownloadClientId)
			case clientErr != nil:
				return nil, getOut{}, clientErr
			default:
				out.DownloadClient = dc.Model.Name
			}
		}
		proxies, err := pc.GetIndexerProxy(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		for _, p := range proxies.Model {
			if slices.ContainsFunc(p.Tags, func(t int) bool { return slices.Contains(i.Tags, t) }) {
				out.Proxies = append(out.Proxies, p.Name)
			}
		}
		apps, err := pc.GetApplications(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		for a := range apps.Model {
			v := syncs(&apps.Model[a], i, l.tags)
			out.Apps = append(out.Apps, appSync{App: apps.Model[a].Name, Synced: v.Synced, Why: v.Reason})
		}
		st, err := pc.GetIndexerStatus(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		for _, s := range st.Model {
			if s.IndexerId == i.Id {
				out.Status = &statusOut{DisabledTill: s.DisabledTill, InitialFailure: s.InitialFailure, MostRecentFailure: s.MostRecentFailure}
			}
		}
		stats, err := r.indexerStats(ctx, 30, []int{i.Id})
		if err != nil {
			return nil, getOut{}, err
		}
		out.Stats = statsOut{Days: 30}
		for _, s := range stats.Indexers {
			if s.IndexerId == i.Id {
				out.Stats = statsOut{
					Days: 30, Queries: s.NumberOfQueries, FailedQueries: s.NumberOfFailedQueries, RSSQueries: s.NumberOfRssQueries,
					FailedRSSQueries: s.NumberOfFailedRssQueries, AuthQueries: s.NumberOfAuthQueries, FailedAuthQueries: s.NumberOfFailedAuthQueries,
					Grabs: s.NumberOfGrabs, FailedGrabs: s.NumberOfFailedGrabs, AvgResponseMs: s.AverageResponseTime, AvgGrabResponseMs: s.AverageGrabResponseTime,
				}
			}
		}
		hist, err := pc.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOperationOptions{IndexerId: i.Id, Limit: 10})
		if err != nil {
			return nil, getOut{}, err
		}
		names := map[int]string{i.Id: i.Name}
		for _, h := range hist.Model {
			out.Recent = append(out.Recent, projectHistory(&h, names))
		}
		slices.SortStableFunc(out.Recent, func(a, b historyRow) int { return strings.Compare(b.Date, a.Date) })

		return nil, out, nil
	})

	type testIn struct {
		Indexer string `json:"indexer,omitempty" jsonschema:"one to test, by name or id; default every enabled one"`
	}
	type testOut struct {
		Results []testRow `json:"results"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "indexer_test",
		Description: "Test indexers the way Prowlarr's Test button does - reach the site, log in, run a query - one by name or every enabled one, and say what is wrong with any that fail. Changes nothing, though a test counts against the site's own request limits.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		all, err := r.indexers(ctx)
		if err != nil {
			return nil, testOut{}, err
		}
		if in.Indexer == "" {
			var results []prowlarr.ProviderTestAllResult
			res, testErr := pc.PostIndexerTestAll(ctx)
			if results, err = testAllAnswer(res.HttpResponse, res.Model, testErr); err != nil {
				return nil, testOut{}, err
			}

			return nil, testOut{Results: testResults(results, indexerNames(all))}, nil
		}
		i, err := find(all, in.Indexer, "indexer")
		if err != nil {
			return nil, testOut{}, err
		}
		row, err := r.testIndexer(ctx, i)

		return nil, testOut{Results: []testRow{row}}, err
	})
}

// testIndexer tests one indexer as it is saved.
func (r *registry) testIndexer(ctx context.Context, i *prowlarr.IndexerResource) (testRow, error) {
	_, err := r.client.PostIndexerTest(ctx, *i, prowlarr.PostIndexerTestOperationOptions{ForceTest: new(true)})

	return testOne(i.Name, err)
}

// nonEmpty drops the blank entries of a list.
func nonEmpty(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}

	return out
}

// indexerStats reads the traffic statistics of the last days days, for some
// indexers or every one.
func (r *registry) indexerStats(ctx context.Context, days int, ids []int) (*prowlarr.IndexerStatsResource, error) {
	opts := prowlarr.GetIndexerStatsOperationOptions{
		StartDate: stampUTC(time.Now().AddDate(0, 0, -days)),
		EndDate:   stampUTC(time.Now().Add(time.Minute)),
	}
	if len(ids) > 0 {
		opts.Indexers = client.CSV(ids)
	}
	res, err := r.client.GetIndexerStats(ctx, opts)
	if err != nil {
		return nil, err
	}
	if res.Model == nil {
		return &prowlarr.IndexerStatsResource{}, nil
	}

	return res.Model, nil
}

// Provider tests ---------------------------------------------------------------

// testRow is the outcome of testing one provider.
type testRow struct {
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Problems []string `json:"problems" jsonschema:"what Prowlarr found wrong, when it failed"`
}

// testResults names the outcome of testing every provider of a kind.
func testResults(results []prowlarr.ProviderTestAllResult, names map[int]string) []testRow {
	out := make([]testRow, 0, len(results))
	for _, res := range results {
		row := testRow{Name: names[res.Id], OK: boolv(res.IsValid)}
		if row.Name == "" {
			row.Name = fmt.Sprintf("id %d", res.Id)
		}
		for _, f := range res.ValidationFailures {
			msg := f.ErrorMessage
			if f.PropertyName != "" {
				msg = f.PropertyName + ": " + msg
			}
			row.Problems = append(row.Problems, msg)
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b testRow) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// testAllAnswer reads what testing every provider of a kind answered: the
// results on a 200, and on the 400 Prowlarr answers when any provider
// failed, the same results in the body, which comes back with the error.
func testAllAnswer(resp *http.Response, model []prowlarr.ProviderTestAllResult, err error) ([]prowlarr.ProviderTestAllResult, error) {
	if err == nil {
		return model, nil
	}
	if client.StatusCode(err) != http.StatusBadRequest || resp == nil || resp.Body == nil {
		return nil, err
	}
	var out []prowlarr.ProviderTestAllResult
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil, err
	}

	return out, nil
}

// testOne is the outcome of testing one provider: a validation failure is an
// answer, not an error.
func testOne(name string, err error) (testRow, error) {
	row := testRow{Name: name, OK: err == nil}
	if err == nil {
		return row, nil
	}
	se, ok := errors.AsType[*client.StatusError](err)
	if !ok || se.StatusCode != http.StatusBadRequest {
		return row, err
	}
	row.Problems = se.Messages
	if len(row.Problems) == 0 {
		row.Problems = []string{se.Body}
	}

	return row, nil
}
