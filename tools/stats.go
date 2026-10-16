package tools

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	mcpregistry "github.com/katbyte/go-kt/mcp/registry"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// historyRow is one event in Prowlarr's history: a query an application or a
// person ran against an indexer, an RSS sync, a login, or a grab.
type historyRow struct {
	Date       string `json:"date"`
	Indexer    string `json:"indexer"`
	Event      string `json:"event"                 jsonschema:"query, rss, grab, auth or info"`
	Successful bool   `json:"successful"`
	Query      string `json:"query,omitempty"`
	QueryType  string `json:"query_type,omitempty"  jsonschema:"search, tvsearch, movie, music or book"`
	Categories string `json:"categories,omitempty"`
	Source     string `json:"source,omitempty"      jsonschema:"who asked: an application's user agent, or Prowlarr for a search from its own page"`
	Host       string `json:"host,omitempty"`
	Results    *int   `json:"results,omitempty"     jsonschema:"releases the query found"`
	ElapsedMs  *int   `json:"elapsed_ms,omitempty"`
	Cached     bool   `json:"cached,omitempty"      jsonschema:"answered from Prowlarr's cache rather than the indexer"`
	Title      string `json:"title,omitempty"       jsonschema:"the release grabbed"`
	GrabMethod string `json:"grab_method,omitempty" jsonschema:"Proxy (fetched through Prowlarr) or Redirect (straight from the indexer)"`
	URL        string `json:"url,omitempty"         jsonschema:"the request made, with any key in it removed"`
}

// eventNames are the short names history rows use for Prowlarr's event types.
var eventNames = map[prowlarr.HistoryEventType]string{
	prowlarr.HistoryEventTypeIndexerQuery:   "query",
	prowlarr.HistoryEventTypeIndexerRss:     "rss",
	prowlarr.HistoryEventTypeReleaseGrabbed: "grab",
	prowlarr.HistoryEventTypeIndexerAuth:    "auth",
	prowlarr.HistoryEventTypeIndexerInfo:    "info",
}

// eventCodes are the numbers the history list filters event types by.
var eventCodes = map[string]int{"grab": 1, "query": 2, "rss": 3, "auth": 4, "info": 5}

// data reads a history event's data by key, ignoring case.
func data(h *prowlarr.HistoryResource, key string) string {
	for k, v := range h.Data {
		if strings.EqualFold(k, key) {
			return v
		}
	}

	return ""
}

func dataInt(h *prowlarr.HistoryResource, key string) *int {
	n, err := strconv.Atoi(data(h, key))
	if err != nil {
		return nil
	}

	return &n
}

func projectHistory(h *prowlarr.HistoryResource, names map[prowlarr.IndexerID]string) historyRow {
	row := historyRow{
		Date: shownTime(h.Date), Indexer: names[h.IndexerId], Event: eventNames[h.EventType], Successful: h.Successful,
		Query: outsideName(data(h, "query")), QueryType: data(h, "queryType"), Categories: data(h, "categories"),
		Source: data(h, "source"), Host: data(h, "host"), Results: dataInt(h, "queryResults"), ElapsedMs: dataInt(h, "elapsedTime"),
		Cached: data(h, "cached") == "1", Title: outsideName(data(h, "grabTitle")), GrabMethod: data(h, "grabMethod"), URL: redactURL(data(h, "url")),
	}
	if row.Indexer == "" {
		row.Indexer = fmt.Sprintf("id %d", h.IndexerId)
	}
	if row.Event == "" {
		row.Event = string(h.EventType)
	}

	return row
}

// redactURL takes the credentials out of a request URL: an indexer's search
// URL carries its API key or passkey in the query string.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return raw
	}
	q := u.Query()
	changed := false
	for k := range q {
		switch strings.ToLower(k) {
		case "apikey", "api_key", "passkey", "key", "token", "rsskey", "authkey", "pid", "uid", "auth", "secret", "cookie", "hash", "user", "username", "password", "pass":
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	u.User = nil

	return u.String()
}

func registerStatsTools(r *registry) {
	pc := r.client

	type historyIn struct {
		Indexer    string `json:"indexer,omitempty"    jsonschema:"only this indexer, by name or id"`
		Event      string `json:"event,omitempty"      jsonschema:"only this kind: query, rss, grab, auth or info"`
		Successful *bool  `json:"successful,omitempty" jsonschema:"false for only the failures, true for only the successes"`
		Days       int    `json:"days,omitempty"       jsonschema:"how far back, default 7"`
		Source     string `json:"source,omitempty"     jsonschema:"only events one application or client caused, matched against its user agent, e.g. Sonarr"`
		pageIn
	}
	type historyOut struct {
		pageOut
		Events []historyRow `json:"events" jsonschema:"newest first; total counts the events matching within the days asked for, of at most 5000 read"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "history_list",
		Description: "Prowlarr's history, newest first: every query an application or a person ran against an indexer, every RSS sync, login and grab, whether it worked, how long it took and how many releases it found. Filter by indexer, kind, success, application and days back; successful=false is the list of what has been failing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in historyIn) (*mcp.CallToolResult, historyOut, error) {
		all, err := r.indexers(ctx)
		if err != nil {
			return nil, historyOut{}, err
		}
		names := indexerNames(all)
		opts := prowlarr.GetHistoryOptions{PageSize: new(250), SortKey: "date", SortDirection: prowlarr.SortDirectionDescending, Successful: in.Successful}
		if in.Indexer != "" {
			var i *prowlarr.IndexerResource
			if i, err = find(all, in.Indexer, "indexer"); err != nil {
				return nil, historyOut{}, err
			}
			opts.IndexerIds = []prowlarr.IndexerID{i.Id}
		}
		if in.Event != "" {
			code, ok := eventCodes[strings.ToLower(in.Event)]
			if !ok {
				return nil, historyOut{}, fmt.Errorf("event must be query, rss, grab, auth or info, got %q", in.Event)
			}
			opts.EventType = []int{code}
		}
		cutoff := time.Now().AddDate(0, 0, -daysOr(in.Days, 7))
		limit, offset := pageArgs(in.Limit, in.Offset, defaultLimit)
		out := historyOut{}
		err = r.eachHistory(ctx, opts, cutoff, 5000, func(h *prowlarr.HistoryResource) {
			if in.Source != "" && !strings.Contains(strings.ToLower(data(h, "source")), strings.ToLower(in.Source)) {
				return
			}
			out.Total++
			if out.Total > offset && len(out.Events) < limit {
				out.Events = append(out.Events, projectHistory(h, names))
			}
		})
		out.Offset = min(offset, out.Total)
		out.NextOffset = mcpregistry.NextOffset(out.Offset, len(out.Events), out.Total)

		return nil, out, err
	})

	type statsIn struct {
		Days     int    `json:"days,omitempty"     jsonschema:"how far back, default 30"`
		Protocol string `json:"protocol,omitempty" jsonschema:"usenet or torrent"`
		Tag      string `json:"tag,omitempty"      jsonschema:"only indexers carrying this tag"`
		Indexer  string `json:"indexer,omitempty"  jsonschema:"only this indexer, by name or id"`
		pageIn
	}
	type indexerStat struct {
		Indexer           string `json:"indexer"`
		Queries           int    `json:"queries"`
		FailedQueries     int    `json:"failed_queries"`
		FailedPercent     int    `json:"failed_percent"      jsonschema:"failed queries, RSS syncs, logins and grabs as a share of all of them"`
		RSSQueries        int    `json:"rss_queries"`
		AuthQueries       int    `json:"auth_queries"`
		FailedAuthQueries int    `json:"failed_auth_queries"`
		Grabs             int    `json:"grabs"`
		FailedGrabs       int    `json:"failed_grabs"`
		AvgResponseMs     int    `json:"avg_response_ms"`
		AvgGrabMs         int    `json:"avg_grab_ms"`
	}
	type agentStat struct {
		Source  string `json:"source"  jsonschema:"an application's user agent, e.g. Sonarr/4.0.9.2244"`
		Queries int    `json:"queries"`
		Grabs   int    `json:"grabs"`
	}
	type hostStat struct {
		Host    string `json:"host"`
		Queries int    `json:"queries"`
		Grabs   int    `json:"grabs"`
	}
	type statsOut struct {
		Days    int `json:"days"`
		Queries int `json:"queries" jsonschema:"across every indexer counted, not only the ones on this page"`
		Grabs   int `json:"grabs"   jsonschema:"across every indexer counted, not only the ones on this page"`
		pageOut
		Indexers []indexerStat `json:"indexers" jsonschema:"busiest first; total, limit and offset count these"`
		Sources  []agentStat   `json:"sources"  jsonschema:"which applications do the asking, busiest first"`
		Hosts    []hostStat    `json:"hosts"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "indexer_stats",
		Description: "How the indexers have been used over the last days (default 30): queries, RSS syncs, logins and grabs per indexer, how many failed and how long they took, and which applications and hosts did the asking. The numbers the reliability, speed and use audits judge by.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in statsIn) (*mcp.CallToolResult, statsOut, error) {
		days := daysOr(in.Days, 30)
		all, err := r.indexers(ctx)
		if err != nil {
			return nil, statsOut{}, err
		}
		names := indexerNames(all)
		opts := prowlarr.GetIndexerStatsOptions{
			StartDate: prowlarr.Time{Time: time.Now().AddDate(0, 0, -days)},
			EndDate:   prowlarr.Time{Time: time.Now().Add(time.Minute)},
			Protocols: strings.ToLower(in.Protocol),
		}
		if in.Indexer != "" {
			var i *prowlarr.IndexerResource
			if i, err = find(all, in.Indexer, "indexer"); err != nil {
				return nil, statsOut{}, err
			}
			opts.Indexers = []prowlarr.IndexerID{i.Id}
		}
		if in.Tag != "" {
			var ids []prowlarr.TagID
			if ids, err = r.resolveTags(ctx, []string{in.Tag}, false); err != nil {
				return nil, statsOut{}, err
			}
			opts.Tags = ids
		}
		res, err := pc.GetIndexerStats(ctx, opts)
		if err != nil {
			return nil, statsOut{}, err
		}
		out := statsOut{Days: days}
		if res.Answer == nil {
			return nil, out, nil
		}
		for _, s := range res.Answer.Indexers {
			name := s.IndexerName
			if name == "" {
				name = names[s.IndexerId]
			}
			out.Queries += s.NumberOfQueries
			out.Grabs += s.NumberOfGrabs
			out.Indexers = append(out.Indexers, indexerStat{
				Indexer: name, Queries: s.NumberOfQueries, FailedQueries: s.NumberOfFailedQueries, FailedPercent: failedPercent(&s),
				RSSQueries: s.NumberOfRssQueries, AuthQueries: s.NumberOfAuthQueries, FailedAuthQueries: s.NumberOfFailedAuthQueries,
				Grabs: s.NumberOfGrabs, FailedGrabs: s.NumberOfFailedGrabs, AvgResponseMs: s.AverageResponseTime, AvgGrabMs: s.AverageGrabResponseTime,
			})
		}
		slices.SortStableFunc(out.Indexers, func(a, b indexerStat) int {
			return (b.Queries + b.RSSQueries + b.Grabs) - (a.Queries + a.RSSQueries + a.Grabs)
		})
		out.Indexers, out.pageOut = paged(out.Indexers, in.Limit, in.Offset, defaultLimit)
		for _, u := range res.Answer.UserAgents {
			out.Sources = append(out.Sources, agentStat{Source: u.UserAgent, Queries: u.NumberOfQueries, Grabs: u.NumberOfGrabs})
		}
		slices.SortStableFunc(out.Sources, func(a, b agentStat) int { return (b.Queries + b.Grabs) - (a.Queries + a.Grabs) })
		for _, h := range res.Answer.Hosts {
			out.Hosts = append(out.Hosts, hostStat{Host: h.Host, Queries: h.NumberOfQueries, Grabs: h.NumberOfGrabs})
		}
		slices.SortStableFunc(out.Hosts, func(a, b hostStat) int { return (b.Queries + b.Grabs) - (a.Queries + a.Grabs) })

		return nil, out, nil
	})
}

// requests is everything an indexer was asked to do: queries, RSS syncs,
// logins and grabs, which Prowlarr counts separately.
func requests(s *prowlarr.IndexerStatistics) int {
	return s.NumberOfQueries + s.NumberOfRssQueries + s.NumberOfAuthQueries + s.NumberOfGrabs
}

// failures is how many of an indexer's requests failed.
func failures(s *prowlarr.IndexerStatistics) int {
	return s.NumberOfFailedQueries + s.NumberOfFailedRssQueries + s.NumberOfFailedAuthQueries + s.NumberOfFailedGrabs
}

// failedPercent is the share of an indexer's requests that failed.
func failedPercent(s *prowlarr.IndexerStatistics) int { return percent(failures(s), requests(s)) }

// eachHistory walks the history newest first, stopping at the cutoff or
// after max events.
func (r *registry) eachHistory(ctx context.Context, opts prowlarr.GetHistoryOptions, cutoff time.Time, maxEvents int, fn func(*prowlarr.HistoryResource)) error {
	size := 250
	if opts.PageSize != nil && *opts.PageSize > 0 {
		size = *opts.PageSize
	}
	opts.PageSize = &size

	seen := 0
	for page := 1; ; page++ {
		opts.Page = &page
		res, err := r.client.GetHistory(ctx, opts)
		if err != nil {
			return err
		}
		if res.Answer == nil || len(res.Answer.Records) == 0 {
			return nil
		}
		for i := range res.Answer.Records {
			h := &res.Answer.Records[i]
			if t := h.Date.Time; !t.IsZero() && t.Before(cutoff) {
				return nil
			}
			fn(h)
			seen++
			if seen >= maxEvents {
				return nil
			}
		}
		if len(res.Answer.Records) < size || page*size >= res.Answer.TotalRecords {
			return nil
		}
	}
}
