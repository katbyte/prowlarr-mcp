package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The indexer audits: what the indexers' own record says about them - their
// failures, their traffic, their limits - read from Prowlarr's status,
// statistics and history rather than by asking the sites.

func registerIndexerAudits(r *registry, t *auditTable) {
	type failingIn struct {
		Test  bool `json:"test,omitempty"  jsonschema:"also test each failing indexer against its site, to say why it fails; counts against the site's request limits"`
		Limit int  `json:"limit,omitempty" jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_failing",
		Description: "Find the indexers that are failing now: those Prowlarr has stopped using after failures (with how long they have been failing and until when they are held back), and enabled ones whose last query or grab failed. " +
			"test=true tests each against its site to say why. indexer_test and indexer_edit fix them (URL, credentials, proxy tags), or indexer_edit enable=false retires one.",
	}, "indexers held back after failures, or whose last request failed", func(ctx context.Context, s *snapshot, in failingIn) (auditOut, error) {
		out := auditOut{}
		now := time.Now()
		for _, i := range s.enabled() {
			out.Scanned++
			hist, err := s.r.client.GetHistoryIndexer(ctx, prowlarr.GetHistoryIndexerOperationOptions{IndexerId: i.Id, Limit: 25})
			if err != nil {
				return auditOut{}, err
			}
			events := hist.Model
			slices.SortStableFunc(events, func(a, b prowlarr.HistoryResource) int { return strings.Compare(b.Date, a.Date) })
			lastFailed := slices.IndexFunc(events, func(h prowlarr.HistoryResource) bool { return !boolv(h.Successful) })

			f := finding{Subject: i.Name, Kind: "indexer", Fix: "indexer_test says why; indexer_edit fixes its URL, credentials or proxy tags, or enable=false retires it"}
			st := s.statusOf(i.Id)
			switch {
			case st != nil:
				f.Problem = "held_back_after_failures"
				f.Detail = fmt.Sprintf("Prowlarr has stopped using it until %s: failing since %s (%s), most recently %s",
					st.DisabledTill, st.InitialFailure, sinceText(now, parseTime(st.InitialFailure)), st.MostRecentFailure)
			case len(events) > 0 && lastFailed == 0:
				f.Problem = "last_request_failed"
				f.Detail = fmt.Sprintf("its last %s, at %s, failed", eventNames[events[0].EventType], events[0].Date)
			default:
				continue
			}
			if lastFailed >= 0 {
				h := &events[lastFailed]
				f.Detail += fmt.Sprintf("; last failed %s at %s", eventNames[h.EventType], h.Date)
				if u := redactURL(data(h, "url")); u != "" {
					f.Detail += " (" + u + ")"
				}
			}
			if in.Test {
				row, err := s.r.testIndexer(ctx, i)
				switch {
				case err != nil:
					f.Detail += "; testing it failed: " + err.Error()
				case row.OK:
					f.Detail += "; it passes its test now"
				default:
					f.Detail += "; its test says: " + strings.Join(row.Problems, "; ")
				}
			}
			out.report(in.Limit, f)
		}

		return out, nil
	})

	type unreliableIn struct {
		Days        int `json:"days,omitempty"         jsonschema:"how far back to judge, default 30"`
		Threshold   int `json:"threshold,omitempty"    jsonschema:"the failed share, in percent, that makes an indexer unreliable; default 20"`
		MinRequests int `json:"min_requests,omitempty" jsonschema:"requests an indexer must have had to be judged, default 10"`
		Limit       int `json:"limit,omitempty"        jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_unreliable",
		Description: "Find the indexers that fail too often over a window (default 30 days): the share of their queries, RSS syncs, logins and grabs that failed is at or above a threshold (default 20%), worst first, split by kind - failing logins mean credentials or a cookie, failing grabs a download limit or a dead link, failing queries a site that is down or behind Cloudflare. " +
			"indexer_test and indexer_edit fix them, or indexer_edit priority lowers one.",
	}, "indexers failing 20% or more of their requests over 30 days", func(ctx context.Context, s *snapshot, in unreliableIn) (auditOut, error) {
		days, threshold, minReq := daysOr(in.Days, 30), limitOr(in.Threshold, 20), limitOr(in.MinRequests, 10)
		stats, err := s.statsFor(ctx, time.Duration(days)*24*time.Hour)
		if err != nil {
			return auditOut{}, err
		}
		type scored struct {
			f   finding
			pct int
		}
		var rows []scored
		out := auditOut{}
		for _, i := range s.enabled() {
			out.Scanned++
			st := stats[i.Id]
			if st == nil || requests(st) < minReq {
				continue
			}
			pct := failedPercent(st)
			if pct < threshold {
				continue
			}
			parts := []string{}
			worst, worstPct := "queries_failing", -1
			for _, k := range []struct {
				name, problem string
				failed, total int
			}{
				{"queries", "queries_failing", st.NumberOfFailedQueries, st.NumberOfQueries},
				{"RSS syncs", "queries_failing", st.NumberOfFailedRssQueries, st.NumberOfRssQueries},
				{"logins", "logins_failing", st.NumberOfFailedAuthQueries, st.NumberOfAuthQueries},
				{"grabs", "grabs_failing", st.NumberOfFailedGrabs, st.NumberOfGrabs},
			} {
				if k.total == 0 {
					continue
				}
				parts = append(parts, fmt.Sprintf("%d of %d %s", k.failed, k.total, k.name))
				if p := percent(k.failed, k.total); p > worstPct {
					worst, worstPct = k.problem, p
				}
			}
			fix := map[string]string{
				"queries_failing": "indexer_test says why; a site behind Cloudflare needs a FlareSolverr proxy (audit_proxies), one that moved needs indexer_edit base_url",
				"logins_failing":  "the credentials or cookie are stale: indexer_edit settings, then indexer_test",
				"grabs_failing":   "downloads are refused: a grab limit, ratio trouble or dead links; check the site, or indexer_edit grab_limit",
			}[worst]
			rows = append(rows, scored{pct: pct, f: finding{
				Subject: i.Name, Kind: "indexer", Problem: worst,
				Detail: fmt.Sprintf("%d%% of its requests failed over %d days: %s", pct, days, strings.Join(parts, ", ")),
				Fix:    fix,
			}})
		}
		slices.SortStableFunc(rows, func(a, b scored) int { return b.pct - a.pct })
		for _, row := range rows {
			out.report(in.Limit, row.f)
		}

		return out, nil
	})

	type slowIn struct {
		Days        int `json:"days,omitempty"         jsonschema:"how far back to judge, default 30"`
		ThresholdMs int `json:"threshold_ms,omitempty" jsonschema:"the average query time, in milliseconds, that makes an indexer slow; default 5000"`
		MinQueries  int `json:"min_queries,omitempty"  jsonschema:"queries an indexer must have answered to be judged, default 5"`
		Limit       int `json:"limit,omitempty"        jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_slow",
		Description: "Find the slow indexers: those whose queries took, on average over a window (default 30 days), longer than a threshold (default 5 seconds), slowest first, and those whose grabs take twice that. An application searching every indexer waits for the slowest, so one slow site slows every search. " +
			"indexer_edit lowers its priority or moves it to a sync profile without automatic search (profile_create), a proxy may be the cause (audit_proxies), or indexer_edit enable=false.",
	}, "indexers averaging over 5 seconds a query over 30 days", func(ctx context.Context, s *snapshot, in slowIn) (auditOut, error) {
		days, threshold, minQ := daysOr(in.Days, 30), limitOr(in.ThresholdMs, 5000), limitOr(in.MinQueries, 5)
		stats, err := s.statsFor(ctx, time.Duration(days)*24*time.Hour)
		if err != nil {
			return auditOut{}, err
		}
		type scored struct {
			f  finding
			ms int
		}
		var rows []scored
		out := auditOut{}
		for _, i := range s.enabled() {
			out.Scanned++
			st := stats[i.Id]
			if st == nil {
				continue
			}
			fix := "indexer_edit priority=50 or a sync profile without automatic search; audit_proxies if it goes through one; indexer_edit enable=false"
			if st.NumberOfQueries+st.NumberOfRssQueries >= minQ && st.AverageResponseTime >= threshold {
				rows = append(rows, scored{ms: st.AverageResponseTime, f: finding{
					Subject: i.Name, Kind: "indexer", Problem: "slow_queries",
					Detail: fmt.Sprintf("its queries took %.1f seconds on average over %d days (%d queries)", float64(st.AverageResponseTime)/1000, days, st.NumberOfQueries+st.NumberOfRssQueries),
					Fix:    fix,
				}})
			}
			if st.NumberOfGrabs > 0 && st.AverageGrabResponseTime >= 2*threshold {
				rows = append(rows, scored{ms: st.AverageGrabResponseTime, f: finding{
					Subject: i.Name, Kind: "indexer", Problem: "slow_grabs",
					Detail: fmt.Sprintf("its grabs took %.1f seconds on average over %d days (%d grabs)", float64(st.AverageGrabResponseTime)/1000, days, st.NumberOfGrabs),
					Fix:    "a slow download can time out in the download client; indexer_edit redirect=true fetches straight from the site where it supports that",
				}})
			}
		}
		slices.SortStableFunc(rows, func(a, b scored) int { return b.ms - a.ms })
		for _, row := range rows {
			out.report(in.Limit, row.f)
		}

		return out, nil
	})

	type unusedIn struct {
		Days       int  `json:"days,omitempty"         jsonschema:"how far back to look, default 90"`
		MinAgeDays *int `json:"min_age_days,omitempty" jsonschema:"leave out indexers added fewer than this many days ago, default 7"`
		Limit      int  `json:"limit,omitempty"        jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_unused",
		Description: "Find the enabled indexers that earn nothing over a window (default 90 days): never queried at all - no application receives them, or none searches them - or queried and never grabbed from, which costs every search the wait for them and gives nothing back. Indexers added within the last week (min_age_days) are left out. " +
			"audit_unsynced says why one is never queried; indexer_edit enable=false or indexer_delete retires one.",
	}, "enabled indexers never queried, or never grabbed from, in 90 days", func(ctx context.Context, s *snapshot, in unusedIn) (auditOut, error) {
		days := daysOr(in.Days, 90)
		stats, err := s.statsFor(ctx, time.Duration(days)*24*time.Hour)
		if err != nil {
			return auditOut{}, err
		}
		now := time.Now()
		grace := 7
		if in.MinAgeDays != nil {
			grace = max(*in.MinAgeDays, 0)
		}
		out := auditOut{}
		young := 0
		for _, i := range s.enabled() {
			added := parseTime(i.Added)
			if !added.IsZero() && now.Sub(added) < time.Duration(grace)*24*time.Hour {
				young++
				continue
			}
			out.Scanned++
			window := days
			if !added.IsZero() && added.After(now.AddDate(0, 0, -days)) {
				window = int(now.Sub(added).Hours() / 24)
			}
			st := stats[i.Id]
			asked := 0
			if st != nil {
				asked = st.NumberOfQueries + st.NumberOfRssQueries
			}
			switch {
			case asked == 0:
				reaches := 0
				for a := range s.apps {
					if syncs(&s.apps[a], i, s.tags).Synced {
						reaches++
					}
				}
				detail := fmt.Sprintf("not queried once in %d days", window)
				fix := "indexer_edit enable=false or indexer_delete if it is not wanted"
				if reaches == 0 {
					detail += ", and no application receives it"
					fix = "audit_unsynced says why no application receives it; " + fix
				}
				out.report(in.Limit, finding{Subject: i.Name, Kind: "indexer", Problem: "never_queried", Detail: detail, Fix: fix})
			case st.NumberOfGrabs == 0:
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "never_grabbed",
					Detail: fmt.Sprintf("queried %d times in %d days and never grabbed from: every search waits for it and it gives nothing back", asked, window),
					Fix:    "indexer_edit enable=false or indexer_delete, or a sync profile without automatic search (profile_create) to keep it for searching by hand",
				})
			}
		}
		if young > 0 {
			out.Note = fmt.Sprintf("%d indexers added within the last %d days were not judged", young, grace)
		}

		return out, nil
	})

	type limitsIn struct {
		Threshold int `json:"threshold,omitempty" jsonschema:"the share of a limit, in percent, used within its window that is worth a warning; default 80"`
		Limit     int `json:"limit,omitempty"     jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_limits",
		Description: "Find the indexers at or near a query or grab limit set on them: how much of each limit the current window (the last day, or the last hour) has used. At the limit Prowlarr refuses the applications with a 429 until the window moves on, which they read as the indexer failing. " +
			"indexer_edit query_limit and grab_limit raise them to what the site allows; a sync profile without RSS (profile_create) spends fewer.",
	}, "indexers that have used 80% or more of a query or grab limit", func(ctx context.Context, s *snapshot, in limitsIn) (auditOut, error) {
		threshold := limitOr(in.Threshold, 80)
		out := auditOut{}
		for _, i := range s.enabled() {
			queryLimit, hasQ := fieldNumber(i.Fields, "baseSettings.queryLimit")
			grabLimit, hasG := fieldNumber(i.Fields, "baseSettings.grabLimit")
			if (!hasQ || queryLimit <= 0) && (!hasG || grabLimit <= 0) {
				continue
			}
			out.Scanned++
			window, unit := 24*time.Hour, "day"
			if u, _ := fieldNumber(i.Fields, "baseSettings.limitsUnit"); u == 1 {
				window, unit = time.Hour, "hour"
			}
			stats, err := s.statsFor(ctx, window)
			if err != nil {
				return auditOut{}, err
			}
			st := stats[i.Id]
			if st == nil {
				st = &prowlarr.IndexerStatistics{}
			}
			for _, l := range []struct {
				name, problem string
				limit         float64
				used          int
				set           bool
			}{
				{"queries", "query_limit", queryLimit, st.NumberOfQueries + st.NumberOfRssQueries, hasQ && queryLimit > 0},
				{"grabs", "grab_limit", grabLimit, st.NumberOfGrabs, hasG && grabLimit > 0},
			} {
				if !l.set {
					continue
				}
				pct := int(float64(l.used) * 100 / l.limit)
				if pct < threshold {
					continue
				}
				problem := l.problem + "_near"
				if float64(l.used) >= l.limit {
					problem = l.problem + "_reached"
				}
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: problem,
					Detail: fmt.Sprintf("%d of %d %s in the last %s (%d%%)", l.used, int(l.limit), l.name, unit, pct),
					Fix:    "indexer_edit query_limit/grab_limit to what the site allows, or a sync profile without RSS to spend fewer",
				})
			}
		}

		return out, nil
	})
}

// sinceText says how long ago a time was, roughly, the way a person says it.
func sinceText(now, t time.Time) string {
	if t.IsZero() {
		return "for an unknown time"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
