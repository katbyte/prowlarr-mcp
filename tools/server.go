package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// healthFix names the audit or tool that deals with each of Prowlarr's own
// health checks, so a health warning leads somewhere.
var healthFix = map[string]string{ //nolint:gosec // health check names, not credentials
	"IndexerCheck":                   "indexer_catalog and indexer_add: no indexer is enabled, so searches find nothing",
	"IndexerStatusCheck":             "audit_failing lists them with their last errors; indexer_test says why",
	"IndexerLongTermStatusCheck":     "audit_failing lists them with their last errors; indexer_test says why",
	"IndexerNoDefinitionCheck":       "audit_definitions: the definition is gone from Prowlarr's catalogue",
	"OutdatedDefinitionCheck":        "audit_definitions: the definition is obsolete; indexer_catalog finds its replacement",
	"IndexerVIPCheck":                "audit_vip: renew the membership, then indexer_edit its vipExpiration",
	"IndexerVIPExpiredCheck":         "audit_vip: renew the membership, then indexer_edit its vipExpiration",
	"IndexerDownloadClientCheck":     "audit_download_clients: the indexer is pinned to a download client that is gone or disabled",
	"IndexerProxyStatusCheck":        "audit_proxies: proxy_test says why",
	"ApplicationStatusCheck":         "audit_apps: app_test says why",
	"ApplicationLongTermStatusCheck": "audit_apps: app_test says why",
	"DownloadClientStatusCheck":      "audit_download_clients: downloadclient_test says why",
	"NotificationStatusCheck":        "notification_test says why",
	"UpdateCheck":                    "server_updates lists what is available",
	"ProxyCheck":                     "Prowlarr's own proxy (Settings > General) cannot be reached",
	"ApiKeyValidationCheck":          "the API key is too short; Prowlarr regenerates it under Settings > General",
	"AllowedHostsCheck":              "server_settings_edit section=host sets allowedHosts to the names and addresses Prowlarr answers to, a comma separated list with no port",
}

func registerServerTools(r *registry) {
	pc := r.client

	type healthRow struct {
		Source  string `json:"source"`
		Type    string `json:"type"               jsonschema:"notice, warning or error"`
		Message string `json:"message"`
		WikiURL string `json:"wiki_url,omitempty"`
		Fix     string `json:"fix,omitempty"      jsonschema:"the audit or tool that deals with it"`
	}
	projectHealth := func(list []prowlarr.HealthResource) []healthRow {
		out := make([]healthRow, 0, len(list))
		for _, h := range list {
			out = append(out, healthRow{Source: h.Source, Type: string(h.Type), Message: outsideSaid(h.Message), WikiURL: h.WikiUrl, Fix: healthFix[h.Source]})
		}
		// errors first, then warnings, then the rest
		rank := map[string]int{"error": 0, "warning": 1, "notice": 2}
		slices.SortStableFunc(out, func(a, b healthRow) int { return rank[a.Type] - rank[b.Type] })

		return out
	}

	type infoOut struct {
		ProwlarrMCPVersion string         `json:"prowlarr_mcp_version"     jsonschema:"the prowlarr-mcp build answering"`
		Version            string         `json:"version"`
		InstanceName       string         `json:"instance_name,omitempty"`
		Branch             string         `json:"branch,omitempty"`
		OS                 string         `json:"os"`
		Docker             bool           `json:"docker"`
		Package            string         `json:"package,omitempty"        jsonschema:"who packaged it and how it updates, e.g. linuxserver.io, docker"`
		Runtime            string         `json:"runtime,omitempty"`
		Database           string         `json:"database,omitempty"`
		URLBase            string         `json:"url_base,omitempty"`
		Authentication     string         `json:"authentication,omitempty"`
		StartTime          string         `json:"start_time,omitempty"`
		Indexers           int            `json:"indexers"`
		IndexersEnabled    int            `json:"indexers_enabled"`
		IndexersFailing    int            `json:"indexers_failing"         jsonschema:"disabled by Prowlarr after failures"`
		ByProtocol         map[string]int `json:"indexers_by_protocol"     jsonschema:"enabled indexers, usenet and torrent"`
		Applications       int            `json:"applications"`
		DownloadClients    int            `json:"download_clients"`
		Proxies            int            `json:"proxies"`
		Health             []healthRow    `json:"health"                   jsonschema:"what Prowlarr's own health checks report, errors first"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_info",
		Description: "Prowlarr's version, platform and database, how many indexers it has (enabled, failing, usenet and torrent), how many applications, download clients and proxies, and what its own health checks report. Start here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, infoOut, error) {
		st, err := pc.GetSystemStatus(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		s := st.Model
		out := infoOut{
			ProwlarrMCPVersion: version.Version,
			Version:            s.Version, InstanceName: s.InstanceName, Branch: s.Branch,
			OS:     strings.TrimSpace(s.OsName + " " + s.OsVersion),
			Docker: boolv(s.IsDocker), Runtime: strings.TrimSpace(s.RuntimeName + " " + s.RuntimeVersion),
			Database: strings.TrimSpace(string(s.DatabaseType) + " " + s.DatabaseVersion),
			URLBase:  s.UrlBase, Authentication: string(s.Authentication), StartTime: s.StartTime,
			ByProtocol: map[string]int{},
		}
		if s.PackageAuthor != "" || s.PackageUpdateMechanism != "" {
			out.Package = strings.TrimSpace(stripMarkdownLink(s.PackageAuthor) + " " + string(s.PackageUpdateMechanism))
		}

		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		failing, err := r.failingNow(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		out.Indexers = len(idx)
		for _, i := range idx {
			if boolv(i.Enable) {
				out.IndexersEnabled++
				out.ByProtocol[string(i.Protocol)]++
			}
			if _, ok := failing[i.Id]; ok {
				out.IndexersFailing++
			}
		}
		apps, err := pc.GetApplications(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		out.Applications = len(apps.Model)
		dcs, err := pc.GetDownloadClient(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		out.DownloadClients = len(dcs.Model)
		proxies, err := pc.GetIndexerProxy(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		out.Proxies = len(proxies.Model)
		health, err := pc.GetHealth(ctx)
		if err != nil {
			return nil, infoOut{}, err
		}
		out.Health = projectHealth(health.Model)

		return nil, out, nil
	})

	type healthOut struct {
		pageOut
		Checks []healthRow `json:"checks" jsonschema:"errors first; empty when Prowlarr has nothing to report"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_health",
		Description: "What Prowlarr's own health checks report - failing indexers and applications, obsolete definitions, expiring VIP, updates - errors first, each with its wiki link and the audit or tool that deals with it. check=true runs the checks again first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Check bool `json:"check,omitempty" jsonschema:"run Prowlarr's health checks again before reading them, rather than reading the last run"`
		pageIn
	},
	) (*mcp.CallToolResult, healthOut, error) {
		if in.Check {
			if _, err := r.runCommand(ctx, "CheckHealth", nil, commandWait); err != nil {
				return nil, healthOut{}, err
			}
		}
		res, err := pc.GetHealth(ctx)
		if err != nil {
			return nil, healthOut{}, err
		}

		page, where := paged(projectHealth(res.Model), in.Limit, in.Offset, defaultLimit)

		return nil, healthOut{pageOut: where, Checks: page}, nil
	})

	type taskRow struct {
		Name            string `json:"name"                    jsonschema:"what task_run takes"`
		Title           string `json:"title"`
		IntervalMinutes int    `json:"interval_minutes"`
		LastRun         string `json:"last_run,omitempty"`
		LastDuration    string `json:"last_duration,omitempty"`
		NextRun         string `json:"next_run,omitempty"`
	}
	type tasksOut struct {
		pageOut
		Tasks []taskRow `json:"tasks"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "task_list",
		Description: "Prowlarr's scheduled tasks - indexer sync to the applications, health checks, backups, definition updates, housekeeping - with how often each runs, when it last ran and for how long, and when it runs next.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, tasksOut, error) {
		res, err := pc.GetSystemTask(ctx)
		if err != nil {
			return nil, tasksOut{}, err
		}
		page, where := paged(res.Model, in.Limit, in.Offset, defaultLimit)
		out := tasksOut{pageOut: where}
		for _, t := range page {
			out.Tasks = append(out.Tasks, taskRow{
				Name: t.TaskName, Title: t.Name, IntervalMinutes: t.Interval,
				LastRun: t.LastExecution, LastDuration: t.LastDuration, NextRun: t.NextExecution,
			})
		}

		return nil, out, nil
	})

	type runIn struct {
		Name string `json:"name"           jsonschema:"the task, as task_list names it: ApplicationIndexerSync, CheckHealth, Backup, IndexerDefinitionUpdate, Housekeeping, CleanUpHistory, ApplicationUpdateCheck"`
		Wait int    `json:"wait,omitempty" jsonschema:"seconds to wait for it to finish, default 60; -1 queues it and returns at once"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "task_run",
		Description: "Run one of Prowlarr's scheduled tasks now - ApplicationIndexerSync pushes the indexers to every application, CheckHealth re-runs the health checks, Backup writes a backup, IndexerDefinitionUpdate fetches the tracker definitions - and wait for it to finish.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, commandOut, error) {
		res, err := pc.GetSystemTask(ctx)
		if err != nil {
			return nil, commandOut{}, err
		}
		names := make([]string, 0, len(res.Model))
		name := ""
		for _, t := range res.Model {
			if strings.EqualFold(t.TaskName, strings.TrimSpace(in.Name)) || strings.EqualFold(t.Name, strings.TrimSpace(in.Name)) {
				name = t.TaskName
			}
			names = append(names, t.TaskName)
		}
		if name == "" {
			slices.Sort(names)
			return nil, commandOut{}, fmt.Errorf("no task %q (have: %s)", in.Name, strings.Join(names, ", "))
		}
		out, err := r.runCommand(ctx, name, nil, waitFor(in.Wait))
		if name == "IndexerDefinitionUpdate" {
			// the catalogue is what it refreshes, so the kept read is stale
			r.forgetCatalog()
		}

		return nil, out, err
	})

	type logFile struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"     jsonschema:"app or update"`
		Modified string `json:"modified"`
	}
	type logsOut struct {
		pageOut
		Files []logFile `json:"files" jsonschema:"newest first; server_log reads one"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_logs",
		Description: "Prowlarr's log files, its own and its updater's, newest first; server_log reads one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, logsOut, error) {
		app, err := pc.GetLogFile(ctx)
		if err != nil {
			return nil, logsOut{}, err
		}
		upd, err := pc.GetLogFileUpdate(ctx)
		if err != nil {
			return nil, logsOut{}, err
		}
		out := logsOut{}
		for _, f := range app.Model {
			out.Files = append(out.Files, logFile{Name: f.Filename, Kind: "app", Modified: f.LastWriteTime})
		}
		for _, f := range upd.Model {
			out.Files = append(out.Files, logFile{Name: f.Filename, Kind: "update", Modified: f.LastWriteTime})
		}
		slices.SortStableFunc(out.Files, func(a, b logFile) int { return strings.Compare(b.Modified, a.Modified) })
		out.Files, out.pageOut = paged(out.Files, in.Limit, in.Offset, defaultLimit)

		return nil, out, nil
	})

	type logIn struct {
		File     string `json:"file,omitempty"     jsonschema:"a log file from server_logs, read from the end; default is Prowlarr's recorded log entries instead"`
		Level    string `json:"level,omitempty"    jsonschema:"recorded entries only: the lowest level to include, info, warn (default) or error"`
		Contains string `json:"contains,omitempty" jsonschema:"only lines or entries containing this text, e.g. an indexer's name"`
		pageIn
	}
	type logEntry struct {
		Time      string `json:"time,omitempty"`
		Level     string `json:"level,omitempty"`
		Logger    string `json:"logger,omitempty"`
		Message   string `json:"message"`
		Exception string `json:"exception,omitempty" jsonschema:"the first line of the exception, when there is one"`
	}
	type logOut struct {
		pageOut
		Entries []logEntry `json:"entries" jsonschema:"newest first; total counts the lines or entries matching among those read, a whole file or the newest 4000 recorded entries"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_log",
		Description: "Read Prowlarr's log: by default its recorded warnings and errors, newest first; with file, the end of one log file from server_logs. contains narrows it to one indexer's or application's lines, which is where the reason an indexer failed is written.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logIn) (*mcp.CallToolResult, logOut, error) {
		limit, offset := pageArgs(in.Limit, in.Offset, defaultLimit)
		out := logOut{}
		if in.File != "" {
			// the newest lines up to the end of the page asked for, oldest
			// first: the page is what is left once the ones skipped are off
			// its end
			lines, total, err := r.logTail(ctx, in.File, in.Contains, offset+limit)
			if err != nil {
				return nil, logOut{}, err
			}
			out.Total, out.Offset = total, min(offset, total)
			for _, line := range slices.Backward(lines[:max(len(lines)-offset, 0)]) {
				out.Entries = append(out.Entries, logEntry{Message: outsideSaid(line)})
			}
			out.NextOffset = nextOffset(out.Offset, len(out.Entries), out.Total)

			return nil, out, nil
		}

		wanted, err := logLevels(in.Level)
		if err != nil {
			return nil, logOut{}, err
		}
		// every page of the newest 4000 is read, so the count is of all that
		// match and not only of the ones answered
		for page := 1; page <= 20; page++ {
			res, err := pc.GetLog(ctx, prowlarr.GetLogOperationOptions{Page: new(page), PageSize: new(200), SortKey: "time", SortDirection: prowlarr.SortDirectionDescending})
			if err != nil {
				return nil, logOut{}, err
			}
			for _, e := range res.Model.Records {
				if !wanted[strings.ToLower(e.Level)] {
					continue
				}
				if in.Contains != "" && !strings.Contains(strings.ToLower(e.Message+" "+e.Exception), strings.ToLower(in.Contains)) {
					continue
				}
				out.Total++
				if out.Total > offset && len(out.Entries) < limit {
					out.Entries = append(out.Entries, logEntry{Time: e.Time, Level: e.Level, Logger: e.Logger, Message: outsideSaid(e.Message), Exception: outsideSaid(firstLine(e.Exception))})
				}
			}
			if len(res.Model.Records) < 200 {
				break
			}
		}
		out.Offset = min(offset, out.Total)
		out.NextOffset = nextOffset(out.Offset, len(out.Entries), out.Total)

		return nil, out, nil
	})

	type updateRow struct {
		Version   string   `json:"version"`
		Released  string   `json:"released,omitempty"`
		Installed bool     `json:"installed"`
		Latest    bool     `json:"latest"`
		New       []string `json:"new"`
		Fixed     []string `json:"fixed"`
	}
	type updatesOut struct {
		Current         string `json:"current"`
		UpdateAvailable bool   `json:"update_available"`
		Mechanism       string `json:"mechanism,omitempty" jsonschema:"how this install is updated; docker means pull a newer image rather than update in place"`
		pageOut
		Updates     []updateRow `json:"updates"               jsonschema:"newest first"`
		Unavailable string      `json:"unavailable,omitempty" jsonschema:"why the list is empty: Prowlarr could not reach its update service"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_updates",
		Description: "The Prowlarr releases newer than, and up to, the one installed, with what each added and fixed, and whether this install is behind.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, updatesOut, error) {
		st, err := pc.GetSystemStatus(ctx)
		if err != nil {
			return nil, updatesOut{}, err
		}
		out := updatesOut{Current: st.Model.Version, Mechanism: string(st.Model.PackageUpdateMechanism)}
		res, err := pc.GetUpdate(ctx)
		if err != nil {
			// Prowlarr asks its update service on every read, and answers a
			// 500 carrying the reason when it cannot reach it
			if se, ok := errors.AsType[*client.StatusError](err); ok && se.StatusCode >= http.StatusInternalServerError {
				out.Unavailable = "Prowlarr could not reach its update service: " + strings.Join(outsideLines(se.Messages), "; ")
				return nil, out, nil
			}
			return nil, updatesOut{}, err
		}
		for _, u := range res.Model {
			row := updateRow{Version: u.Version, Released: u.ReleaseDate, Installed: boolv(u.Installed), Latest: boolv(u.Latest)}
			if u.Changes != nil {
				row.New, row.Fixed = outsideLines(u.Changes.New), outsideLines(u.Changes.Fixed)
			}
			if row.Latest && !row.Installed && u.Version != st.Model.Version {
				out.UpdateAvailable = true
			}
			out.Updates = append(out.Updates, row)
		}
		out.Updates, out.pageOut = paged(out.Updates, in.Limit, in.Offset, defaultLimit)

		return nil, out, nil
	})
}

// failingNow maps the indexers Prowlarr is holding back after failures to
// their status: those it has disabled until a time still to come.
func (r *registry) failingNow(ctx context.Context) (map[int]prowlarr.IndexerStatusResource, error) {
	res, err := r.client.GetIndexerStatus(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := map[int]prowlarr.IndexerStatusResource{}
	for _, s := range res.Model {
		if t := parseTime(s.DisabledTill); !t.IsZero() && t.After(now) {
			out[s.IndexerId] = s
		}
	}

	return out, nil
}

// logLevels is the set of levels at or above the one asked for.
func logLevels(level string) (map[string]bool, error) {
	order := []string{"trace", "debug", "info", "warn", "error", "fatal"}
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" || level == "warning" {
		level = "warn"
	}
	i := slices.Index(order, level)
	if i < 0 {
		return nil, fmt.Errorf("level must be one of %s, got %q", strings.Join(order, ", "), level)
	}
	out := map[string]bool{}
	for _, l := range order[i:] {
		out[l] = true
	}

	return out, nil
}

// logTail reads the last lines of a log file, optionally only those
// containing some text, and counts how many there were in all. A file name
// the server does not list is refused before it is asked for.
func (r *registry) logTail(ctx context.Context, name, contains string, limit int) (lines []string, total int, err error) {
	app, err := r.client.GetLogFile(ctx)
	if err != nil {
		return nil, 0, err
	}
	upd, err := r.client.GetLogFileUpdate(ctx)
	if err != nil {
		return nil, 0, err
	}
	var body io.ReadCloser
	var names []string
	for _, f := range app.Model {
		names = append(names, f.Filename)
		if strings.EqualFold(f.Filename, name) && body == nil {
			res, err := r.client.GetLogFileByFilename(ctx, f.Filename)
			if err != nil {
				return nil, 0, err
			}
			body = res.HttpResponse.Body
		}
	}
	for _, f := range upd.Model {
		names = append(names, f.Filename)
		if strings.EqualFold(f.Filename, name) && body == nil {
			res, err := r.client.GetLogFileUpdateByFilename(ctx, f.Filename)
			if err != nil {
				return nil, 0, err
			}
			body = res.HttpResponse.Body
		}
	}
	if body == nil {
		slices.Sort(names)
		return nil, 0, fmt.Errorf("no log file %q (have: %s)", name, strings.Join(names, ", "))
	}
	defer func() { _ = body.Close() }()

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if contains != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(contains)) {
			continue
		}
		total++
		lines = append(lines, line)
		if len(lines) > limit {
			lines = lines[1:]
		}
	}

	return lines, total, sc.Err()
}

// stripMarkdownLink turns "[linuxserver.io](https://...)" into its text.
func stripMarkdownLink(s string) string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]("); i > 0 {
			return s[1:i]
		}
	}

	return s
}
