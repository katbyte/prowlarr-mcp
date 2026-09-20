package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
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
		Title: rel.Title, Indexer: rel.Indexer, IndexerID: rel.IndexerId, GUID: rel.Guid, Protocol: string(rel.Protocol),
		Size: rel.Size, SizeText: humanSize(rel.Size), AgeHours: float64(int(rel.AgeHours*10)) / 10, Published: rel.PublishDate,
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
		Limit      int      `json:"limit,omitempty"       jsonschema:"results to return, default 25"`
	}
	type searchOut struct {
		Total     int            `json:"total"      jsonschema:"results found, before the limit"`
		ByIndexer map[string]int `json:"by_indexer" jsonschema:"how many results each indexer answered with; an indexer asked that is not here found nothing or failed"`
		Releases  []releaseRow   `json:"releases"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "release_search",
		Description: "Search the indexers through Prowlarr, as an application would: by words and kind (tv, movie, music, book), narrowed to categories, indexers or protocol, sorted by seeders, age, size or grabs. Each release carries the guid and indexer a grab of it needs. Every indexer asked records the query in its history and statistics.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		kind, ok := searchTypes[strings.ToLower(in.Type)]
		if !ok {
			return nil, searchOut{}, fmt.Errorf("type must be search, tv, movie, music or book, got %q", in.Type)
		}
		opts := prowlarr.GetSearchOperationOptions{Query: in.Query, Type: kind, Limit: 100}
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
		out := searchOut{ByIndexer: map[string]int{}}
		var rows []releaseRow
		for i := range res.Model {
			rel := &res.Model[i]
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
		out.Total = len(rows)
		out.Releases = rows[:min(len(rows), limitOr(in.Limit, 25))]

		return nil, out, nil
	})

	type grabIn struct {
		Indexer        string `json:"indexer"                   jsonschema:"the release's indexer, by name or id (release_search's indexer_id)"`
		GUID           string `json:"guid"                      jsonschema:"the release's guid from release_search, within half an hour of the search"`
		DownloadClient string `json:"download_client,omitempty" jsonschema:"the download client to send it to, by name or id; default the indexer's own, then the first that takes its protocol"`
	}
	type grabOut struct {
		Grabbed string `json:"grabbed"`
		Indexer string `json:"indexer"`
		Note    string `json:"note"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "release_grab",
		Description: "Send a release found by release_search to a download client, the way Prowlarr's own search page does: Prowlarr fetches it from the indexer and hands it over, and the grab shows in the history and statistics. The release must come from a search in the last half hour.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in grabIn) (*mcp.CallToolResult, grabOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, grabOut{}, err
		}
		if strings.TrimSpace(in.GUID) == "" {
			return nil, grabOut{}, errors.New("give the release's guid, from release_search")
		}
		body := prowlarr.ReleaseResource{IndexerId: i.Id, Guid: in.GUID}
		if in.DownloadClient != "" {
			var dc *prowlarr.DownloadClientResource
			if dc, err = r.resolveDownloadClient(ctx, in.DownloadClient); err != nil {
				return nil, grabOut{}, err
			}
			body.DownloadClientId = dc.Id
		}
		if _, err := pc.PostSearch(ctx, body); err != nil {
			return nil, grabOut{}, err
		}
		// the answer is the release as sent, a guid and an indexer; the
		// grab's own record in the history names what was fetched
		title := in.GUID
		hist, err := pc.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOperationOptions{IndexerId: i.Id, EventType: prowlarr.HistoryEventTypeReleaseGrabbed, Limit: 5})
		if err == nil {
			events := hist.Model
			slices.SortStableFunc(events, func(a, b prowlarr.HistoryResource) int { return strings.Compare(b.Date, a.Date) })
			if len(events) > 0 && data(&events[0], "grabTitle") != "" {
				title = data(&events[0], "grabTitle")
			}
		}

		return nil, grabOut{Grabbed: title, Indexer: i.Name, Note: "sent to the download client; history_list event=grab shows it"}, nil
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
