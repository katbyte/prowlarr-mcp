// Command pandorest generates the Prowlarr SDK (sdk/prowlarr) from the
// vendored OpenAPI document, with github.com/katbyte/pandorest. What is this
// repository's own is here: the service, and the workarounds for the bugs in
// its document (workarounds).
//
//	import    api-defs/prowlarr-openapi-<release>.json -> workarounds -> api-defs/prowlarr-<release>/*.json
//	generate  api-defs/prowlarr-<release> -> sdk/prowlarr
//	diff      what a refreshed spec changes, against the checked-in definitions
//	check     the definitions match the spec and the package has every method
//	resolve   which document and definitions the service is at
//
// Run from the repository root (the make targets do):
//
//	go run ./sdk/pandorest import
//	go run ./sdk/pandorest generate
//	go run ./sdk/pandorest diff
//	go run ./sdk/pandorest diff -old api-defs/prowlarr-2.6.5.5623 -new api-defs/prowlarr-2.7.0.5700
//
// See docs/README.md for how to refresh the spec.
package main

import (
	"time"

	"github.com/katbyte/pandorest"
	"github.com/katbyte/pandorest/config"
	"github.com/katbyte/prowlarr-mcp/sdk/pandorest/workarounds"
)

// generator is what the SDK is generated from: the one service, and the
// fixes for its document.
var generator = pandorest.Config{
	Services: []config.Service{{
		Name:    "prowlarr",
		Package: "prowlarr",
		Output:  "sdk/prowlarr",
		// the base client is pandorest's own, made for Prowlarr from the
		// service the package declares by hand (sdk/prowlarr/service.go)
		ClientService: "service",
		Auth:          "APIKey",
		Credential:    "API key",
		Naming:        config.PathNaming,
		PathPrefix:    "/api/v1",
		Words:         words,
		// Prowlarr's lists take a page number (from 1) and a page size, and
		// answer the page's records and the total across every page
		Paging:       &config.Paging{Start: "page", Limit: "pageSize", Items: "records", Total: "totalRecords", ByPage: true},
		WrittenWhole: settings,
		// testing every provider of a kind answers 400 with each one's result
		// when any of them fails
		ExpectSameShape: true,
		// an operation with one option takes it as an argument: the switch that saves or tests without asking the server to check first
		InlineSingleOptions: true,
		// Prowlarr declares its dates and sends RFC 3339 in UTC ("2026-09-19T13:00:00Z"), with year one for never, which reads as no date
		Dates: config.Dates{Send: time.RFC3339},
		// the ids that are held apart from their own record and passed between kinds of thing: an indexer's in history, statistics and a search, a profile's on an
		// indexer, a tag's on five kinds of thing, a download client's on a grab. The rest stay plain numbers
		IDs: []config.ID{
			{Name: "IndexerID", Of: []string{"IndexerResource"}, Names: []string{"indexerId", "indexerIds"}, In: []string{"IndexerBulkResource .ids"}, Joined: []string{"indexers"}},
			{Name: "AppProfileID", Of: []string{"AppProfileResource"}, Names: []string{"appProfileId"}},
			{Name: "TagID", Of: []string{"TagResource"}, Names: []string{"tags"}, In: []string{"TagDetailsResource .id"}, Joined: []string{"tags"}},
			{Name: "DownloadClientID", Of: []string{"DownloadClientResource"}, Names: []string{"downloadClientId"}, In: []string{"DownloadClientBulkResource .ids"}},
		},
		// left plain on purpose: other services' ids, which only pass through, a release's and a grab's own text ids, and the id of a thing nothing else refers to
		Plain: []string{"applicationIds", "doubanid", "downloadId", "guid", "id", "ids", "imdbId", "imdbid", "indexerProxyIds", "notificationIds", "rid", "tmdbId", "tmdbid", "traktid", "tvMazeId", "tvdbId", "tvdbid", "tvmazeid"},
	}},
	Workarounds: workarounds.All,
}

// settings are the models callers read and write back whole, whose empty
// strings and zeros are sent rather than left out: Prowlarr reads a setting
// left out as null, which is not the value that was read, and not a value it
// will save.
var settings = []string{
	"DevelopmentConfigResource",
	"DownloadClientConfigResource",
	"HostConfigResource",
	"UiConfigResource",
}

// words spells the segments of Prowlarr's paths that run two words together,
// which name methods and their files.
var words = map[string]string{
	"appprofile":     "AppProfile",
	"customfilter":   "CustomFilter",
	"downloadclient": "DownloadClient",
	"filesystem":     "FileSystem",
	"indexerproxy":   "IndexerProxy",
	"indexerstats":   "IndexerStats",
	"indexerstatus":  "IndexerStatus",
	"testall":        "TestAll",
}

func main() {
	pandorest.Main(generator)
}
