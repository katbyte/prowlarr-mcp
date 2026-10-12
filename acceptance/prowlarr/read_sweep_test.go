//go:build acceptance

package prowlarr_test

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
	at, err := config.Service{Name: "prowlarr"}.In("../..").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	defs, err := definitions.Load(at.Path(at.Definitions))
	if err != nil {
		t.Fatal(err)
	}

	// a download link, as Prowlarr hands one to an application: its own
	// download route, carrying the indexer's link and the file's name
	found, err := sdk.GetSearch(ctx, prowlarr.GetSearchOptions{Query: "dune", IndexerIds: []prowlarr.IndexerID{fx.torrent}, Limit: new(10)})
	if err != nil || len(found.Answer) == 0 {
		t.Fatalf("searching for something to download: %v", err)
	}
	link, err := url.Parse(found.Answer[0].DownloadUrl)
	if err != nil {
		t.Fatal(err)
	}
	download := map[string]any{"Link": link.Query().Get("link"), "File": link.Query().Get("file")}

	// a command and a task to read back, a backup and a log file that exist,
	// and releases for the update server to list
	command := finished(t)(sdk.CheckHealthAndWait(ctx, prowlarr.CheckHealthCommand{}, commandWait))

	// queued the plain way, by a name in a body, which is how a task the server lists is run
	queued, err := sdk.PostCommand(ctx, []byte(`{"name":"Backup"}`))
	if err != nil || queued.Answer == nil {
		t.Fatalf("queueing a backup = %+v, %v", queued.Answer, err)
	}
	finished(t)(sdk.WaitForCommand(ctx, queued.Answer.Id, commandWait))
	status, err := sdk.GetSystemStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home.Offer(arrserver.Update{Version: "99.0.0.1", New: []string{"a new thing"}}, arrserver.Update{Version: status.Answer.Version})
	t.Cleanup(func() { home.Offer() })
	tasks, err := sdk.GetSystemTask(ctx)
	if err != nil || len(tasks.Answer) == 0 {
		t.Fatalf("tasks: %v", err)
	}
	files, err := sdk.GetLogFile(ctx)
	if err != nil || len(files.Answer) == 0 {
		t.Fatalf("log files: %v", err)
	}

	fixtures := sweep.Fixtures{
		Path: map[string]string{
			"id":                itoa(int(fx.torrent)), // the newznab and download routes, which have no prefix
			"indexer/id":        itoa(int(fx.torrent)),
			"applications/id":   itoa(fx.app),
			"appprofile/id":     itoa(int(fx.profile)),
			"customfilter/id":   itoa(fx.filter),
			"downloadclient/id": itoa(int(fx.client)),
			"indexerproxy/id":   itoa(fx.proxy),
			"notification/id":   itoa(fx.webhook),
			"tag/id":            itoa(int(fx.tag)),
			"detail/id":         itoa(int(fx.tag)),
			"command/id":        itoa(command.Id),
			"task/id":           itoa(tasks.Answer[0].Id),
			"host/id":           "1", // the settings sections are single, and ignore the id
			"ui/id":             "1",
			"development/id":    "1",
			"file/filename":     files.Answer[0].Filename,
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
		"GetHistorySince":        {Options: map[string]any{"Date": prowlarr.Time{Time: time.Now().Add(-time.Hour)}}},
		"GetSearch":              {Options: map[string]any{"Query": "dune", "IndexerIds": itoa(int(fx.torrent))}},
		"GetHistoryIndexer":      {Options: map[string]any{"IndexerId": itoa(int(fx.torrent)), "Limit": "5"}},

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
