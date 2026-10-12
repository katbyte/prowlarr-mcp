//go:build acceptance

package prowlarr_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/go-kt/test/services/newznab"
	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// The calls the tools rely on, field by field, and the fixes the importer's
// workarounds make, each proved against the server they were made for.

func TestSystem(t *testing.T) {
	need(t)

	st, err := sdk.GetSystemStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Answer.Version, "2.") || st.Answer.AppName != "Prowlarr" || !st.Answer.IsDocker || st.Answer.StartTime.IsZero() {
		t.Errorf("status = %+v", st.Answer)
	}
	ping, err := sdk.GetPing(ctx)
	if err != nil || ping.Answer.Status != "OK" {
		t.Errorf("ping = %+v, %v", ping.Answer, err)
	}
	// prowlarr-undeclared-responses: the API info is JSON of no declared shape
	info, err := sdk.GetApi(ctx)
	if err != nil || info.Answer == nil || info.Answer.Current != "v1" {
		t.Errorf("api info = %+v, %v", info.Answer, err)
	}
	health, err := sdk.GetHealth(ctx)
	if err != nil || len(health.Answer) == 0 || health.Answer[0].Source == "" || health.Answer[0].Type == "" {
		t.Errorf("health = %+v, %v", health.Answer, err)
	}
	tasks, err := sdk.GetSystemTask(ctx)
	if err != nil || !slices.ContainsFunc(tasks.Answer, func(x prowlarr.TaskResource) bool { return x.TaskName == "ApplicationIndexerSync" && x.Interval > 0 }) {
		t.Errorf("tasks = %+v, %v", tasks.Answer, err)
	}
	one, err := sdk.GetSystemTaskById(ctx, tasks.Answer[0].Id)
	if err != nil || one.Answer.TaskName != tasks.Answer[0].TaskName {
		t.Errorf("one task = %+v, %v", one.Answer, err)
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
	i := got.Answer
	if i.Name != torrentName || i.Protocol != prowlarr.DownloadProtocolTorrent || i.Implementation != "Torznab" ||
		field(i.Fields, "baseUrl") != sites.Indexer(torrentSite).URL() || i.AppProfileId != fx.profile || !slices.Contains(i.Tags, fx.tag) {
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
	put, err := sdk.PutIndexerById(ctx, i.Id, model(t, i))
	if err != nil || put.HttpResponse.StatusCode != http.StatusAccepted || put.Answer.Priority != 12 {
		t.Fatalf("update = %v, %v", put.HttpResponse.Status, err)
	}
	// the masked key sent back keeps the key
	if _, err := sdk.PostIndexerTest(ctx, model(t, put.Answer), true); err != nil {
		t.Errorf("testing the saved indexer: %v", err)
	}

	// prowlarr-bulk-answer-lists: the bulk update answers every indexer it changed
	bulk, err := sdk.PutIndexerBulk(ctx, prowlarr.IndexerBulkResource{Ids: []prowlarr.IndexerID{fx.torrent, fx.usenet}, Priority: 25})
	if err != nil || len(bulk.Answer) != 2 || bulk.Answer[0].Priority != 25 {
		t.Errorf("bulk = %+v, %v", bulk.Answer, err)
	}

	// prowlarr-test-all-results: every result, as a 200 while all pass
	all, err := sdk.PostIndexerTestAll(ctx)
	if err != nil || len(all.Answer) != 2 || !all.Answer[0].IsValid {
		t.Errorf("test all = %+v, %v", all.Answer, err)
	}

	// an unknown id is a 404, told apart
	if _, err := sdk.GetIndexerById(ctx, 99999); !client.IsNotFound(err) {
		t.Errorf("an unknown indexer = %v", err)
	}
	// a validation failure reads as what Prowlarr said
	bad := model(t, put.Answer)
	bad.Priority = 99
	if _, err := sdk.PutIndexerById(ctx, bad.Id, bad); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Priority") {
		t.Errorf("a priority out of range = %v", err)
	}
}

func TestSearchAndGrab(t *testing.T) {
	need(t)

	res, err := sdk.GetSearch(ctx, prowlarr.GetSearchOptions{Query: "dune", Type: "search", IndexerIds: []prowlarr.IndexerID{fx.torrent, fx.usenet}, Limit: new(100)})
	if err != nil {
		t.Fatal(err)
	}
	var torrent prowlarr.ReleaseResource
	for _, r := range res.Answer {
		if r.IndexerId == fx.torrent {
			torrent = r
		}
	}
	if len(res.Answer) != 2 || torrent.Title == "" || torrent.Seeders != 80 || torrent.Size == 0 || torrent.Guid == "" || len(torrent.Categories) == 0 ||
		torrent.TmdbId != 693134 || !strings.Contains(torrent.DownloadUrl, "/download?") {
		t.Errorf("search = %+v", res.Answer)
	}

	grab, err := sdk.PostSearch(ctx, prowlarr.ReleaseResource{IndexerId: fx.torrent, Guid: torrent.Guid})
	if err != nil || grab.Answer.Guid != torrent.Guid {
		t.Errorf("grab = %+v, %v", grab.Answer, err)
	}
	// prowlarr-bulk-answer-lists: the bulk grab answers the releases it grabbed
	res, err = sdk.GetSearch(ctx, prowlarr.GetSearchOptions{Query: "severance", IndexerIds: []prowlarr.IndexerID{fx.torrent}})
	if err != nil || len(res.Answer) != 1 {
		t.Fatalf("second search = %v, %v", res.Answer, err)
	}
	bulk, err := sdk.PostSearchBulk(ctx, []prowlarr.ReleaseResource{{IndexerId: fx.torrent, Guid: res.Answer[0].Guid}})
	if err != nil || len(bulk.Answer) != 1 {
		t.Errorf("bulk grab = %+v, %v", bulk.Answer, err)
	}
	if len(sites.Indexer(torrentSite).Grabbed()) < 2 {
		t.Errorf("the site served %d downloads, want the two grabs", len(sites.Indexer(torrentSite).Grabbed()))
	}
}

// The Newznab and Torznab feed Prowlarr serves the applications, and the
// download link it hands them (prowlarr-undeclared-responses).
func TestNewznabFeed(t *testing.T) {
	need(t)

	caps, err := sdk.GetIndexerByIdNewznab(ctx, fx.torrent, prowlarr.GetIndexerByIdNewznabOptions{T: "caps"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(caps.HttpResponse.Body)
	_ = caps.HttpResponse.Body.Close()
	if !strings.Contains(string(b), "<caps>") || !strings.Contains(caps.HttpResponse.Header.Get("Content-Type"), "xml") {
		t.Errorf("caps = %.80q", b)
	}
	feed, err := sdk.GetByIdApi(ctx, int(fx.torrent), prowlarr.GetByIdApiOptions{T: "search", Q: "severance"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(feed.HttpResponse.Body)
	_ = feed.HttpResponse.Body.Close()
	if !strings.Contains(string(b), "Severance.S01E01") || !strings.Contains(string(b), "torznab:attr") {
		t.Errorf("feed = %.200q", b)
	}
	// a request missing the function is Prowlarr's 400, in XML
	if _, err := sdk.GetByIdApi(ctx, int(fx.torrent), prowlarr.GetByIdApiOptions{}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("no function = %v", err)
	}
}

func TestApplicationSync(t *testing.T) {
	need(t)

	app, err := sdk.GetApplicationsById(ctx, fx.app)
	if err != nil || app.Answer.SyncLevel != prowlarr.ApplicationSyncLevelFullSync || field(app.Answer.Fields, "baseUrl") != apps.URL(fakeSonarr) {
		t.Fatalf("application = %+v, %v", app.Answer, err)
	}
	if _, err := sdk.PostApplicationsTest(ctx, model(t, app.Answer)); err != nil {
		t.Errorf("testing it: %v", err)
	}
	all, err := sdk.PostApplicationsTestAll(ctx)
	if err != nil || len(all.Answer) != 1 || !all.Answer[0].IsValid {
		t.Errorf("test all = %+v, %v", all.Answer, err)
	}

	// prowlarr-command-body: a command carries its own fields
	cmd := finished(t)(sdk.ApplicationIndexerSyncAndWait(ctx, prowlarr.ApplicationIndexerSyncCommand{ForceSync: new(true)}, commandWait))
	if cmd.CommandName == "" {
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

	app.Answer.SyncLevel = prowlarr.ApplicationSyncLevelAddOnly
	put, err := sdk.PutApplicationsById(ctx, fx.app, model(t, app.Answer))
	if err != nil || put.Answer.SyncLevel != prowlarr.ApplicationSyncLevelAddOnly {
		t.Errorf("update = %+v, %v", put.Answer, err)
	}
	bulk, err := sdk.PutApplicationsBulk(ctx, prowlarr.ApplicationBulkResource{Ids: []int{fx.app}, SyncLevel: prowlarr.ApplicationSyncLevelFullSync})
	if err != nil || len(bulk.Answer) != 1 || bulk.Answer[0].SyncLevel != prowlarr.ApplicationSyncLevelFullSync {
		t.Errorf("bulk = %+v, %v", bulk.Answer, err)
	}
}

func TestHistoryAndStats(t *testing.T) {
	need(t)

	for _, q := range []string{"first", "second", "third"} {
		if _, err := sdk.GetSearch(ctx, prowlarr.GetSearchOptions{Query: q, IndexerIds: []prowlarr.IndexerID{fx.torrent}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := sdk.GetHistory(ctx, prowlarr.GetHistoryOptions{Page: new(1), PageSize: new(2), SortKey: "date", SortDirection: prowlarr.SortDirectionDescending})
	if err != nil || len(page.Answer.Records) != 2 || page.Answer.TotalRecords < 3 {
		t.Fatalf("a page = %+v, %v", page.Answer, err)
	}
	r := page.Answer.Records[0]
	if r.EventType == "" || r.IndexerId == 0 || r.Date.IsZero() || r.Data["query"] == "" || r.Data["elapsedTime"] == "" {
		t.Errorf("a record = %+v", r)
	}
	// the pager walks every page
	all, err := sdk.GetHistoryComplete(ctx, prowlarr.GetHistoryOptions{PageSize: new(2)})
	if err != nil || len(all.Items) != page.Answer.TotalRecords {
		t.Errorf("complete = %d of %d, %v", len(all.Items), page.Answer.TotalRecords, err)
	}
	// filtered by event type (its number) and indexer
	grabs, err := sdk.GetHistory(ctx, prowlarr.GetHistoryOptions{EventType: []int{2}, IndexerIds: []prowlarr.IndexerID{fx.torrent}, PageSize: new(50)})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grabs.Answer.Records {
		if g.EventType != prowlarr.HistoryEventTypeIndexerQuery || g.IndexerId != fx.torrent {
			t.Errorf("filtered = %+v", g)
		}
	}
	since, err := sdk.GetHistorySince(ctx, prowlarr.GetHistorySinceOptions{Date: prowlarr.Time{Time: time.Now().Add(-time.Hour)}})
	if err != nil || len(since.Answer) < 3 {
		t.Errorf("since = %d, %v", len(since.Answer), err)
	}
	one, err := sdk.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOptions{IndexerId: fx.torrent, Limit: new(2)})
	if err != nil || len(one.Answer) != 2 {
		t.Errorf("an indexer's history = %d, %v", len(one.Answer), err)
	}

	stats, err := sdk.GetIndexerStats(ctx, prowlarr.GetIndexerStatsOptions{StartDate: prowlarr.Time{Time: time.Now().Add(-time.Hour)}, Indexers: []prowlarr.IndexerID{fx.torrent}})
	if err != nil || len(stats.Answer.Indexers) != 1 || stats.Answer.Indexers[0].NumberOfQueries < 3 || stats.Answer.Indexers[0].IndexerName != torrentName {
		t.Errorf("stats = %+v, %v", stats.Answer, err)
	}
	if len(stats.Answer.UserAgents) == 0 || len(stats.Answer.Hosts) == 0 {
		t.Errorf("stats by agent and host = %+v", stats.Answer)
	}
	if _, err := sdk.GetIndexerStatus(ctx); err != nil {
		t.Error(err)
	}
}

func TestTagsProfilesAndDetails(t *testing.T) {
	need(t)

	tag, err := sdk.GetTagById(ctx, fx.tag)
	if err != nil || tag.Answer.Label != tagName {
		t.Fatalf("tag = %+v, %v", tag.Answer, err)
	}
	put, err := sdk.PutTagById(ctx, fx.tag, prowlarr.TagResource{Id: fx.tag, Label: tagName})
	if err != nil || put.HttpResponse.StatusCode != http.StatusAccepted {
		t.Errorf("update = %v", err)
	}
	details, err := sdk.GetTagDetailById(ctx, fx.tag)
	if err != nil || !slices.Contains(details.Answer.IndexerIds, fx.torrent) {
		t.Errorf("details = %+v, %v", details.Answer, err)
	}

	profile, err := sdk.GetAppProfileById(ctx, fx.profile)
	if err != nil || profile.Answer.MinimumSeeders != 3 || boolv(profile.Answer.EnableInteractiveSearch) {
		t.Fatalf("profile = %+v, %v", profile.Answer, err)
	}
	profile.Answer.MinimumSeeders = 4
	if res, err := sdk.PutAppProfileById(ctx, fx.profile, model(t, profile.Answer)); err != nil || res.Answer.MinimumSeeders != 4 {
		t.Errorf("profile update = %v", err)
	}
	if schema, err := sdk.GetAppProfileSchema(ctx); err != nil || schema.Answer == nil {
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
	if err != nil || host.Answer.Port != 9696 || host.Answer.ApiKey == "" {
		t.Fatalf("host = %+v, %v", host.Answer, err)
	}
	// prowlarr-host-settings: the server sends the host names it answers to
	// and the networks it trusts, which the document does not list. This
	// container lets local addresses in unasked and starts with no allowed
	// hosts, which Prowlarr reads back and then refuses to save, so the round
	// trip is refused as read and taken once the hosts are named
	if host.Answer.AllowedHosts == "" {
		if _, err := sdk.PutConfigHostById(ctx, 1, model(t, host.Answer)); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Allowed Hosts") {
			t.Errorf("host round trip with no allowed hosts = %v", err)
		}
	}
	host.Answer.AllowedHosts = "127.0.0.1,localhost," + testenv.Host()
	put, err := sdk.PutConfigHostById(ctx, 1, model(t, host.Answer))
	if err != nil || put.Answer.AllowedHosts != host.Answer.AllowedHosts || put.Answer.ApiKey != host.Answer.ApiKey || put.Answer.Port != 9696 {
		t.Errorf("host round trip with the hosts named = %+v, %v", put.Answer, err)
	}
	ui, err := sdk.GetConfigUi(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigUiById(ctx, 1, model(t, ui.Answer)); err != nil {
		t.Errorf("ui round trip: %v", err)
	}
	dc, err := sdk.GetConfigDownloadClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigDownloadClientById(ctx, 1, model(t, dc.Answer)); err != nil {
		t.Errorf("download client config round trip: %v", err)
	}
	dev, err := sdk.GetConfigDevelopment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PutConfigDevelopmentById(ctx, 1, model(t, dev.Answer)); err != nil {
		t.Errorf("development config round trip: %v", err)
	}
	again, err := sdk.GetConfigHost(ctx)
	if err != nil || again.Answer.Port != host.Answer.Port || again.Answer.UrlBase != host.Answer.UrlBase || again.Answer.ApiKey != host.Answer.ApiKey {
		t.Errorf("after the round trip = %+v", again.Answer)
	}
}

func TestProvidersReadAndUpdate(t *testing.T) {
	need(t)

	dc, err := sdk.GetDownloadClientById(ctx, fx.client)
	if err != nil || dc.Answer.Implementation != "TorrentBlackhole" || field(dc.Answer.Fields, "torrentFolder") != "/downloads/torrent" {
		t.Fatalf("download client = %+v, %v", dc.Answer, err)
	}
	dc.Answer.Priority = 5
	if res, err := sdk.PutDownloadClientById(ctx, fx.client, model(t, dc.Answer)); err != nil || res.Answer.Priority != 5 {
		t.Errorf("download client update = %v", err)
	}
	if res, err := sdk.PostDownloadClientTestAll(ctx); err != nil || len(res.Answer) != 1 {
		t.Errorf("download client test all = %v", err)
	}
	if _, err := sdk.PostDownloadClientTest(ctx, model(t, dc.Answer)); err != nil {
		t.Errorf("download client test = %v", err)
	}
	if res, err := sdk.PutDownloadClientBulk(ctx, prowlarr.DownloadClientBulkResource{Ids: []prowlarr.DownloadClientID{fx.client}, Priority: 1}); err != nil || len(res.Answer) != 1 {
		t.Errorf("download client bulk = %v", err)
	}

	px, err := sdk.GetIndexerProxyById(ctx, fx.proxy)
	if err != nil || px.Answer.Implementation != "Http" || field(px.Answer.Fields, "host") != "proxy.invalid" {
		t.Fatalf("proxy = %+v, %v", px.Answer, err)
	}
	px.Answer.Name = proxyName + " Renamed"
	if res, err := sdk.PutIndexerProxyById(ctx, fx.proxy, model(t, px.Answer)); err != nil || res.Answer.Name != proxyName+" Renamed" {
		t.Errorf("proxy update = %v", err)
	}
	// its host is not one that exists, so its test is Prowlarr's 400 saying what failed, which is an answer from the test and not a fault
	if _, err := sdk.PostIndexerProxyTest(ctx, model(t, px.Answer)); err != nil && client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("proxy test = %v, want a pass or Prowlarr's 400", err)
	}
	// none of the proxies is used, so testing them all tests none
	if res, err := sdk.PostIndexerProxyTestAll(ctx); err != nil || len(res.Answer) != 0 {
		t.Errorf("proxy test all = %+v, %v", res.Answer, err)
	}

	note, err := sdk.GetNotificationById(ctx, fx.webhook)
	if err != nil || note.Answer.Implementation != "Webhook" || boolv(note.Answer.OnGrab) {
		t.Fatalf("notification = %+v, %v", note.Answer, err)
	}
	if res, err := sdk.PutNotificationById(ctx, fx.webhook, model(t, note.Answer)); err != nil || res.HttpResponse.StatusCode != http.StatusAccepted {
		t.Errorf("notification update = %v", err)
	}
	// the webhook's address has nothing listening, and Prowlarr answers a notification it could not send with a 500 saying so, alone or among them all
	if _, err := sdk.PostNotificationTest(ctx, model(t, note.Answer)); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "refused") {
		t.Errorf("notification test = %v, want Prowlarr's 500 about the connection", err)
	}
	if all, err := sdk.PostNotificationTestAll(ctx); all.HttpResponse == nil || client.StatusCode(err) > http.StatusInternalServerError {
		t.Errorf("notification test all = %+v, %v", all.Answer, err)
	}

	filter, err := sdk.GetCustomFilterById(ctx, fx.filter)
	if err != nil || filter.Answer == nil || filter.Answer.Label != filterName {
		t.Fatalf("filter = %+v, %v", filter.Answer, err)
	}
	filter.Answer.Label = filterName + " Renamed"
	if res, err := sdk.PutCustomFilterById(ctx, fx.filter, model(t, filter.Answer)); err != nil || res.Answer == nil || res.Answer.Label != filterName+" Renamed" {
		t.Errorf("filter update = %+v, %v", res.Answer, err)
	}
}

func TestCommandsAndBackups(t *testing.T) {
	need(t)

	finished(t)(sdk.BackupAndWait(ctx, prowlarr.BackupCommand{}, commandWait))
	backups, err := sdk.GetSystemBackup(ctx)
	if err != nil || len(backups.Answer) == 0 || backups.Answer[0].Size == 0 || backups.Answer[0].Type == "" {
		t.Fatalf("backups = %+v, %v", backups.Answer, err)
	}
	if res, err := sdk.GetCommand(ctx); err != nil || len(res.Answer) == 0 {
		t.Errorf("commands = %v", err)
	}
	// cancelling takes a command off the queue before it starts; one caught later is refused, and either is the server answering the call
	queued, err := sdk.PostCommand(ctx, []byte(`{"name":"CheckHealth"}`))
	if err != nil || queued.Answer == nil {
		t.Fatalf("queueing a command to cancel = %+v, %v", queued.Answer, err)
	}
	if res, err := sdk.DeleteCommandById(ctx, queued.Answer.Id); res.HttpResponse == nil {
		t.Errorf("cancelling the command got no answer: %v", err)
	}
	if _, err := sdk.DeleteSystemBackupById(ctx, backups.Answer[0].Id); err != nil {
		t.Errorf("deleting the backup: %v", err)
	}
}

func TestLogs(t *testing.T) {
	need(t)

	logs, err := sdk.GetLog(ctx, prowlarr.GetLogOptions{Page: new(1), PageSize: new(5), SortKey: "time", SortDirection: prowlarr.SortDirectionDescending})
	if err != nil || len(logs.Answer.Records) == 0 || logs.Answer.Records[0].Level == "" || logs.Answer.Records[0].Message == "" {
		t.Fatalf("log = %+v, %v", logs.Answer, err)
	}
	files, err := sdk.GetLogFile(ctx)
	if err != nil || len(files.Answer) == 0 {
		t.Fatalf("log files = %v", err)
	}
	one, err := sdk.GetLogFileByFilename(ctx, files.Answer[0].Filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = one.HttpResponse.Body.Close() }()
	if b, _ := io.ReadAll(io.LimitReader(one.HttpResponse.Body, 4096)); !strings.Contains(string(b), "|Info|") {
		t.Errorf("a log file = %.100q", b)
	}
}

// prowlarr-undeclared-write-responses: a provider's action answers JSON of
// its own shape, the options a settings field offers. Every kind of provider
// has the route, and an action the provider does not have answers an empty
// object, still JSON.
func TestProviderActions(t *testing.T) {
	need(t)

	isJSON := func(what string, raw json.RawMessage, err error) {
		t.Helper()

		if err != nil {
			t.Errorf("%s: %v", what, err)

			return
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("%s answered something that is not JSON: %q", what, raw)
		}
	}
	idx, err := sdk.GetIndexerById(ctx, fx.torrent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sdk.PostIndexerActionByName(ctx, "nothing", model(t, idx.Answer))
	isJSON("an indexer's action", res.Answer, err)
	app, err := sdk.GetApplicationsById(ctx, fx.app)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := sdk.PostApplicationsActionByName(ctx, "nothing", model(t, app.Answer))
	isJSON("an application's action", res2.Answer, err)
	dc, err := sdk.GetDownloadClientById(ctx, fx.client)
	if err != nil {
		t.Fatal(err)
	}
	res3, err := sdk.PostDownloadClientActionByName(ctx, "nothing", model(t, dc.Answer))
	isJSON("a download client's action", res3.Answer, err)
	px, err := sdk.GetIndexerProxyById(ctx, fx.proxy)
	if err != nil {
		t.Fatal(err)
	}
	res4, err := sdk.PostIndexerProxyActionByName(ctx, "nothing", model(t, px.Answer))
	isJSON("a proxy's action", res4.Answer, err)
	note, err := sdk.GetNotificationById(ctx, fx.webhook)
	if err != nil {
		t.Fatal(err)
	}
	res5, err := sdk.PostNotificationActionByName(ctx, "nothing", model(t, note.Answer))
	isJSON("a notification's action", res5.Answer, err)
}

// prowlarr-bulk-answer-lists is for the bulk updates; the bulk deletes take
// the same body, ids and all, and answer nothing. One of each kind is made
// to be deleted that way: an indexer, an application and a download client.
func TestBulkDeletes(t *testing.T) {
	need(t)

	schema, err := sdk.GetIndexerSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(schema.Answer, func(x prowlarr.IndexerResource) bool { return x.Name == "Generic Torznab" })
	idx := schema.Answer[i]
	idx.Name, idx.Enable, idx.Presets, idx.AppProfileId = "Bulk Torznab", new(false), nil, fx.profile
	setField(idx.Fields, "baseUrl", sites.Indexer(torrentSite).URL())
	setField(idx.Fields, "apiKey", siteKey)
	madeIdx, err := sdk.PostIndexer(ctx, idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.DeleteIndexerBulk(ctx, prowlarr.IndexerBulkResource{Ids: []prowlarr.IndexerID{madeIdx.Answer.Id}}); err != nil {
		t.Errorf("deleting indexers in bulk: %v", err)
	}
	if _, err := sdk.GetIndexerById(ctx, madeIdx.Answer.Id); client.StatusCode(err) != http.StatusNotFound {
		t.Errorf("after the bulk delete the indexer answers %v, want a 404", err)
	}

	appSchema, err := sdk.GetApplicationsSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i = slices.IndexFunc(appSchema.Answer, func(x prowlarr.ApplicationResource) bool { return x.Implementation == "Sonarr" })
	app := appSchema.Answer[i]
	app.Name, app.Presets, app.SyncLevel, app.Tags = "Bulk Sonarr", nil, prowlarr.ApplicationSyncLevelDisabled, []prowlarr.TagID{}
	setField(app.Fields, "baseUrl", apps.URL(fakeSonarr))
	setField(app.Fields, "apiKey", fakeSonarrKey)
	setField(app.Fields, "prowlarrUrl", "http://prowlarr:9696")
	madeApp, err := sdk.PostApplications(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.DeleteApplicationsBulk(ctx, prowlarr.ApplicationBulkResource{Ids: []int{madeApp.Answer.Id}}); err != nil {
		t.Errorf("deleting applications in bulk: %v", err)
	}
	if _, err := sdk.GetApplicationsById(ctx, madeApp.Answer.Id); client.StatusCode(err) != http.StatusNotFound {
		t.Errorf("after the bulk delete the application answers %v, want a 404", err)
	}

	clients, err := sdk.GetDownloadClientSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i = slices.IndexFunc(clients.Answer, func(x prowlarr.DownloadClientResource) bool { return x.Implementation == "TorrentBlackhole" })
	dc := clients.Answer[i]
	dc.Name, dc.Enable, dc.Presets = "Bulk Blackhole", new(false), nil
	setField(dc.Fields, "torrentFolder", "/downloads/torrent")
	madeDC, err := sdk.PostDownloadClient(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.DeleteDownloadClientBulk(ctx, prowlarr.DownloadClientBulkResource{Ids: []prowlarr.DownloadClientID{madeDC.Answer.Id}}); err != nil {
		t.Errorf("deleting download clients in bulk: %v", err)
	}
	if _, err := sdk.GetDownloadClientById(ctx, madeDC.Answer.Id); client.StatusCode(err) != http.StatusNotFound {
		t.Errorf("after the bulk delete the download client answers %v, want a 404", err)
	}
}

// A failing test answers 400 with every result, which the generated method
// takes as an answer rather than an error. This runs last: a failed test
// blocks the indexer for a minute, and the searches above need it.
func TestTestAllWithAFailure(t *testing.T) {
	need(t)

	sites.Indexer(usenetSite).SetFailure(newznab.Failure{Status: http.StatusServiceUnavailable})
	defer sites.Indexer(usenetSite).SetFailure(newznab.Failure{})
	res, err := sdk.PostIndexerTestAll(ctx)
	if err != nil || res.HttpResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("test all = %v, %v", res.HttpResponse, err)
	}
	var failed prowlarr.ProviderTestAllResult
	for _, r := range res.Answer {
		if r.Id == int(fx.usenet) {
			failed = r
		}
	}
	if failed.IsValid || len(failed.ValidationFailures) == 0 || failed.ValidationFailures[0].ErrorMessage == "" {
		t.Errorf("the usenet indexer's result = %+v", failed)
	}
}

func boolv(b *bool) bool { return b != nil && *b }

// The releases Prowlarr reads from its update server, which the suite plays:
// each decodes with its changes, the newest is marked latest and the one
// running installed, and with the server away the read answers a 500 saying
// so.
func TestUpdates(t *testing.T) {
	need(t)

	st, err := sdk.GetSystemStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const newer = "99.0.0.1"
	home.Offer(arrserver.Update{Version: newer, New: []string{"a new thing"}, Fixed: []string{"an old fault"}}, arrserver.Update{Version: st.Answer.Version})
	t.Cleanup(func() { home.Offer() })

	updates, err := sdk.GetUpdate(ctx)
	if err != nil || len(updates.Answer) != 2 {
		t.Fatalf("updates = %+v, %v", updates.Answer, err)
	}
	latest, running := updates.Answer[0], updates.Answer[1]
	if latest.Version != newer || !latest.Latest || latest.Installed || !latest.Installable || latest.ReleaseDate.IsZero() || latest.Branch == "" || latest.FileName == "" {
		t.Errorf("the newer release = %+v", latest)
	}
	if latest.Changes == nil || !slices.Equal(latest.Changes.New, []string{"a new thing"}) || !slices.Equal(latest.Changes.Fixed, []string{"an old fault"}) {
		t.Errorf("its changes = %+v", latest.Changes)
	}
	if running.Version != st.Answer.Version || !running.Installed || running.Latest {
		t.Errorf("the release running = %+v", running)
	}

	home.SetDown(true)
	t.Cleanup(func() { home.SetDown(false) })
	if _, err := sdk.GetUpdate(ctx); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("with the update server down the read answered %v, want a 500", err)
	}
}

// prowlarr-restore-upload-body: a backup is uploaded as a multipart form and
// restored, and restored again by its id, which Prowlarr applies when it next starts; then Prowlarr is shut
// down. In the test container its supervisor starts it again, which is how a
// shutdown can be tested at all, and how the restore takes effect. On a real
// install nothing starts it again. This runs last: everything after it would
// run against a Prowlarr just restarted, and the backup is of the state the
// tests above left, so the clean-up after finds what it expects.
func TestRestoreUploadShutdownAndRestart(t *testing.T) {
	need(t)

	finished(t)(sdk.BackupAndWait(ctx, prowlarr.BackupCommand{}, commandWait))
	backups, err := sdk.GetSystemBackup(ctx)
	if err != nil || len(backups.Answer) == 0 {
		t.Fatalf("backups = %v, %v", backups.Answer, err)
	}
	newest := backups.Answer[0]
	// the file itself is not served by the API; the suite reads it from the
	// container's data directory, which it mounts
	data, err := os.ReadFile(filepath.Join(testenv.DataDir(), "config", "Backups", string(newest.Type), newest.Name))
	if err != nil {
		t.Fatal(err)
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreateFormFile("restore", newest.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.PostSystemBackupRestoreUpload(ctx, &form, w.FormDataContentType()); err != nil {
		t.Fatalf("restoring from an upload: %v", err)
	}
	// the same backup restored where it lies, by its id, and one that is not there is not found
	if _, err := sdk.PostSystemBackupRestoreById(ctx, newest.Id); err != nil {
		t.Fatalf("restoring backup %d: %v", newest.Id, err)
	}
	if _, err := sdk.PostSystemBackupRestoreById(ctx, 999999); !client.IsNotFound(err) {
		t.Errorf("restoring a backup that is not there = %v, want not found", err)
	}
	// and with no form, Prowlarr's 500 saying so, as the document would
	// have every client send it
	if _, err := sdk.PostSystemBackupRestoreUpload(ctx, nil, ""); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "Content-Type") {
		t.Errorf("a restore with no form = %v, want Prowlarr's 500 about the Content-Type", err)
	}

	down := func(what string, stop func() error) {
		t.Helper()

		before, err := sdk.GetSystemStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stop(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		deadline := time.Now().Add(2 * time.Minute)
		for {
			after, err := sdk.GetSystemStatus(ctx)
			if err == nil && !after.Answer.StartTime.Equal(before.Answer.StartTime.Time) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("Prowlarr did not come back within two minutes of the %s (last: %v)", what, err)
			}
			time.Sleep(2 * time.Second)
		}
	}
	down("shutdown", func() error { _, err := sdk.PostSystemShutdown(ctx); return err })
	down("restart", func() error { _, err := sdk.PostSystemRestart(ctx); return err })

	// the restore took effect: what the tests left is still there
	if _, err := sdk.GetIndexerById(ctx, fx.torrent); err != nil {
		t.Errorf("after the restore and the restart the torrent indexer answers %v", err)
	}
}
