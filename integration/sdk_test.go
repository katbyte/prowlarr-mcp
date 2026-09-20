//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/internal/fakes/newznab"
	"github.com/katbyte/prowlarr-mcp/lib/client"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
)

// The calls the tools rely on, field by field, and the fixes the importer's
// workarounds make, each proved against the server they were made for.

func TestSystem(t *testing.T) {
	need(t)

	st, err := sdk.GetSystemStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Model.Version, "2.") || st.Model.AppName != "Prowlarr" || !boolv(st.Model.IsDocker) || st.Model.StartTime == "" {
		t.Errorf("status = %+v", st.Model)
	}
	ping, err := sdk.GetPing(ctx)
	if err != nil || ping.Model.Status != "OK" {
		t.Errorf("ping = %+v, %v", ping.Model, err)
	}
	// prowlarr-undeclared-responses: the API info is JSON of no declared shape
	info, err := sdk.GetApi(ctx)
	if err != nil || info.Model == nil || info.Model.Current != "v1" {
		t.Errorf("api info = %+v, %v", info.Model, err)
	}
	health, err := sdk.GetHealth(ctx)
	if err != nil || len(health.Model) == 0 || health.Model[0].Source == "" || health.Model[0].Type == "" {
		t.Errorf("health = %+v, %v", health.Model, err)
	}
	tasks, err := sdk.GetSystemTask(ctx)
	if err != nil || !slices.ContainsFunc(tasks.Model, func(x prowlarr.TaskResource) bool { return x.TaskName == "ApplicationIndexerSync" && x.Interval > 0 }) {
		t.Errorf("tasks = %+v, %v", tasks.Model, err)
	}
	one, err := sdk.GetSystemTaskById(ctx, tasks.Model[0].Id)
	if err != nil || one.Model.TaskName != tasks.Model[0].TaskName {
		t.Errorf("one task = %+v, %v", one.Model, err)
	}
	// prowlarr-undeclared-responses: the route table is text
	routes, err := sdk.GetSystemRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = routes.HttpResponse.Body.Close() }()
	if b, _ := io.ReadAll(routes.HttpResponse.Body); !strings.HasPrefix(string(b), "digraph") {
		t.Errorf("routes = %.60q", b)
	}
}

// The creates answer 201 and the updates 202 (prowlarr-created-accepted),
// and an update's id is an integer (prowlarr-put-id-integer).
func TestIndexerWrites(t *testing.T) {
	need(t)

	got, err := sdk.GetIndexerById(ctx, fx.torrent)
	if err != nil {
		t.Fatal(err)
	}
	i := got.Model
	if i.Name != torrentName || i.Protocol != prowlarr.DownloadProtocolTorrent || i.Implementation != "Torznab" ||
		field(i.Fields, "baseUrl") != sites.URL(torrentSite) || i.AppProfileId != fx.profile || !slices.Contains(i.Tags, fx.tag) {
		t.Errorf("indexer = %+v", i)
	}
	// the site's capabilities, read from its caps when it was added
	if i.Capabilities == nil || len(i.Capabilities.Categories) == 0 || len(i.Capabilities.TvSearchParams) == 0 {
		t.Errorf("capabilities = %+v", i.Capabilities)
	}
	// the API key comes back masked
	if field(i.Fields, "apiKey") != "********" {
		t.Errorf("apiKey = %v", field(i.Fields, "apiKey"))
	}

	i.Priority = 12
	put, err := sdk.PutIndexerById(ctx, i.Id, *i, prowlarr.PutIndexerByIdOperationOptions{})
	if err != nil || put.HttpResponse.StatusCode != http.StatusAccepted || put.Model.Priority != 12 {
		t.Fatalf("update = %v, %v", put.HttpResponse.Status, err)
	}
	// the masked key sent back keeps the key
	if _, err := sdk.PostIndexerTest(ctx, *put.Model, prowlarr.PostIndexerTestOperationOptions{ForceTest: new(true)}); err != nil {
		t.Errorf("testing the saved indexer: %v", err)
	}

	// prowlarr-bulk-answer-lists: the bulk update answers every indexer it changed
	bulk, err := sdk.PutIndexerBulk(ctx, prowlarr.IndexerBulkResource{Ids: []int{fx.torrent, fx.usenet}, Priority: 25})
	if err != nil || len(bulk.Model) != 2 || bulk.Model[0].Priority != 25 {
		t.Errorf("bulk = %+v, %v", bulk.Model, err)
	}

	// prowlarr-test-all-results: every result, as a 200 while all pass
	all, err := sdk.PostIndexerTestAll(ctx)
	if err != nil || len(all.Model) != 2 || !boolv(all.Model[0].IsValid) {
		t.Errorf("test all = %+v, %v", all.Model, err)
	}

	// an unknown id is a 404, told apart
	if _, err := sdk.GetIndexerById(ctx, 99999); !client.IsNotFound(err) {
		t.Errorf("an unknown indexer = %v", err)
	}
	// a validation failure reads as what Prowlarr said
	bad := *put.Model
	bad.Priority = 99
	if _, err := sdk.PutIndexerById(ctx, bad.Id, bad, prowlarr.PutIndexerByIdOperationOptions{}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Priority") {
		t.Errorf("a priority out of range = %v", err)
	}
}

func TestSearchAndGrab(t *testing.T) {
	need(t)

	res, err := sdk.GetSearch(ctx, prowlarr.GetSearchOperationOptions{Query: "dune", Type: "search", IndexerIds: []int{fx.torrent, fx.usenet}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var torrent prowlarr.ReleaseResource
	for _, r := range res.Model {
		if r.IndexerId == fx.torrent {
			torrent = r
		}
	}
	if len(res.Model) != 2 || torrent.Title == "" || torrent.Seeders != 80 || torrent.Size == 0 || torrent.Guid == "" || len(torrent.Categories) == 0 ||
		torrent.TmdbId != 693134 || !strings.Contains(torrent.DownloadUrl, "/download?") {
		t.Errorf("search = %+v", res.Model)
	}

	grab, err := sdk.PostSearch(ctx, prowlarr.ReleaseResource{IndexerId: fx.torrent, Guid: torrent.Guid})
	if err != nil || grab.Model.Guid != torrent.Guid {
		t.Errorf("grab = %+v, %v", grab.Model, err)
	}
	// prowlarr-bulk-answer-lists: the bulk grab answers the releases it grabbed
	res, err = sdk.GetSearch(ctx, prowlarr.GetSearchOperationOptions{Query: "severance", IndexerIds: []int{fx.torrent}})
	if err != nil || len(res.Model) != 1 {
		t.Fatalf("second search = %v, %v", res.Model, err)
	}
	bulk, err := sdk.PostSearchBulk(ctx, []prowlarr.ReleaseResource{{IndexerId: fx.torrent, Guid: res.Model[0].Guid}})
	if err != nil || len(bulk.Model) != 1 {
		t.Errorf("bulk grab = %+v, %v", bulk.Model, err)
	}
	if sites.Downloads(torrentSite) < 2 {
		t.Errorf("the site served %d downloads, want the two grabs", sites.Downloads(torrentSite))
	}
}

// The Newznab and Torznab feed Prowlarr serves the applications, and the
// download link it hands them (prowlarr-undeclared-responses).
func TestNewznabFeed(t *testing.T) {
	need(t)

	caps, err := sdk.GetIndexerByIdNewznab(ctx, fx.torrent, prowlarr.GetIndexerByIdNewznabOperationOptions{T: "caps"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(caps.HttpResponse.Body)
	_ = caps.HttpResponse.Body.Close()
	if !strings.Contains(string(b), "<caps>") || !strings.Contains(caps.HttpResponse.Header.Get("Content-Type"), "xml") {
		t.Errorf("caps = %.80q", b)
	}
	feed, err := sdk.GetByIdApi(ctx, fx.torrent, prowlarr.GetByIdApiOperationOptions{T: "search", Q: "severance"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(feed.HttpResponse.Body)
	_ = feed.HttpResponse.Body.Close()
	if !strings.Contains(string(b), "Severance.S01E01") || !strings.Contains(string(b), "torznab:attr") {
		t.Errorf("feed = %.200q", b)
	}
	// a request missing the function is Prowlarr's 400, in XML
	if _, err := sdk.GetByIdApi(ctx, fx.torrent, prowlarr.GetByIdApiOperationOptions{}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("no function = %v", err)
	}
}

func TestApplicationSync(t *testing.T) {
	need(t)

	app, err := sdk.GetApplicationsById(ctx, fx.app)
	if err != nil || app.Model.SyncLevel != prowlarr.ApplicationSyncLevelFullSync || field(app.Model.Fields, "baseUrl") != apps.URL(fakeSonarr) {
		t.Fatalf("application = %+v, %v", app.Model, err)
	}
	if _, err := sdk.PostApplicationsTest(ctx, *app.Model, prowlarr.PostApplicationsTestOperationOptions{}); err != nil {
		t.Errorf("testing it: %v", err)
	}
	all, err := sdk.PostApplicationsTestAll(ctx)
	if err != nil || len(all.Model) != 1 || !boolv(all.Model[0].IsValid) {
		t.Errorf("test all = %+v, %v", all.Model, err)
	}

	// prowlarr-command-body: a command carries its own fields
	cmd := runCommand(t, map[string]any{"name": "ApplicationIndexerSync", "forceSync": true})
	if cmd.Status != prowlarr.CommandStatusCompleted || cmd.CommandName == "" {
		t.Fatalf("sync = %+v", cmd)
	}
	var names []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		names = names[:0]
		for _, i := range apps.Indexers(fakeSonarr) {
			names = append(names, i.Name)
		}
		if len(names) == 2 {
			break
		}
	}
	if !slices.Equal(names, []string{torrentName + " (Prowlarr)", usenetName + " (Prowlarr)"}) {
		t.Errorf("Sonarr holds %v", names)
	}

	app.Model.SyncLevel = prowlarr.ApplicationSyncLevelAddOnly
	put, err := sdk.PutApplicationsById(ctx, fx.app, *app.Model, prowlarr.PutApplicationsByIdOperationOptions{})
	if err != nil || put.Model.SyncLevel != prowlarr.ApplicationSyncLevelAddOnly {
		t.Errorf("update = %+v, %v", put.Model, err)
	}
	bulk, err := sdk.PutApplicationsBulk(ctx, prowlarr.ApplicationBulkResource{Ids: []int{fx.app}, SyncLevel: prowlarr.ApplicationSyncLevelFullSync})
	if err != nil || len(bulk.Model) != 1 || bulk.Model[0].SyncLevel != prowlarr.ApplicationSyncLevelFullSync {
		t.Errorf("bulk = %+v, %v", bulk.Model, err)
	}
}

func TestHistoryAndStats(t *testing.T) {
	need(t)

	for _, q := range []string{"first", "second", "third"} {
		if _, err := sdk.GetSearch(ctx, prowlarr.GetSearchOperationOptions{Query: q, IndexerIds: []int{fx.torrent}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := sdk.GetHistory(ctx, prowlarr.GetHistoryOperationOptions{Page: 1, PageSize: 2, SortKey: "date", SortDirection: prowlarr.SortDirectionDescending})
	if err != nil || len(page.Model.Records) != 2 || page.Model.TotalRecords < 3 {
		t.Fatalf("a page = %+v, %v", page.Model, err)
	}
	r := page.Model.Records[0]
	if r.EventType == "" || r.IndexerId == 0 || r.Date == "" || r.Data["query"] == "" || r.Data["elapsedTime"] == "" {
		t.Errorf("a record = %+v", r)
	}
	// the pager walks every page
	all, err := sdk.GetHistoryComplete(ctx, prowlarr.GetHistoryOperationOptions{PageSize: 2})
	if err != nil || len(all.Items) != page.Model.TotalRecords {
		t.Errorf("complete = %d of %d, %v", len(all.Items), page.Model.TotalRecords, err)
	}
	// filtered by event type (its number) and indexer
	grabs, err := sdk.GetHistory(ctx, prowlarr.GetHistoryOperationOptions{EventType: []int{2}, IndexerIds: []int{fx.torrent}, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grabs.Model.Records {
		if g.EventType != prowlarr.HistoryEventTypeIndexerQuery || g.IndexerId != fx.torrent {
			t.Errorf("filtered = %+v", g)
		}
	}
	since, err := sdk.GetHistorySince(ctx, prowlarr.GetHistorySinceOperationOptions{Date: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	if err != nil || len(since.Model) < 3 {
		t.Errorf("since = %d, %v", len(since.Model), err)
	}
	one, err := sdk.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOperationOptions{IndexerId: fx.torrent, Limit: 2})
	if err != nil || len(one.Model) != 2 {
		t.Errorf("an indexer's history = %d, %v", len(one.Model), err)
	}

	stats, err := sdk.GetIndexerStats(ctx, prowlarr.GetIndexerStatsOperationOptions{StartDate: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), Indexers: client.CSV([]int{fx.torrent})})
	if err != nil || len(stats.Model.Indexers) != 1 || stats.Model.Indexers[0].NumberOfQueries < 3 || stats.Model.Indexers[0].IndexerName != torrentName {
		t.Errorf("stats = %+v, %v", stats.Model, err)
	}
	if len(stats.Model.UserAgents) == 0 || len(stats.Model.Hosts) == 0 {
		t.Errorf("stats by agent and host = %+v", stats.Model)
	}
	if _, err := sdk.GetIndexerStatus(ctx); err != nil {
		t.Error(err)
	}
}

func TestTagsProfilesAndDetails(t *testing.T) {
	need(t)

	tag, err := sdk.GetTagById(ctx, fx.tag)
	if err != nil || tag.Model.Label != tagName {
		t.Fatalf("tag = %+v, %v", tag.Model, err)
	}
	put, err := sdk.PutTagById(ctx, fx.tag, prowlarr.TagResource{Id: fx.tag, Label: tagName})
	if err != nil || put.HttpResponse.StatusCode != http.StatusAccepted {
		t.Errorf("update = %v", err)
	}
	details, err := sdk.GetTagDetailById(ctx, fx.tag)
	if err != nil || !slices.Contains(details.Model.IndexerIds, fx.torrent) {
		t.Errorf("details = %+v, %v", details.Model, err)
	}

	profile, err := sdk.GetAppProfileById(ctx, fx.profile)
	if err != nil || profile.Model.MinimumSeeders != 3 || boolv(profile.Model.EnableInteractiveSearch) {
		t.Fatalf("profile = %+v, %v", profile.Model, err)
	}
	profile.Model.MinimumSeeders = 4
	if res, err := sdk.PutAppProfileById(ctx, fx.profile, *profile.Model); err != nil || res.Model.MinimumSeeders != 4 {
		t.Errorf("profile update = %v", err)
	}
	if schema, err := sdk.GetAppProfileSchema(ctx); err != nil || schema.Model == nil {
		t.Errorf("profile schema = %v", err)
	}
	// deleting a profile in use is refused
	if _, err := sdk.DeleteAppProfileById(ctx, fx.profile); err == nil {
		t.Error("a profile in use was deleted")
	}
}

// The settings sections are read whole and written back whole: a round trip
// changes nothing, and an empty string survives it (WrittenWhole).
func TestConfigRoundTrips(t *testing.T) {
	need(t)

	host, err := sdk.GetConfigHost(ctx)
	if err != nil || host.Model.Port != 9696 || host.Model.ApiKey == "" {
		t.Fatalf("host = %+v, %v", host.Model, err)
	}
	// the one section a round trip cannot write back: this container does not
	// require authentication from local addresses, and Prowlarr reads an empty
	// allowed-hosts list that it then refuses to save
	if _, err := sdk.PutConfigHostById(ctx, 1, *host.Model); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Allowed Hosts") {
		t.Errorf("host round trip = %v", err)
	}
	ui, err := sdk.GetConfigUi(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigUiById(ctx, 1, *ui.Model); err != nil {
		t.Errorf("ui round trip: %v", err)
	}
	dc, err := sdk.GetConfigDownloadClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigDownloadClientById(ctx, 1, *dc.Model); err != nil {
		t.Errorf("download client config round trip: %v", err)
	}
	dev, err := sdk.GetConfigDevelopment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigDevelopmentById(ctx, 1, *dev.Model); err != nil {
		t.Errorf("development config round trip: %v", err)
	}
	again, err := sdk.GetConfigHost(ctx)
	if err != nil || again.Model.Port != host.Model.Port || again.Model.UrlBase != host.Model.UrlBase || again.Model.ApiKey != host.Model.ApiKey {
		t.Errorf("after the round trip = %+v", again.Model)
	}
}

func TestProvidersReadAndUpdate(t *testing.T) {
	need(t)

	dc, err := sdk.GetDownloadClientById(ctx, fx.client)
	if err != nil || dc.Model.Implementation != "TorrentBlackhole" || field(dc.Model.Fields, "torrentFolder") != "/downloads/torrent" {
		t.Fatalf("download client = %+v, %v", dc.Model, err)
	}
	dc.Model.Priority = 5
	if res, err := sdk.PutDownloadClientById(ctx, fx.client, *dc.Model, prowlarr.PutDownloadClientByIdOperationOptions{}); err != nil || res.Model.Priority != 5 {
		t.Errorf("download client update = %v", err)
	}
	if res, err := sdk.PostDownloadClientTestAll(ctx); err != nil || len(res.Model) != 1 {
		t.Errorf("download client test all = %v", err)
	}
	if _, err := sdk.PostDownloadClientTest(ctx, *dc.Model, prowlarr.PostDownloadClientTestOperationOptions{}); err != nil {
		t.Errorf("download client test = %v", err)
	}
	if res, err := sdk.PutDownloadClientBulk(ctx, prowlarr.DownloadClientBulkResource{Ids: []int{fx.client}, Priority: 1}); err != nil || len(res.Model) != 1 {
		t.Errorf("download client bulk = %v", err)
	}

	px, err := sdk.GetIndexerProxyById(ctx, fx.proxy)
	if err != nil || px.Model.Implementation != "Http" || field(px.Model.Fields, "host") != "proxy.invalid" {
		t.Fatalf("proxy = %+v, %v", px.Model, err)
	}
	px.Model.Name = proxyName + " Renamed"
	if res, err := sdk.PutIndexerProxyById(ctx, fx.proxy, *px.Model, prowlarr.PutIndexerProxyByIdOperationOptions{}); err != nil || res.Model.Name != proxyName+" Renamed" {
		t.Errorf("proxy update = %v", err)
	}
	// none of the proxies is used, so testing them all tests none
	if res, err := sdk.PostIndexerProxyTestAll(ctx); err != nil || len(res.Model) != 0 {
		t.Errorf("proxy test all = %+v, %v", res.Model, err)
	}

	note, err := sdk.GetNotificationById(ctx, fx.webhook)
	if err != nil || note.Model.Implementation != "Webhook" || boolv(note.Model.OnGrab) {
		t.Fatalf("notification = %+v, %v", note.Model, err)
	}
	if res, err := sdk.PutNotificationById(ctx, fx.webhook, *note.Model, prowlarr.PutNotificationByIdOperationOptions{}); err != nil || res.HttpResponse.StatusCode != http.StatusAccepted {
		t.Errorf("notification update = %v", err)
	}
}

func TestCommandsAndBackups(t *testing.T) {
	need(t)

	cmd := runCommand(t, map[string]any{"name": "Backup"})
	if cmd.Status != prowlarr.CommandStatusCompleted {
		t.Fatalf("backup = %+v", cmd)
	}
	backups, err := sdk.GetSystemBackup(ctx)
	if err != nil || len(backups.Model) == 0 || backups.Model[0].Size == 0 || backups.Model[0].Type == "" {
		t.Fatalf("backups = %+v, %v", backups.Model, err)
	}
	if res, err := sdk.GetCommand(ctx); err != nil || len(res.Model) == 0 {
		t.Errorf("commands = %v", err)
	}
	if _, err := sdk.DeleteSystemBackupById(ctx, backups.Model[0].Id); err != nil {
		t.Errorf("deleting the backup: %v", err)
	}
}

func TestLogs(t *testing.T) {
	need(t)

	logs, err := sdk.GetLog(ctx, prowlarr.GetLogOperationOptions{Page: 1, PageSize: 5, SortKey: "time", SortDirection: prowlarr.SortDirectionDescending})
	if err != nil || len(logs.Model.Records) == 0 || logs.Model.Records[0].Level == "" || logs.Model.Records[0].Message == "" {
		t.Fatalf("log = %+v, %v", logs.Model, err)
	}
	files, err := sdk.GetLogFile(ctx)
	if err != nil || len(files.Model) == 0 {
		t.Fatalf("log files = %v", err)
	}
	one, err := sdk.GetLogFileByFilename(ctx, files.Model[0].Filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = one.HttpResponse.Body.Close() }()
	if b, _ := io.ReadAll(io.LimitReader(one.HttpResponse.Body, 4096)); !strings.Contains(string(b), "|Info|") {
		t.Errorf("a log file = %.100q", b)
	}
}

// prowlarr-undeclared-write-responses: a provider's action answers JSON of
// its own shape - here the options a Cardigann definition's setting offers.
func TestProviderAction(t *testing.T) {
	need(t)

	app, err := sdk.GetApplicationsById(ctx, fx.app)
	if err != nil {
		t.Fatal(err)
	}
	// an action the provider does not have answers an empty object, still JSON
	res, err := sdk.PostApplicationsActionByName(ctx, "nothing", *app.Model)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(res.Model, &v); err != nil {
		t.Errorf("the answer is not JSON: %q", res.Model)
	}
}

// A failing test answers 400 with every result, which the generated method
// takes as an answer rather than an error. This runs last: a failed test
// blocks the indexer for a minute, and the searches above need it.
func TestTestAllWithAFailure(t *testing.T) {
	need(t)

	sites.SetFailure(usenetSite, newznab.Failure{Status: http.StatusServiceUnavailable})
	defer sites.SetFailure(usenetSite, newznab.Failure{})
	res, err := sdk.PostIndexerTestAll(ctx)
	if err != nil || res.HttpResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("test all = %v, %v", res.HttpResponse, err)
	}
	var failed prowlarr.ProviderTestAllResult
	for _, r := range res.Model {
		if r.Id == fx.usenet {
			failed = r
		}
	}
	if boolv(failed.IsValid) || len(failed.ValidationFailures) == 0 || failed.ValidationFailures[0].ErrorMessage == "" {
		t.Errorf("the usenet indexer's result = %+v", failed)
	}
}

func boolv(b *bool) bool { return b != nil && *b }
