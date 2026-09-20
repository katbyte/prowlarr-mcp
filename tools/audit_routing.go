package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The routing audits: whether what Prowlarr holds reaches where it should -
// the indexers the applications, the requests the proxies, the grabs the
// download clients - and the tags and profiles that decide it.

// localHosts are the names a URL uses to mean "this machine".
var localHosts = []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"}

func registerRoutingAudits(r *registry, t *auditTable) {
	type limitIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_unsynced",
		Description: "Find what the syncing leaves out: enabled indexers no application receives, with each application's reason - it lacks their tag, syncs none of their categories, needs a kind of search they do not offer, or its sync is off - applications that receive no indexer at all, and indexers whose sync profile turns off RSS and both searches, so the applications receive them and never use them. " +
			"indexer_edit (tags, sync profile), app_edit (tags, sync categories) and app_sync fix them.",
	}, "enabled indexers no application receives, applications that receive none", func(_ context.Context, s *snapshot, in limitIn) (auditOut, error) {
		out := auditOut{}
		if len(s.apps) == 0 {
			out.Note = "Prowlarr has no applications, so nothing is synced anywhere; app_add connects one"
			return out, nil
		}
		dead := map[int]bool{}
		for _, p := range s.profiles {
			if !boolv(p.EnableRss) && !boolv(p.EnableAutomaticSearch) && !boolv(p.EnableInteractiveSearch) {
				dead[p.Id] = true
			}
		}
		for _, i := range s.enabled() {
			out.Scanned++
			var reasons []string
			reached := 0
			for a := range s.apps {
				v := syncs(&s.apps[a], i, s.tags)
				if v.Synced {
					reached++
				} else {
					reasons = append(reasons, v.Reason)
				}
			}
			switch {
			case reached == 0:
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "reaches_no_app",
					Detail: "no application receives it: " + strings.Join(reasons, "; "),
					Fix:    "indexer_edit add_tags to share a tag with the applications, or app_edit sync_categories; then app_sync",
				})
			case dead[i.AppProfileId]:
				name := ""
				for _, p := range s.profiles {
					if p.Id == i.AppProfileId {
						name = p.Name
					}
				}
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "profile_uses_nothing",
					Detail: fmt.Sprintf("its sync profile %q turns off RSS, automatic and interactive search, so the %d applications that receive it never use it", name, reached),
					Fix:    "indexer_edit sync_profile to one that searches, or profile_edit",
				})
			}
		}
		for a := range s.apps {
			app := &s.apps[a]
			if app.SyncLevel == prowlarr.ApplicationSyncLevelDisabled {
				continue
			}
			byRule := map[string][]string{}
			got := 0
			for _, i := range s.enabled() {
				v := syncs(app, i, s.tags)
				if v.Synced {
					got++
				} else {
					byRule[v.Rule] = append(byRule[v.Rule], i.Name)
				}
			}
			if got > 0 || len(s.enabled()) == 0 {
				continue
			}
			out.report(in.Limit, finding{
				Subject: app.Name, Kind: "app", Problem: "app_gets_nothing",
				Detail: fmt.Sprintf("receives none of the %d enabled indexers: %s", len(s.enabled()), whyNothing(app, byRule, s.tags)),
				Fix:    "app_edit tags (or clear them) and sync_categories, or indexer_bulk_edit add_tags; then app_sync",
			})
		}

		return out, nil
	})

	type testIn struct {
		Test  bool `json:"test,omitempty"  jsonschema:"also test each one's connection, which reaches out to it"`
		Limit int  `json:"limit,omitempty" jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_apps",
		Description: "Find what is wrong with the applications Prowlarr feeds: failing (Prowlarr's health checks say so, or with test=true its connection test), sync switched off, told to reach Prowlarr at localhost while running elsewhere (every indexer it receives then points at itself), the same application added twice, or no categories to sync. " +
			"app_edit and app_test fix them, app_delete removes a duplicate.",
	}, "applications failing, not syncing, pointed at a local Prowlarr, or added twice", func(ctx context.Context, s *snapshot, in testIn) (auditOut, error) {
		out := auditOut{}
		checks, err := s.healthChecks(ctx)
		if err != nil {
			return auditOut{}, err
		}
		var tested map[int]testRow
		if in.Test && len(s.apps) > 0 {
			res, err := s.r.client.PostApplicationsTestAll(ctx)
			results, err := testAllAnswer(res.HttpResponse, res.Model, err)
			if err != nil {
				return auditOut{}, err
			}
			tested = map[int]testRow{}
			for _, row := range results {
				tested[row.Id] = testResults([]prowlarr.ProviderTestAllResult{row}, nil)[0]
			}
		}
		for a := range s.apps {
			app := &s.apps[a]
			out.Scanned++
			base, prowlarrURL := fieldString(app.Fields, "baseUrl"), fieldString(app.Fields, "prowlarrUrl")
			if h := healthNames(checks, app.Name, "ApplicationStatusCheck", "ApplicationLongTermStatusCheck"); h != nil {
				out.report(in.Limit, finding{
					Subject: app.Name, Kind: "app", Problem: "failing",
					Detail: "Prowlarr's health check: " + h.Message, Fix: "app_test says why; app_edit base_url or api_key",
				})
			}
			if tr, ok := tested[app.Id]; ok && !tr.OK {
				out.report(in.Limit, finding{
					Subject: app.Name, Kind: "app", Problem: "test_failed",
					Detail: "its connection test fails: " + strings.Join(tr.Problems, "; "), Fix: "app_edit base_url or api_key",
				})
			}
			if app.SyncLevel == prowlarr.ApplicationSyncLevelDisabled {
				out.report(in.Limit, finding{
					Subject: app.Name, Kind: "app", Problem: "sync_disabled",
					Detail: "its sync level is disabled, so Prowlarr sends it nothing", Fix: "app_edit sync_level=full, or app_delete if it is gone",
				})
			}
			if slices.Contains(localHosts, hostOf(prowlarrURL)) && !slices.Contains(localHosts, hostOf(base)) {
				out.report(in.Limit, finding{
					Subject: app.Name, Kind: "app", Problem: "prowlarr_url_local",
					Detail: fmt.Sprintf("it is at %s but told to reach Prowlarr at %s, which from anywhere but Prowlarr's own host is itself: every indexer it receives points there", base, prowlarrURL),
					Fix:    "app_edit prowlarr_url to Prowlarr's address as the application sees it, then app_sync force=true",
				})
			}
			if len(appCategories(app)) == 0 {
				out.report(in.Limit, finding{
					Subject: app.Name, Kind: "app", Problem: "no_sync_categories",
					Detail: "it syncs no categories, so no indexer qualifies for it", Fix: "app_edit sync_categories",
				})
			}
			for b := range a {
				if other := &s.apps[b]; sameURL(fieldString(other.Fields, "baseUrl"), base) {
					out.report(in.Limit, finding{
						Subject: app.Name, Kind: "app", Problem: "duplicate",
						Detail: fmt.Sprintf("it and %s are the same application at %s: each pushes its own copy of every indexer into it", other.Name, base),
						Fix:    "app_delete one of them",
					})
				}
			}
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_proxies",
		Description: "Find what is wrong with the proxies: enabled indexers whose site is behind Cloudflare with no FlareSolverr proxy sharing a tag (they fail every query), proxies with no tags or tags no indexer carries (used by nothing), and proxies failing (Prowlarr's health checks, or with test=true their test). " +
			"proxy_add, proxy_edit and indexer_edit add_tags fix them.",
	}, "Cloudflare sites without FlareSolverr, proxies used by nothing or failing", func(ctx context.Context, s *snapshot, in testIn) (auditOut, error) {
		out := auditOut{}
		checks, err := s.healthChecks(ctx)
		if err != nil {
			return auditOut{}, err
		}
		for _, i := range s.enabled() {
			if !cloudflare(i.Fields) {
				continue
			}
			out.Scanned++
			solved := slices.ContainsFunc(s.proxies, func(p prowlarr.IndexerProxyResource) bool {
				return p.Implementation == "FlareSolverr" && slices.ContainsFunc(p.Tags, func(t int) bool { return slices.Contains(i.Tags, t) })
			})
			if solved {
				continue
			}
			fix := "proxy_add kind=FlareSolverr with a tag, then indexer_edit add_tags with it"
			if idx := slices.IndexFunc(s.proxies, func(p prowlarr.IndexerProxyResource) bool { return p.Implementation == "FlareSolverr" }); idx >= 0 {
				fix = fmt.Sprintf("indexer_edit add_tags with one of %s's tags (%s)", s.proxies[idx].Name, strings.Join(labels(s.proxies[idx].Tags, s.tags), ", "))
			}
			out.report(in.Limit, finding{
				Subject: i.Name, Kind: "indexer", Problem: "needs_flaresolverr",
				Detail: "its definition says the site is behind Cloudflare, and no FlareSolverr proxy shares a tag with it", Fix: fix,
			})
		}
		var tested map[int]testRow
		if in.Test && len(s.proxies) > 0 {
			res, err := s.r.client.PostIndexerProxyTestAll(ctx)
			results, err := testAllAnswer(res.HttpResponse, res.Model, err)
			if err != nil {
				return auditOut{}, err
			}
			tested = map[int]testRow{}
			for _, row := range results {
				tested[row.Id] = testResults([]prowlarr.ProviderTestAllResult{row}, nil)[0]
			}
		}
		for _, p := range s.proxies {
			out.Scanned++
			users := 0
			for _, i := range s.indexers {
				if slices.ContainsFunc(p.Tags, func(t int) bool { return slices.Contains(i.Tags, t) }) {
					users++
				}
			}
			switch {
			case len(p.Tags) == 0:
				out.report(in.Limit, finding{
					Subject: p.Name, Kind: "proxy", Problem: "proxy_unused",
					Detail: "it has no tags, so no indexer uses it", Fix: "proxy_edit tags, and the same tag on the indexers that need it",
				})
			case users == 0:
				out.report(in.Limit, finding{
					Subject: p.Name, Kind: "proxy", Problem: "proxy_unused",
					Detail: fmt.Sprintf("no indexer carries its tags (%s), so none uses it", strings.Join(labels(p.Tags, s.tags), ", ")),
					Fix:    "indexer_edit add_tags on the indexers that need it, or proxy_delete",
				})
			}
			if h := healthNames(checks, p.Name, "IndexerProxyStatusCheck"); h != nil {
				out.report(in.Limit, finding{
					Subject: p.Name, Kind: "proxy", Problem: "proxy_failing",
					Detail: "Prowlarr's health check: " + h.Message, Fix: "proxy_test says why; proxy_edit its host",
				})
			}
			if tr, ok := tested[p.Id]; ok && !tr.OK {
				out.report(in.Limit, finding{
					Subject: p.Name, Kind: "proxy", Problem: "proxy_failing",
					Detail: "its test fails: " + strings.Join(tr.Problems, "; "), Fix: "proxy_edit its host, or proxy_delete",
				})
			}
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_download_clients",
		Description: "Find what is wrong with Prowlarr's own download clients (the ones grabs from Prowlarr itself go to): indexers pinned to a client that is gone or disabled, a usenet blackhole (which can never take a usenet grab), and clients failing (Prowlarr's health checks, or with test=true their test). " +
			"indexer_edit download_client, downloadclient_edit and downloadclient_test fix them.",
	}, "indexers pinned to a missing or disabled download client, clients failing", func(ctx context.Context, s *snapshot, in testIn) (auditOut, error) {
		out := auditOut{}
		checks, err := s.healthChecks(ctx)
		if err != nil {
			return auditOut{}, err
		}
		clients := map[int]*prowlarr.DownloadClientResource{}
		for c := range s.clients {
			clients[s.clients[c].Id] = &s.clients[c]
		}
		for _, i := range s.enabled() {
			if i.DownloadClientId <= 0 {
				continue
			}
			out.Scanned++
			switch c := clients[i.DownloadClientId]; {
			case c == nil:
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "pinned_client_missing",
					Detail: fmt.Sprintf("its grabs go to download client %d, which no longer exists", i.DownloadClientId),
					Fix:    "indexer_edit download_client to another, or none",
				})
			case !boolv(c.Enable):
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "pinned_client_disabled",
					Detail: fmt.Sprintf("its grabs go to %s, which is disabled", c.Name),
					Fix:    "downloadclient_edit enable=true, or indexer_edit download_client to another",
				})
			}
		}
		var tested map[int]testRow
		if in.Test {
			res, err := s.r.client.PostDownloadClientTestAll(ctx)
			results, err := testAllAnswer(res.HttpResponse, res.Model, err)
			if err != nil {
				return auditOut{}, err
			}
			tested = map[int]testRow{}
			for _, row := range results {
				tested[row.Id] = testResults([]prowlarr.ProviderTestAllResult{row}, nil)[0]
			}
		}
		for _, c := range s.clients {
			out.Scanned++
			// Prowlarr always hands a usenet grab over as a link (a usenet
			// indexer must redirect), and a blackhole only takes a file, so
			// UsenetBlackhole refuses every grab it is sent
			if c.Implementation == "UsenetBlackhole" && boolv(c.Enable) {
				out.report(in.Limit, finding{
					Subject: c.Name, Kind: "download_client", Problem: "blackhole_takes_no_usenet",
					Detail: "Prowlarr hands usenet grabs over as a link (usenet indexers always redirect), which a blackhole folder cannot take, so every usenet grab sent to it fails",
					Fix:    "downloadclient_add a SABnzbd or NZBGet for usenet grabs, then downloadclient_edit enable=false this one",
				})
			}
			if h := healthNames(checks, c.Name, "DownloadClientStatusCheck"); h != nil {
				out.report(in.Limit, finding{
					Subject: c.Name, Kind: "download_client", Problem: "client_failing",
					Detail: "Prowlarr's health check: " + h.Message, Fix: "downloadclient_test says why; downloadclient_edit its settings",
				})
			}
			if tr, ok := tested[c.Id]; ok && !tr.OK {
				out.report(in.Limit, finding{
					Subject: c.Name, Kind: "download_client", Problem: "client_failing",
					Detail: "its test fails: " + strings.Join(tr.Problems, "; "), Fix: "downloadclient_edit its settings, or enable=false",
				})
			}
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_tags",
		Description: "Find the tags that do nothing: carried by nothing at all, or carried by an application that no indexer shares, which leaves that application with nothing through it. " +
			"tag_delete removes an unused one; indexer_bulk_edit add_tags or app_edit fix the other.",
	}, "tags carried by nothing, or by an application no indexer shares", func(ctx context.Context, s *snapshot, in limitIn) (auditOut, error) {
		details, err := s.tagDetails(ctx)
		if err != nil {
			return auditOut{}, err
		}
		out := auditOut{}
		for _, d := range details {
			out.Scanned++
			switch {
			case d.Unused:
				out.report(in.Limit, finding{
					Subject: d.Label, Kind: "tag", Problem: "tag_unused",
					Detail: "nothing carries it", Fix: "tag_delete",
				})
			case len(d.Apps) > 0 && len(d.Indexers) == 0:
				out.report(in.Limit, finding{
					Subject: d.Label, Kind: "tag", Problem: "app_tag_matches_nothing",
					Detail: fmt.Sprintf("%s only take indexers tagged %s, and no indexer is", strings.Join(d.Apps, " and "), d.Label),
					Fix:    "indexer_bulk_edit add_tags on the indexers meant for them, or app_edit remove_tags",
				})
			}
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_profiles",
		Description: "Find the sync profiles that do nothing: one that turns off RSS, automatic and interactive search (its indexers reach the applications and are never used), and ones no indexer uses. " +
			"profile_edit fixes the first; profile_delete removes one no indexer uses.",
	}, "sync profiles that turn everything off, or that no indexer uses", func(_ context.Context, s *snapshot, in limitIn) (auditOut, error) {
		out := auditOut{}
		for p := range s.profiles {
			prof := &s.profiles[p]
			out.Scanned++
			row := projectProfile(prof, s.indexers)
			if !row.RSS && !row.AutomaticSearch && !row.InteractiveSearch {
				out.report(in.Limit, finding{
					Subject: prof.Name, Kind: "profile", Problem: "profile_uses_nothing",
					Detail: fmt.Sprintf("it turns off RSS, automatic and interactive search, so the applications never use its %d indexers", len(row.Indexers)),
					Fix:    "profile_edit to turn a search on, or indexer_bulk_edit sync_profile to move its indexers",
				})
			}
			if len(row.Indexers) == 0 && len(s.profiles) > 1 {
				out.report(in.Limit, finding{
					Subject: prof.Name, Kind: "profile", Problem: "profile_unused",
					Detail: "no indexer uses it", Fix: "profile_delete, or leave it for later",
				})
			}
		}

		return out, nil
	})
}
