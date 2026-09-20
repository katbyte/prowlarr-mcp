package tools

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The setting audits: what the indexers' own settings say is wrong, checked
// against each other and against the catalogue they were added from.

// generic are the implementations one site each is added through, told
// apart by their URL rather than their definition.
var generic = []string{"Newznab", "Torznab", "TorrentRssIndexer", "TorrentPotato"}

// siteAddress is a generic indexer's API address, the way two of them are
// compared: host in lower case, no trailing slashes, the API path after the
// base. "" when it has no host.
func siteAddress(base, apiPath string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return ""
	}

	return strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/") + "/" + strings.Trim(apiPath, "/")
}

func registerSettingAudits(r *registry, t *auditTable) {
	type limitIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_duplicates",
		Description: "Find trackers added more than once: two indexers from the same definition, or two generic Newznab or Torznab indexers pointed at the same site. Each copy is searched and returns the same releases, so the applications see every result twice and the site counts double requests against its limits. " +
			"indexer_delete removes the extra copy; indexer_edit enable=false stops it being searched, and it stays listed here, marked disabled.",
	}, "trackers added more than once", func(_ context.Context, s *snapshot, in limitIn) (auditOut, error) {
		out := auditOut{Scanned: len(s.indexers)}
		groups := map[string][]string{}
		var keys []string
		for _, i := range s.indexers {
			key := "definition " + definitionOf(&i)
			if slices.Contains(generic, i.Implementation) {
				// the whole address, path included: one Jackett or one
				// Prowlarr serves many sites from one host, a path each
				site := siteAddress(fieldString(i.Fields, "baseUrl"), fieldString(i.Fields, "apiPath"))
				if site == "" {
					continue
				}
				key = "site " + site
			}
			if groups[key] == nil {
				keys = append(keys, key)
			}
			label := i.Name
			if !boolv(i.Enable) {
				label += " (disabled)"
			}
			groups[key] = append(groups[key], label)
		}
		for _, key := range keys {
			names := groups[key]
			if len(names) < 2 {
				continue
			}
			out.report(in.Limit, finding{
				Subject: names[0], Kind: "indexer", Problem: "duplicate",
				Detail: fmt.Sprintf("%s are the same %s", strings.Join(names, ", "), key),
				Fix:    "indexer_delete or indexer_edit enable=false the extra copies",
			})
		}

		return out, nil
	})

	type seedingIn struct {
		Public bool `json:"public,omitempty" jsonschema:"judge public trackers too, which rarely care"`
		Limit  int  `json:"limit,omitempty"  jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_seeding",
		Description: "Find the private and semi-private torrent indexers with no seed goal: neither a seed ratio nor a seed time is set, so the applications tell their download client nothing and it may stop seeding before the tracker's minimum, which private trackers punish as hit-and-run. public=true judges public trackers too. " +
			"indexer_bulk_edit seed_ratio and seed_time set them on many at once.",
	}, "private torrent indexers with no seed ratio or seed time", func(_ context.Context, s *snapshot, in seedingIn) (auditOut, error) {
		out := auditOut{}
		for _, i := range s.enabled() {
			if i.Protocol != prowlarr.DownloadProtocolTorrent || (!in.Public && i.Privacy == prowlarr.IndexerPrivacyPublic) {
				continue
			}
			out.Scanned++
			_, ratio := fieldNumber(i.Fields, "torrentBaseSettings.seedRatio")
			_, secs := fieldNumber(i.Fields, "torrentBaseSettings.seedTime")
			if ratio || secs {
				continue
			}
			out.report(in.Limit, finding{
				Subject: i.Name, Kind: "indexer", Problem: "no_seed_goal",
				Detail: fmt.Sprintf("a %s tracker with no seed ratio or seed time: the download client may stop seeding before the tracker's minimum", i.Privacy),
				Fix:    "indexer_bulk_edit seed_ratio and seed_time to the tracker's rules (often ratio 1 or 72 hours)",
			})
		}

		return out, nil
	})

	type vipIn struct {
		Days  int `json:"days,omitempty"  jsonschema:"warn this many days ahead, default 14"`
		Limit int `json:"limit,omitempty" jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_vip",
		Description: "Find the indexers whose paid membership (VIP) has run out or runs out within a number of days (default 14), from the VIP expiry recorded on them - after which many sites cut the API or the searches the applications rely on. " +
			"Renew it, then indexer_edit vip_expiration.",
	}, "VIP memberships expired or ending within 14 days", func(_ context.Context, s *snapshot, in vipIn) (auditOut, error) {
		days := daysOr(in.Days, 14)
		now := time.Now()
		out := auditOut{}
		for _, i := range s.enabled() {
			raw := fieldString(i.Fields, "vipExpiration")
			if raw == "" {
				continue
			}
			out.Scanned++
			end := parseTime(raw)
			switch {
			case end.IsZero():
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "vip_unreadable",
					Detail: fmt.Sprintf("its VIP expiry %q is not a date", raw), Fix: "indexer_edit vip_expiration=YYYY-MM-DD",
				})
			case end.Before(now):
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "vip_expired",
					Detail: fmt.Sprintf("its VIP ran out on %s, %s ago", raw, sinceText(now, end)),
					Fix:    "renew it and indexer_edit vip_expiration, or indexer_edit enable=false if the site no longer serves it",
				})
			case end.Before(now.AddDate(0, 0, days)):
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "vip_expiring",
					Detail: fmt.Sprintf("its VIP runs out on %s, in %d days", raw, int(end.Sub(now).Hours()/24)+1),
					Fix:    "renew it, then indexer_edit vip_expiration",
				})
			}
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_definitions",
		Description: "Find the indexers their definition has left behind: its definition is gone from Prowlarr's catalogue or marked obsolete (Prowlarr cannot run it), it uses an address the site has moved away from, or one the definition does not list at all (a mirror or a typo), or its definition carries a warning. " +
			"indexer_edit base_url moves one to a current address; indexer_catalog finds a replacement for one that is gone.",
	}, "indexers on a retired definition or address", func(ctx context.Context, s *snapshot, in limitIn) (auditOut, error) {
		catalog, err := s.r.catalog(ctx)
		if err != nil {
			return auditOut{}, err
		}
		checks, err := s.healthChecks(ctx)
		if err != nil {
			return auditOut{}, err
		}
		out := auditOut{}
		for idx := range s.indexers {
			i := &s.indexers[idx]
			out.Scanned++
			if i.Implementation == "Cardigann" {
				file := fieldString(i.Fields, "definitionFile")
				if file == "" {
					file = i.DefinitionName
				}
				if !slices.ContainsFunc(catalog, func(e prowlarr.IndexerResource) bool {
					return e.Implementation == "Cardigann" && strings.EqualFold(definitionOf(&e), file)
				}) {
					out.report(in.Limit, finding{
						Subject: i.Name, Kind: "indexer", Problem: "definition_gone",
						Detail: fmt.Sprintf("its definition %q is no longer in Prowlarr's catalogue, so Prowlarr cannot run it", file),
						Fix:    "indexer_catalog for a replacement and indexer_add it, then indexer_delete this one",
					})
					continue
				}
			}
			if h := healthNames(checks, i.Name, "OutdatedDefinitionCheck", "IndexerNoDefinitionCheck"); h != nil {
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "definition_obsolete",
					Detail: "Prowlarr's health check: " + h.Message, Fix: "indexer_catalog for its replacement and indexer_add it, then indexer_delete this one",
				})
			}
			if i.Message != nil && i.Message.Message != "" && i.Message.Type != prowlarr.ProviderMessageTypeInfo {
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "definition_notice",
					Detail: "its definition says: " + i.Message.Message, Fix: "read it; indexer_catalog may list a replacement",
				})
			}
			base := fieldString(i.Fields, "baseUrl")
			urls := nonEmpty(i.IndexerUrls)
			if base == "" || len(urls) == 0 || slices.Contains(generic, i.Implementation) {
				continue
			}
			switch {
			case slices.ContainsFunc(i.LegacyUrls, func(u string) bool { return sameURL(u, base) }):
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "retired_url",
					Detail: fmt.Sprintf("it uses %s, an address the site has moved away from; current: %s", base, strings.Join(urls, ", ")),
					Fix:    "indexer_edit base_url=" + urls[0],
				})
			case !slices.ContainsFunc(urls, func(u string) bool { return sameURL(u, base) }):
				out.report(in.Limit, finding{
					Subject: i.Name, Kind: "indexer", Problem: "unlisted_url",
					Detail: fmt.Sprintf("it uses %s, which its definition does not list (it knows %s): a mirror, or a typo", base, strings.Join(urls, ", ")),
					Fix:    fmt.Sprintf("indexer_edit base_url=%s, unless the mirror is deliberate", urls[0]),
				})
			}
		}

		return out, nil
	})

	type healthIn struct {
		Notices bool `json:"notices,omitempty" jsonschema:"include notices, not only warnings and errors"`
		Limit   int  `json:"limit,omitempty"   jsonschema:"findings to return, default 100"`
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_health",
		Description: "Prowlarr's own health checks as a worklist: every warning and error it reports - failing indexers and applications, obsolete definitions, expiring VIP, an update, a proxy it cannot reach - errors first, each with its wiki link and the audit or tool that deals with it. " +
			"server_health check=true runs the checks again.",
	}, "warnings and errors in Prowlarr's own health checks", func(ctx context.Context, s *snapshot, in healthIn) (auditOut, error) {
		checks, err := s.healthChecks(ctx)
		if err != nil {
			return auditOut{}, err
		}
		out := auditOut{Scanned: len(checks)}
		sorted := slices.Clone(checks)
		rank := map[prowlarr.HealthCheckResult]int{prowlarr.HealthCheckResultError: 0, prowlarr.HealthCheckResultWarning: 1, prowlarr.HealthCheckResultNotice: 2}
		slices.SortStableFunc(sorted, func(a, b prowlarr.HealthResource) int { return rank[a.Type] - rank[b.Type] })
		for _, h := range sorted {
			if h.Type == prowlarr.HealthCheckResultOk || (h.Type == prowlarr.HealthCheckResultNotice && !in.Notices) {
				continue
			}
			detail := h.Message
			if h.WikiUrl != "" {
				detail += " (" + h.WikiUrl + ")"
			}
			out.report(in.Limit, finding{
				Subject: h.Source, Kind: "health", Problem: string(h.Type), Detail: detail, Fix: healthFix[h.Source],
			})
		}

		return out, nil
	})
}
