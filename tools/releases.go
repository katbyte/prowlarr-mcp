package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// searchTypes are the kinds of search release_search takes, and the Newznab
// function each one asks the indexers for.
var searchTypes = map[string]string{
	"":       "search",
	"search": "search",
	"tv":     "tvsearch",
	"movie":  "movie",
	"music":  "music",
	"book":   "book",
}

// releaseRow is a release as a search found it.
type releaseRow struct {
	Title      string   `json:"title"`
	Indexer    string   `json:"indexer"`
	IndexerID  int      `json:"indexer_id"`
	GUID       string   `json:"guid"                jsonschema:"with indexer_id, what a grab of it takes"`
	Protocol   string   `json:"protocol"`
	Size       int64    `json:"size"                jsonschema:"bytes"`
	SizeText   string   `json:"size_text"`
	AgeHours   float64  `json:"age_hours"`
	Published  string   `json:"published,omitempty"`
	Seeders    *int     `json:"seeders,omitempty"`
	Leechers   *int     `json:"leechers,omitempty"`
	Grabs      int      `json:"grabs,omitempty"`
	Categories []string `json:"categories"`
	Flags      []string `json:"flags,omitempty"     jsonschema:"what the indexer marks it as: freeleech, internal, scene..."`
	IMDBID     int      `json:"imdb_id,omitempty"`
	TMDBID     int      `json:"tmdb_id,omitempty"`
	TVDBID     int      `json:"tvdb_id,omitempty"`
	InfoURL    string   `json:"info_url,omitempty"`
}

func projectRelease(rel *prowlarr.ReleaseResource) releaseRow {
	row := releaseRow{
		Title: outsideName(rel.Title), Indexer: rel.Indexer, IndexerID: int(rel.IndexerId), GUID: rel.Guid, Protocol: string(rel.Protocol),
		Size: rel.Size, SizeText: humanSize(rel.Size), AgeHours: float64(int(rel.AgeHours*10)) / 10, Published: shownTime(rel.PublishDate),
		Grabs: rel.Grabs, Flags: rel.IndexerFlags, IMDBID: rel.ImdbId, TMDBID: rel.TmdbId, TVDBID: rel.TvdbId, InfoURL: rel.InfoUrl,
	}
	if rel.Protocol == prowlarr.DownloadProtocolTorrent {
		row.Seeders, row.Leechers = new(rel.Seeders), new(rel.Leechers)
	}
	// a site's own categories ride along unnamed beside the standard ones
	// they map to; the standard names are the ones worth reading
	for _, c := range rel.Categories {
		if c.Name != "" && !slices.Contains(row.Categories, c.Name) {
			row.Categories = append(row.Categories, c.Name)
		}
	}

	return row
}

// foundFor is how long a release a search turned up can be downloaded: the half hour Prowlarr itself keeps one for a grab.
const foundFor = 30 * time.Minute

// maxDownload is the largest file release_download hands back, since the whole file rides in one answer. A torrent is rarely a tenth of it.
const maxDownload = 8 << 20

// foundKey names a release the way a grab does: by its indexer and its guid.
type foundKey struct {
	indexer prowlarr.IndexerID
	guid    string
}

// foundRelease is what a download needs of a release a search turned up: its name, and the addresses Prowlarr fetches its file and its magnet link
// through.
type foundRelease struct {
	title    string
	infoHash string
	file     string
	magnet   string
	at       time.Time
}

// foundReleases remembers what the searches of the last half hour turned up, so release_download can be given a guid as release_grab is. The
// addresses carry Prowlarr's API key, which is why they are kept here and never shown.
type foundReleases struct {
	mu       sync.Mutex
	releases map[foundKey]foundRelease
}

// remember keeps a search's releases, and lets go of the ones found too long ago to download.
func (f *foundReleases) remember(found []prowlarr.ReleaseResource) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	for key, rel := range f.releases {
		if now.Sub(rel.at) > foundFor {
			delete(f.releases, key)
		}
	}

	if f.releases == nil {
		f.releases = map[foundKey]foundRelease{}
	}
	for i := range found {
		rel := &found[i]
		f.releases[foundKey{rel.IndexerId, rel.Guid}] = foundRelease{title: rel.Title, infoHash: rel.InfoHash, file: rel.DownloadUrl, magnet: rel.MagnetUrl, at: now}
	}
}

// get is a release a search turned up in the last half hour, and whether there is one.
func (f *foundReleases) get(indexer prowlarr.IndexerID, guid string) (foundRelease, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rel, ok := f.releases[foundKey{indexer, guid}]

	return rel, ok && time.Since(rel.at) <= foundFor
}

// downloadOptions reads what Prowlarr's download call takes out of the address a search gave for a release, leaving the API key in it behind.
func downloadOptions(address, title string) (prowlarr.GetIndexerByIdDownloadOptions, error) {
	u, err := url.Parse(address)
	if err != nil || u.Query().Get("link") == "" {
		return prowlarr.GetIndexerByIdDownloadOptions{}, errors.New("the address Prowlarr gave for this release is not one of its own download links, so there is nothing to fetch it through")
	}

	return prowlarr.GetIndexerByIdDownloadOptions{Link: u.Query().Get("link"), File: cmp.Or(u.Query().Get("file"), title)}, nil
}

// redirectsHandedBack is the client with a redirect handed back as the answer where it would be refused. It is for the download alone: Prowlarr
// answers one for a release with no file by sending its caller on to the magnet link.
func (r *registry) redirectsHandedBack() *prowlarr.Client {
	base := r.client.Client
	if base == nil || base.HTTPClient == nil {
		return r.client
	}

	handsBack, sender := *base, *base.HTTPClient
	sender.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	handsBack.HTTPClient = &sender

	return &prowlarr.Client{Client: &handsBack}
}

// sentOnTo is where an answer sent its caller on to, "" when it did not.
func sentOnTo(res *http.Response) string {
	if res == nil || res.StatusCode < http.StatusMultipleChoices || res.StatusCode >= http.StatusBadRequest {
		return ""
	}

	return res.Header.Get("Location")
}

// fileName is the name a downloaded torrent goes out under: the one Prowlarr gave it, or the release's own, made safe to show and with nothing in
// it that reads as a folder.
func fileName(given, title string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(outsideName(cmp.Or(given, title+".torrent")))
}

func registerReleaseTools(r *registry) {
	pc := r.client

	type searchIn struct {
		Query      string   `json:"query,omitempty"       jsonschema:"what to look for; empty lists what the indexers have newest (their RSS)"`
		Type       string   `json:"type,omitempty"        jsonschema:"search (default), tv, movie, music or book: the indexers that support the kind are asked the way an application would ask them"`
		Categories []string `json:"categories,omitempty"  jsonschema:"only these categories, by number or name: 2000 Movies, 3000 Audio, 5000 TV, 7000 Books, or a subcategory such as Movies/HD"`
		Indexers   []string `json:"indexers,omitempty"    jsonschema:"only these indexers, by name or id; default every enabled one"`
		Protocol   string   `json:"protocol,omitempty"    jsonschema:"only usenet or only torrent results"`
		MinSeeders int      `json:"min_seeders,omitempty" jsonschema:"torrents with fewer seeders are left out"`
		Sort       string   `json:"sort,omitempty"        jsonschema:"seeders, age (newest first), size or grabs; default seeders for torrents, then age"`
		pageIn
	}
	type searchOut struct {
		pageOut
		ByIndexer map[string]int `json:"by_indexer" jsonschema:"how many results each indexer answered with, across every page; an indexer asked that is not here found nothing or failed"`
		Releases  []releaseRow   `json:"releases"   jsonschema:"the indexers are asked again for every page, so what they answer may have moved between one and the next"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "release_search",
		Description: "Search the indexers through Prowlarr, as an application would: by words and kind (tv, movie, music, book), narrowed to categories, indexers or protocol, sorted by seeders, age, size or grabs. Each release carries the guid and indexer a grab of it needs. Every indexer asked records the query in its history and statistics.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		kind, ok := searchTypes[strings.ToLower(in.Type)]
		if !ok {
			return nil, searchOut{}, fmt.Errorf("type must be search, tv, movie, music or book, got %q", in.Type)
		}
		opts := prowlarr.GetSearchOptions{Query: in.Query, Type: kind, Limit: new(100)}
		cats, err := r.categoryIDs(ctx, in.Categories)
		if err != nil {
			return nil, searchOut{}, err
		}
		opts.Categories = cats
		if len(in.Indexers) > 0 {
			var idx []prowlarr.IndexerResource
			if idx, err = r.resolveIndexers(ctx, in.Indexers); err != nil {
				return nil, searchOut{}, err
			}
			for _, i := range idx {
				opts.IndexerIds = append(opts.IndexerIds, i.Id)
			}
		}
		res, err := pc.GetSearch(ctx, opts)
		if err != nil {
			return nil, searchOut{}, err
		}
		r.found.remember(res.Answer)

		out := searchOut{ByIndexer: map[string]int{}}
		var rows []releaseRow
		for i := range res.Answer {
			rel := &res.Answer[i]
			if in.Protocol != "" && !strings.EqualFold(string(rel.Protocol), in.Protocol) {
				continue
			}
			if in.MinSeeders > 0 && rel.Protocol == prowlarr.DownloadProtocolTorrent && rel.Seeders < in.MinSeeders {
				continue
			}
			out.ByIndexer[rel.Indexer]++
			rows = append(rows, projectRelease(rel))
		}
		if err := sortReleases(rows, in.Sort); err != nil {
			return nil, searchOut{}, err
		}
		out.Releases, out.pageOut = paged(rows, in.Limit, in.Offset, defaultLimit)

		return nil, out, nil
	})

	type grabIn struct {
		Indexer        string   `json:"indexer"                   jsonschema:"the releases' indexer, by name or id (release_search's indexer_id)"`
		GUID           string   `json:"guid,omitempty"            jsonschema:"the release's guid from release_search, within half an hour of the search"`
		GUIDs          []string `json:"guids,omitempty"           jsonschema:"several releases' guids from one indexer, grabbed in one call"`
		DownloadClient string   `json:"download_client,omitempty" jsonschema:"the download client to send them to, by name or id; default the indexer's own, then the first that takes its protocol"`
	}
	type grabOut struct {
		Grabbed []string `json:"grabbed" jsonschema:"what was sent, by name where the history records one and by guid where not"`
		Indexer string   `json:"indexer"`
		Note    string   `json:"note"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "release_grab",
		Description: "Send a release found by release_search, or several from one indexer at once, to a download client, the way Prowlarr's own search page does: Prowlarr fetches it from the indexer and hands it over, and the grab shows in the history and statistics. The release must come from a search in the last half hour.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in grabIn) (*mcp.CallToolResult, grabOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, grabOut{}, err
		}
		var guids []string
		for _, guid := range append([]string{in.GUID}, in.GUIDs...) {
			if guid = strings.TrimSpace(guid); guid != "" && !slices.Contains(guids, guid) {
				guids = append(guids, guid)
			}
		}
		if len(guids) == 0 {
			return nil, grabOut{}, errors.New("give the release's guid, or several guids, from release_search")
		}
		var sendTo prowlarr.DownloadClientID
		if in.DownloadClient != "" {
			dc, dcErr := r.resolveDownloadClient(ctx, in.DownloadClient)
			if dcErr != nil {
				return nil, grabOut{}, dcErr
			}
			sendTo = dc.Id
		}
		releases := make([]prowlarr.ReleaseResource, 0, len(guids))
		for _, guid := range guids {
			releases = append(releases, prowlarr.ReleaseResource{IndexerId: i.Id, Guid: guid, DownloadClientId: sendTo})
		}
		// one goes as Prowlarr's search page sends one; several go as its
		// bulk grab sends them, in one call
		if len(releases) == 1 {
			_, err = pc.PostSearch(ctx, releases[0])
		} else {
			_, err = pc.PostSearchBulk(ctx, releases)
		}
		if err != nil {
			return nil, grabOut{}, err
		}
		// the answer is the releases as sent, guids and an indexer; the
		// grabs' own records in the history name what was fetched
		out := grabOut{Grabbed: slices.Clone(guids), Indexer: i.Name, Note: "sent to the download client; history_list event=grab shows them"}
		hist, err := pc.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOptions{IndexerId: i.Id, EventType: prowlarr.HistoryEventTypeReleaseGrabbed, Limit: new(len(guids) + 5)})
		if err == nil {
			events := hist.Answer
			slices.SortStableFunc(events, func(a, b prowlarr.HistoryResource) int { return b.Date.Compare(a.Date.Time) })
			for n := 0; n < len(guids) && n < len(events); n++ {
				if title := data(&events[n], "grabTitle"); title != "" {
					out.Grabbed[len(guids)-1-n] = outsideName(title)
				}
			}
		}

		return nil, out, nil
	})

	type downloadIn struct {
		Indexer string `json:"indexer" jsonschema:"the release's indexer, by name or id (release_search's indexer_id)"`
		GUID    string `json:"guid"    jsonschema:"the release's guid from release_search, within half an hour of the search"`
	}
	type downloadOut struct {
		Title    string `json:"title"`
		Indexer  string `json:"indexer"`
		File     string `json:"file,omitempty"      jsonschema:"the name of the file attached to this answer"`
		MIMEType string `json:"mime_type,omitempty" jsonschema:"what kind of file it is: application/x-bittorrent for a torrent"`
		Size     int    `json:"size,omitempty"      jsonschema:"the file's size in bytes"`
		InfoHash string `json:"info_hash,omitempty" jsonschema:"the torrent's hash, where the indexer gave one"`
		Magnet   string `json:"magnet,omitempty"    jsonschema:"the magnet link of a release that has no file, as the indexer gave it; nothing is attached"`
		Note     string `json:"note"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "release_download",
		Description: "Fetch the .torrent file of a release found by release_search and hand it back over this connection, attached to the answer as a file, where release_grab sends it to a download client. A release with no file answers its magnet link instead. Prowlarr fetches it from the indexer, so it counts against the indexer's grab limit and shows in the history and statistics as a grab. The release must come from a search in the last half hour. Usenet releases cannot be fetched this way: release_grab takes those.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in downloadIn) (*mcp.CallToolResult, downloadOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, downloadOut{}, err
		}

		rel, ok := r.found.get(i.Id, strings.TrimSpace(in.GUID))
		if !ok {
			return nil, downloadOut{}, fmt.Errorf("no search in the last half hour found a release with that guid on %s: find it with release_search, and give its guid as it came", i.Name)
		}

		opts, err := downloadOptions(cmp.Or(rel.file, rel.magnet), rel.title)
		if err != nil {
			return nil, downloadOut{}, err
		}
		out := downloadOut{Title: outsideName(rel.title), Indexer: i.Name, InfoHash: rel.infoHash}

		res, err := r.redirectsHandedBack().GetIndexerByIdDownload(ctx, i.Id, opts)
		to := sentOnTo(res.HttpResponse)
		if strings.HasPrefix(strings.ToLower(to), "magnet:") {
			out.Magnet, out.Note = to, "this release has no file, only a magnet link, which a torrent client takes as it is; the history records a grab"

			return nil, out, nil
		}
		if to != "" {
			return nil, downloadOut{}, fmt.Errorf("%s sends downloads straight to the site rather than fetching them (its redirect setting, which usenet indexers always have on), so Prowlarr has no file to hand over: release_grab sends the release to a download client, or indexer_edit redirect=false has Prowlarr fetch a torrent indexer's files itself", i.Name)
		}
		if err != nil {
			return nil, downloadOut{}, fmt.Errorf("fetching the release from %s: %w", i.Name, err)
		}

		// the length is checked before anything is read, and again as it is read for an answer that named none
		if res.ContentLength() > maxDownload {
			body, _ := res.Reader()
			_ = body.Close()

			return nil, downloadOut{}, fmt.Errorf("the file is %s, more than the %s one answer carries: release_grab sends the release to a download client instead", humanSize(res.ContentLength()), humanSize(maxDownload))
		}
		data, err := res.Bytes(maxDownload)
		if _, tooLarge := errors.AsType[*client.TooLargeError](err); tooLarge {
			return nil, downloadOut{}, fmt.Errorf("the file is more than the %s one answer carries: release_grab sends the release to a download client instead", humanSize(maxDownload))
		}
		if err != nil {
			return nil, downloadOut{}, fmt.Errorf("fetching the release from %s: %w", i.Name, err)
		}

		mimeType, _, _ := mime.ParseMediaType(res.HttpResponse.Header.Get("Content-Type"))
		out.MIMEType = cmp.Or(mimeType, "application/octet-stream")
		out.File, out.Size = fileName(res.Filename(), rel.title), len(data)
		out.Note = "the file is attached to this answer; Prowlarr fetched it from the indexer, and the history records a grab"

		// the answer is said in words as well, for a client that reads only what is beside the file
		said, err := json.Marshal(out)
		if err != nil {
			return nil, downloadOut{}, err
		}
		file := &mcp.ResourceContents{URI: "prowlarr://release/" + strconv.Itoa(int(i.Id)) + "/" + url.PathEscape(out.File), MIMEType: out.MIMEType, Blob: data}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(said)}, &mcp.EmbeddedResource{Resource: file}}}, out, nil
	})
}

// sortReleases orders releases the way a person picks one: most seeders for
// torrents (usenet has none, and sorts by age), newest, largest or most
// grabbed.
func sortReleases(rows []releaseRow, by string) error {
	seeders := func(r releaseRow) int {
		if r.Seeders == nil {
			return -1
		}
		return *r.Seeders
	}
	var f func(a, b releaseRow) int
	switch strings.ToLower(by) {
	case "", "seeders":
		f = func(a, b releaseRow) int {
			if c := cmp.Compare(seeders(b), seeders(a)); c != 0 {
				return c
			}
			return cmp.Compare(a.AgeHours, b.AgeHours)
		}
	case "age":
		f = func(a, b releaseRow) int { return cmp.Compare(a.AgeHours, b.AgeHours) }
	case "size":
		f = func(a, b releaseRow) int { return cmp.Compare(b.Size, a.Size) }
	case "grabs":
		f = func(a, b releaseRow) int { return cmp.Compare(b.Grabs, a.Grabs) }
	default:
		return fmt.Errorf("sort must be seeders, age, size or grabs, got %q", by)
	}
	slices.SortStableFunc(rows, f)

	return nil
}
