//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
)

// Every GET Prowlarr documents, called with arguments from the fixtures. See
// sweep_test.go for what a sweepCase means.
func TestReadSweep(t *testing.T) {
	need(t)

	// a download link, as Prowlarr hands one to an application: its own
	// download route, carrying the indexer's link and the file's name
	found, err := sdk.GetSearch(ctx, prowlarr.GetSearchOperationOptions{Query: "dune", IndexerIds: []int{fx.torrent}, Limit: 10})
	if err != nil || len(found.Model) == 0 {
		t.Fatalf("searching for something to download: %v", err)
	}
	link, err := url.Parse(found.Model[0].DownloadUrl)
	if err != nil {
		t.Fatal(err)
	}
	download := map[string]any{"Link": link.Query().Get("link"), "File": link.Query().Get("file")}

	// a command and a task to read back, and a log file that exists
	command := runCommand(t, map[string]any{"name": "CheckHealth"})
	tasks, err := sdk.GetSystemTask(ctx)
	if err != nil || len(tasks.Model) == 0 {
		t.Fatalf("tasks: %v", err)
	}
	files, err := sdk.GetLogFile(ctx)
	if err != nil || len(files.Model) == 0 {
		t.Fatalf("log files: %v", err)
	}

	fixtures := sweepFixtures{
		path: map[string]string{
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

	sweep(t, "prowlarr", sdk, fixtures, map[string]sweepCase{
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

		// the container has no internet, so the version it would upgrade to
		// cannot be looked up, and it has never upgraded, so there is no log
		// of one
		"GetUpdate": {
			Status: http.StatusInternalServerError,
			Why:    "the release list cannot be fetched without internet",
		},
		"GetLogFileUpdateByFilename": {
			Status: http.StatusNotFound,
			Why:    "the container is never upgraded in place, so it keeps no upgrade logs",
		},
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
