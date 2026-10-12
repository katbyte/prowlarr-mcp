//go:build acceptance

package prowlarr_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/go-kt/test/services/newznab"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// The fixtures the suite creates through the SDK, by name, and the fakes
// behind them.
const (
	tagName       = "sdk"
	filterName    = "SDK Filter"
	profileName   = "SDK Profile"
	torrentName   = "SDK Torrent"
	usenetName    = "SDK Usenet"
	clientName    = "SDK Blackhole"
	proxyName     = "SDK Proxy"
	appName       = "SDK Sonarr"
	webhookName   = "SDK Webhook"
	torrentSite   = "sdk-tor"
	usenetSite    = "sdk-nzb"
	siteKey       = "sdkkey"
	fakeSonarr    = "sonarr"
	fakeSonarrKey = "sonarrkey"
)

var releases = []newznab.Release{
	{Title: "Severance.S01E01.1080p.WEB-DL-SDK", Category: newznab.CategoryTVHD, Seeders: 20, Peers: 2, TVDBID: 371980},
	{Title: "Dune.Part.Two.2024.1080p.BluRay-SDK", Category: newznab.CategoryMoviesHD, Seeders: 80, Peers: 4, TMDBID: 693134},
}

// fixtures are the ids of what the suite created.
type fixtures struct {
	tag, filter, profile, torrent, usenet, client, proxy, app, webhook int
}

var (
	ctx   context.Context
	sdk   *prowlarr.Client
	sites *newznab.Server
	apps  *servarr.Server
	// home is the server Prowlarr calls home to, as the suite plays it: the
	// update server, the clock and the notices
	home  = arrserver.New()
	proxy *testenv.Proxy
	fx    fixtures
	ready bool
)

// The ports the fake sites and applications listen on when the environment
// names none.
const (
	defaultIndexerPort = 19791
	defaultAppPort     = 19792
)

func runSuite(m *testing.M) int {
	if !testenv.Configured() {
		return m.Run() // every test skips
	}
	ctx = context.Background()
	p, err := testenv.StartProxy(ctx, home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proxy:", err)
		return 1
	}
	proxy = p
	if err := start(); err != nil {
		fmt.Fprintln(os.Stderr, "acceptance/prowlarr setup:", err)
		stop()
		return 1
	}
	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "acceptance/prowlarr cleanup:", err)
		if code == 0 {
			code = 1
		}
	}
	stop()

	// a request Prowlarr made that the suite does not answer is said loudly
	// even when the assertions happened to survive it
	report := proxy.Report() + home.Report()
	if report != "" {
		fmt.Fprint(os.Stderr, report)
		if code == 0 {
			code = 1
		}
	}

	return code
}

func start() error {
	indexerPort, err := testenv.IndexerPort(defaultIndexerPort)
	if err != nil {
		return err
	}
	appPort, err := testenv.AppPort(defaultAppPort)
	if err != nil {
		return err
	}
	sites, err = newznab.New(newznab.Options{
		Addr: ":" + strconv.Itoa(indexerPort), PublicHost: testenv.Host(),
		Sites: []newznab.Site{
			{Name: torrentSite, Protocol: newznab.Torrent, APIKey: siteKey, Releases: releases},
			{Name: usenetSite, Protocol: newznab.Usenet, APIKey: siteKey, Releases: releases},
		},
	})
	if err != nil {
		return err
	}
	apps, err = servarr.New(servarr.Options{
		Addr: ":" + strconv.Itoa(appPort), PublicHost: testenv.Host(),
		Apps: []servarr.App{{Name: fakeSonarr, Kind: servarr.Sonarr, APIKey: fakeSonarrKey}},
	})
	if err != nil {
		return err
	}
	if sdk, err = prowlarr.New(testenv.Server(), testenv.Token()); err != nil {
		return err
	}
	ready = true

	return seed()
}

func stop() {
	if sites != nil {
		_ = sites.Close()
	}
	if apps != nil {
		_ = apps.Close()
	}
	if err := proxy.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// need skips a test when the container is not configured.
func need(t *testing.T) {
	t.Helper()

	if !ready {
		t.Skip(testenv.NotConfigured)
	}
}

// seed creates the fixtures through the SDK's write methods, each of which
// is generated to expect Prowlarr's real status: 201 for a create.
func seed() error {
	// Prowlarr checks its health as it starts, before the suite's proxy is
	// there to answer its update check, and a check that cannot be answered
	// ends the run of them: so they are run again now that it can
	if err := checkHealth(); err != nil {
		return err
	}

	tag, err := sdk.PostTag(ctx, prowlarr.TagResource{Label: tagName})
	if err != nil {
		return fmt.Errorf("tag: %w", err)
	}
	fx.tag = tag.Model.Id

	// a saved filter, the one thing here only the web interface makes
	filter, err := sdk.PostCustomFilter(ctx, prowlarr.CustomFilterResource{
		Type: "indexerIndex", Label: filterName,
		Filters: []map[string]any{{"key": "protocol", "value": []string{"torrent"}, "type": "equal"}},
	})
	if err != nil {
		return fmt.Errorf("custom filter: %w", err)
	}
	fx.filter = filter.Model.Id

	profile, err := sdk.PostAppProfile(ctx, prowlarr.AppProfileResource{
		Name: profileName, EnableRss: new(true), EnableAutomaticSearch: new(true), EnableInteractiveSearch: new(false), MinimumSeeders: 3,
	})
	if err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	fx.profile = profile.Model.Id

	schema, err := sdk.GetIndexerSchema(ctx)
	if err != nil {
		return fmt.Errorf("indexer schema: %w", err)
	}
	for _, s := range []struct {
		id                   *int
		name, template, site string
	}{{&fx.torrent, torrentName, "Generic Torznab", torrentSite}, {&fx.usenet, usenetName, "Generic Newznab", usenetSite}} {
		i := slices.IndexFunc(schema.Model, func(x prowlarr.IndexerResource) bool { return x.Name == s.template })
		if i < 0 {
			return fmt.Errorf("no %s in the schema", s.template)
		}
		idx := schema.Model[i]
		idx.Name, idx.Enable, idx.AppProfileId, idx.Tags, idx.Presets = s.name, new(true), fx.profile, []int{fx.tag}, nil
		setField(idx.Fields, "baseUrl", sites.Indexer(s.site).URL())
		setField(idx.Fields, "apiKey", siteKey)
		made, postErr := sdk.PostIndexer(ctx, idx, prowlarr.PostIndexerOperationOptions{})
		if postErr != nil {
			return fmt.Errorf("%s: %w", s.name, postErr)
		}
		*s.id = made.Model.Id
	}

	clients, err := sdk.GetDownloadClientSchema(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(clients.Model, func(x prowlarr.DownloadClientResource) bool { return x.Implementation == "TorrentBlackhole" })
	dc := clients.Model[i]
	dc.Name, dc.Enable, dc.Presets = clientName, new(true), nil
	setField(dc.Fields, "torrentFolder", "/downloads/torrent")
	made, err := sdk.PostDownloadClient(ctx, dc, prowlarr.PostDownloadClientOperationOptions{})
	if err != nil {
		return fmt.Errorf("download client: %w", err)
	}
	fx.client = made.Model.Id

	// a proxy with no tags is used by nothing, so Prowlarr does not test it
	proxies, err := sdk.GetIndexerProxySchema(ctx)
	if err != nil {
		return err
	}
	i = slices.IndexFunc(proxies.Model, func(x prowlarr.IndexerProxyResource) bool { return x.Implementation == "Http" })
	px := proxies.Model[i]
	px.Name, px.Presets, px.Tags = proxyName, nil, []int{}
	setField(px.Fields, "host", "proxy.invalid")
	setField(px.Fields, "port", 3128)
	madeProxy, err := sdk.PostIndexerProxy(ctx, px, prowlarr.PostIndexerProxyOperationOptions{})
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	fx.proxy = madeProxy.Model.Id

	appSchema, err := sdk.GetApplicationsSchema(ctx)
	if err != nil {
		return err
	}
	i = slices.IndexFunc(appSchema.Model, func(x prowlarr.ApplicationResource) bool { return x.Implementation == "Sonarr" })
	app := appSchema.Model[i]
	app.Name, app.Presets, app.SyncLevel, app.Tags = appName, nil, prowlarr.ApplicationSyncLevelFullSync, []int{}
	setField(app.Fields, "baseUrl", apps.URL(fakeSonarr))
	setField(app.Fields, "apiKey", fakeSonarrKey)
	setField(app.Fields, "prowlarrUrl", "http://prowlarr:9696")
	madeApp, err := sdk.PostApplications(ctx, app, prowlarr.PostApplicationsOperationOptions{})
	if err != nil {
		return fmt.Errorf("application: %w", err)
	}
	fx.app = madeApp.Model.Id

	// a notification sent for nothing is off, and not tested
	notes, err := sdk.GetNotificationSchema(ctx)
	if err != nil {
		return err
	}
	i = slices.IndexFunc(notes.Model, func(x prowlarr.NotificationResource) bool { return x.Implementation == "Webhook" })
	note := notes.Model[i]
	note.Name, note.Presets = webhookName, nil
	note.OnGrab, note.OnHealthIssue, note.OnHealthRestored, note.OnApplicationUpdate = new(false), new(false), new(false), new(false)
	setField(note.Fields, "url", "http://"+testenv.Host()+":1/webhook")
	madeNote, err := sdk.PostNotification(ctx, note, prowlarr.PostNotificationOperationOptions{})
	if err != nil {
		return fmt.Errorf("notification: %w", err)
	}
	fx.webhook = madeNote.Model.Id

	return nil
}

// cleanup removes the fixtures again, through the SDK's deletes.
func cleanup() error {
	var errs []error
	for _, del := range []func() error{
		func() error { _, err := sdk.DeleteApplicationsById(ctx, fx.app); return err },
		func() error { _, err := sdk.DeleteNotificationById(ctx, fx.webhook); return err },
		func() error { _, err := sdk.DeleteIndexerById(ctx, fx.torrent); return err },
		func() error { _, err := sdk.DeleteIndexerById(ctx, fx.usenet); return err },
		func() error { _, err := sdk.DeleteIndexerProxyById(ctx, fx.proxy); return err },
		func() error { _, err := sdk.DeleteDownloadClientById(ctx, fx.client); return err },
		func() error { _, err := sdk.DeleteAppProfileById(ctx, fx.profile); return err },
		func() error { _, err := sdk.DeleteCustomFilterById(ctx, fx.filter); return err },
		func() error { _, err := sdk.DeleteTagById(ctx, fx.tag); return err },
	} {
		if err := del(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// setField sets a provider setting by name in a template's fields.
func setField(fields []prowlarr.Field, name string, value any) {
	for i := range fields {
		if fields[i].Name == name {
			fields[i].Value = value
		}
	}
}

// field reads a provider setting by name.
func field(fields []prowlarr.Field, name string) any {
	for _, f := range fields {
		if f.Name == name {
			return f.Value
		}
	}

	return nil
}

// runCommand queues a command and waits for it to finish.
func runCommand(t *testing.T, body map[string]any) *prowlarr.CommandResource {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sdk.PostCommand(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	cmd := res.Model
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		got, err := sdk.GetCommandById(ctx, cmd.Id)
		if err != nil {
			t.Fatal(err)
		}
		if cmd = got.Model; cmd.Status != prowlarr.CommandStatusQueued && cmd.Status != prowlarr.CommandStatusStarted {
			return cmd
		}
	}
	t.Fatalf("%v never finished", body)

	return nil
}

// checkHealth runs Prowlarr's health checks and waits for them to finish.
func checkHealth() error {
	res, err := sdk.PostCommand(ctx, []byte(`{"name":"CheckHealth"}`))
	if err != nil {
		return fmt.Errorf("running the health checks: %w", err)
	}
	cmd := res.Model
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		got, err := sdk.GetCommandById(ctx, cmd.Id)
		if err != nil {
			return fmt.Errorf("reading the health checks' command: %w", err)
		}
		if cmd = got.Model; cmd.Status != prowlarr.CommandStatusQueued && cmd.Status != prowlarr.CommandStatusStarted {
			break
		}
	}
	if cmd.Status != prowlarr.CommandStatusCompleted {
		return fmt.Errorf("the health checks ended %s: %s", cmd.Status, cmd.Exception)
	}

	return nil
}

// model is the answer behind p, failing the test when the server sent none.
func model[T any](t *testing.T, p *T) T {
	t.Helper()

	if p == nil {
		t.Fatal("the answer from Prowlarr had nothing in it")
	}

	return *p
}
