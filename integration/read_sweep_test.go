//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/pandorest/config"
	"github.com/katbyte/pandorest/definitions"
	"github.com/katbyte/pandorest/sweep"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// Every GET Prowlarr documents, called with arguments from the fixtures, and
// held to answering with something its model keeps. The sweep is pandorest's
// (sweep), which says what a Case means.
func TestReadSweep(t *testing.T) {
	need(t)

	// the definitions the SDK was generated from: the highest release checked in
	at, err := config.Service{Name: "prowlarr"}.In("..").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	defs, err := definitions.Load(at.Path(at.Definitions))
	if err != nil {
		t.Fatal(err)
	}

	// a download link, as Prowlarr hands one to an application: its own
	// download route, carrying the indexer's link and the file's name
	found, err := sdk.GetSearch(ctx, prowlarr.GetSearchOperationOptions{Query: "dune", IndexerIds: []int{fx.torrent}, Limit: new(10)})
	if err != nil || len(found.Model) == 0 {
		t.Fatalf("searching for something to download: %v", err)
	}
	link, err := url.Parse(found.Model[0].DownloadUrl)
	if err != nil {
		t.Fatal(err)
	}
	download := map[string]any{"Link": link.Query().Get("link"), "File": link.Query().Get("file")}

	// a command and a task to read back, a backup and a log file that exist,
	// and releases for the update server to list
	command := runCommand(t, map[string]any{"name": "CheckHealth"})
	runCommand(t, map[string]any{"name": "Backup"})
	status, err := sdk.GetSystemStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home.Offer(arrserver.Update{Version: "99.0.0.1", New: []string{"a new thing"}}, arrserver.Update{Version: status.Model.Version})
	t.Cleanup(func() { home.Offer() })
	tasks, err := sdk.GetSystemTask(ctx)
	if err != nil || len(tasks.Model) == 0 {
		t.Fatalf("tasks: %v", err)
	}
	files, err := sdk.GetLogFile(ctx)
	if err != nil || len(files.Model) == 0 {
		t.Fatalf("log files: %v", err)
	}

	fixtures := sweep.Fixtures{
		Path: map[string]string{
			"id":                itoa(fx.torrent), // the newznab and download routes, which have no prefix
			"indexer/id":        itoa(fx.torrent),
			"applications/id":   itoa(fx.app),
			"appprofile/id":     itoa(fx.profile),
			"customfilter/id":   itoa(fx.filter),
			"downloadclient/id": itoa(fx.client),
			"indexerproxy/id":   itoa(fx.proxy),
			"notification/id":   itoa(fx.webhook),
			"tag/id":            itoa(fx.tag),
			"detail/id":         itoa(fx.tag),
			"command/id":        itoa(command.Id),
			"task/id":           itoa(tasks.Model[0].Id),
			"host/id":           "1", // the settings sections are single, and ignore the id
			"ui/id":             "1",
			"development/id":    "1",
			"file/filename":     files.Model[0].Filename,
			"update/filename":   "upgrade.txt",
		},
	}

	cases := map[string]sweep.Case{
		// the feeds and the file need to be told what to fetch
		"GetByIdApi":             {Options: map[string]any{"T": "caps"}},
		"GetIndexerByIdNewznab":  {Options: map[string]any{"T": "caps"}},
		"GetByIdDownload":        {Options: download},
		"GetIndexerByIdDownload": {Options: download},
		"GetFileSystem":          {Options: map[string]any{"Path": "/config"}},
		"GetFileSystemType":      {Options: map[string]any{"Path": "/config"}},
		"GetHistorySince":        {Options: map[string]any{"Date": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}},
		"GetSearch":              {Options: map[string]any{"Query": "dune", "IndexerIds": itoa(fx.torrent)}},
		"GetHistoryIndexer":      {Options: map[string]any{"IndexerId": itoa(fx.torrent), "Limit": "5"}},

		// nothing here has gone wrong for long enough to be held back
		"GetIndexerStatus": {Empty: "no indexer of the suite's has failed, so Prowlarr holds none back; the tool suite reads it with something in it, for audit_failing"},

		// the container has never upgraded, so there is no log of one
		"GetLogFileUpdate": {Empty: "the container is never upgraded in place, so it keeps no upgrade logs"},
		"GetLogFileUpdateByFilename": {
			Status: http.StatusNotFound,
			Why:    "the container is never upgraded in place, so it keeps no upgrade logs",
		},
	}

	sweep.Sweep{Definitions: defs, Client: sdk, StatusCode: client.StatusCode, Fixtures: fixtures, Cases: cases}.Run(t)
}

func itoa(n int) string { return strconv.Itoa(n) }
