package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The audits: each a sweep over every indexer, application, proxy or tag for
// one thing that goes wrong, answering a worklist rather than a dump, and
// naming the tool that fixes what it finds.
//
// Detection is code, correction is judgment: the sweeps are cheap and
// deterministic, read only what Prowlarr already knows (its settings, its
// failure records, its statistics and history, its catalogue), and never
// test an indexer against its site unless asked to. The AI reasons only about
// what they turn up.

// finding is one row of an audit's worklist.
type finding struct {
	Subject string `json:"subject"       jsonschema:"the indexer, application, proxy, download client, tag or profile it is about"`
	Kind    string `json:"kind"          jsonschema:"indexer, app, proxy, download_client, tag, profile or health"`
	Problem string `json:"problem"       jsonschema:"the kind of problem, a short fixed phrase the findings can be grouped by"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty" jsonschema:"how to fix it, naming the tool"`
}

// auditOut is what every audit answers.
type auditOut struct {
	Scanned  int       `json:"scanned"        jsonschema:"how many indexers (or applications, proxies, tags) were looked at"`
	Found    int       `json:"total_findings"`
	Findings []finding `json:"findings"       jsonschema:"capped at limit; total_findings is the real count"`
	Note     string    `json:"note,omitempty" jsonschema:"what the audit could not see, or what its count means"`
}

// report adds a finding, counting it whether or not it fits under the limit.
func (o *auditOut) report(limit int, f finding) {
	o.Found++
	if len(o.Findings) < limitOr(limit, 100) {
		o.Findings = append(o.Findings, f)
	}
}

// snapshot is Prowlarr as the audits read it, loaded once per call and shared:
// audit_all runs every audit over one snapshot rather than reading every
// indexer once an audit. What only some audits need is read the first time
// one asks.
type snapshot struct {
	r *registry

	indexers []prowlarr.IndexerResource
	statuses []prowlarr.IndexerStatusResource
	apps     []prowlarr.ApplicationResource
	profiles []prowlarr.AppProfileResource
	proxies  []prowlarr.IndexerProxyResource
	clients  []prowlarr.DownloadClientResource
	tags     map[int]string

	mu      sync.Mutex
	health  []prowlarr.HealthResource
	stats   map[string]*prowlarr.IndexerStatsResource
	details []tagRow
}

func (r *registry) snap(ctx context.Context) (*snapshot, error) {
	s := &snapshot{r: r, stats: map[string]*prowlarr.IndexerStatsResource{}}
	var err error
	if s.indexers, err = r.indexers(ctx); err != nil {
		return nil, err
	}
	st, err := r.client.GetIndexerStatus(ctx)
	if err != nil {
		return nil, err
	}
	s.statuses = st.Model
	apps, err := r.client.GetApplications(ctx)
	if err != nil {
		return nil, err
	}
	s.apps = apps.Model
	profiles, err := r.client.GetAppProfile(ctx)
	if err != nil {
		return nil, err
	}
	s.profiles = profiles.Model
	proxies, err := r.client.GetIndexerProxy(ctx)
	if err != nil {
		return nil, err
	}
	s.proxies = proxies.Model
	clients, err := r.client.GetDownloadClient(ctx)
	if err != nil {
		return nil, err
	}
	s.clients = clients.Model
	if s.tags, err = r.tagLabels(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(s.indexers, func(a, b prowlarr.IndexerResource) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	return s, nil
}

// enabled are the enabled indexers.
func (s *snapshot) enabled() []*prowlarr.IndexerResource {
	var out []*prowlarr.IndexerResource
	for i := range s.indexers {
		if boolv(s.indexers[i].Enable) {
			out = append(out, &s.indexers[i])
		}
	}

	return out
}

// statusOf is an indexer's failure record, nil when it has none.
func (s *snapshot) statusOf(id int) *prowlarr.IndexerStatusResource {
	for i := range s.statuses {
		if s.statuses[i].IndexerId == id {
			return &s.statuses[i]
		}
	}

	return nil
}

// statsFor reads the statistics of a window ending now, once per window.
func (s *snapshot) statsFor(ctx context.Context, window time.Duration) (map[int]*prowlarr.IndexerStatistics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := window.String()
	res, ok := s.stats[key]
	if !ok {
		got, err := s.r.client.GetIndexerStats(ctx, prowlarr.GetIndexerStatsOperationOptions{
			StartDate: stampUTC(time.Now().Add(-window)),
			EndDate:   stampUTC(time.Now().Add(time.Minute)),
		})
		if err != nil {
			return nil, err
		}
		res = got.Model
		if res == nil {
			res = &prowlarr.IndexerStatsResource{}
		}
		s.stats[key] = res
	}
	out := map[int]*prowlarr.IndexerStatistics{}
	for i := range res.Indexers {
		out[res.Indexers[i].IndexerId] = &res.Indexers[i]
	}

	return out, nil
}

// healthChecks reads Prowlarr's own health checks, once.
func (s *snapshot) healthChecks(ctx context.Context) ([]prowlarr.HealthResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.health == nil {
		res, err := s.r.client.GetHealth(ctx)
		if err != nil {
			return nil, err
		}
		s.health = res.Model
		if s.health == nil {
			s.health = []prowlarr.HealthResource{}
		}
	}

	return s.health, nil
}

// tagDetails reads what carries each tag, once.
func (s *snapshot) tagDetails(ctx context.Context) ([]tagRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.details == nil {
		d, err := s.r.tagRows(ctx, s.indexers, s.apps, s.proxies)
		if err != nil {
			return nil, err
		}
		s.details = d
	}

	return s.details, nil
}

// healthNames reports whether one of the named health checks mentions a
// name: Prowlarr's checks list the providers they are about by name in
// their messages.
func healthNames(checks []prowlarr.HealthResource, name string, sources ...string) *prowlarr.HealthResource {
	for i := range checks {
		if slices.Contains(sources, checks[i].Source) && mentions(checks[i].Message, name) {
			return &checks[i]
		}
	}

	return nil
}

// mentions reports whether a health message names a provider. Prowlarr's
// checks list the names after a colon, separated by commas ("...due to
// failures: Sonarr 4K, Radarr"), so a name counts only where it stands whole
// between those: after the colon or a comma (and a space), and before a
// comma, a full stop or the end. "Sonarr" is not named in that message.
func mentions(msg, name string) bool {
	if name == "" {
		return false
	}
	opens := func(at int) bool {
		if at == 0 {
			return true
		}
		if msg[at-1] == ' ' {
			at--
		}
		return at > 0 && strings.ContainsRune(":,(", rune(msg[at-1]))
	}
	for i := 0; ; {
		j := strings.Index(msg[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		closes := end == len(msg) || strings.ContainsRune(",.;)", rune(msg[end]))
		if opens(start) && closes {
			return true
		}
		i = start + 1
	}
}

// audit is one registered audit: its name, and how audit_all runs it with its
// defaults, counting only.
type audit struct {
	name string
	// summary is what audit_all says the count means.
	summary string
	run     func(ctx context.Context, s *snapshot) (auditOut, error)
}

// audits are the sweeps audit_all runs, in the order it reports them;
// registration adds each as it registers its tool.
type auditTable struct {
	list []audit
}

func registerAuditTools(r *registry) {
	t := &auditTable{}
	registerIndexerAudits(r, t)
	registerRoutingAudits(r, t)
	registerSettingAudits(r, t)
	registerAuditAll(r, t)
}

type auditAllRow struct {
	Audit    string `json:"audit"`
	Findings int    `json:"findings"`
	Scanned  int    `json:"scanned"`
	Note     string `json:"note,omitempty" jsonschema:"what the count means"`
}

type auditAllOut struct {
	Audits []auditAllRow `json:"audits"`
	Total  int           `json:"total_findings"`
}

// registerAuditAll adds the one-call overview: every audit, counts only, so a
// session starts with a picture of where Prowlarr needs work and then calls
// the audit that matters for its worklist.
func registerAuditAll(r *registry, t *auditTable) {
	add(r, readTool, &mcp.Tool{
		Name: "audit_all",
		Description: "Run every audit and report only the counts, so one call says where Prowlarr needs work: failing, unreliable, slow and unused indexers, indexers no application receives, applications that are misconfigured, duplicates, private trackers with no seed goal, limits and VIP running out, retired definitions and addresses, missing FlareSolverr, broken download clients, stray tags and profiles, and Prowlarr's own health checks. " +
			"Start here, then call the audit whose count is not zero for its worklist. Nothing is tested against a site or an application: the audits that can test (audit_failing, audit_apps, audit_proxies, audit_download_clients) do so only when asked.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, auditAllOut, error) {
		s, err := r.snap(ctx)
		if err != nil {
			return nil, auditAllOut{}, err
		}
		out := auditAllOut{}
		for _, a := range t.list {
			res, err := a.run(ctx, s)
			if err != nil {
				return nil, auditAllOut{}, fmt.Errorf("%s: %w", a.name, err)
			}
			note := a.summary
			if res.Note != "" {
				note = res.Note
			}
			out.Audits = append(out.Audits, auditAllRow{Audit: a.name, Findings: res.Found, Scanned: res.Scanned, Note: note})
			out.Total += res.Found
		}

		return nil, out, nil
	})
}

// addAudit registers an audit's tool and queues it for audit_all. The tool
// reads a snapshot and runs the audit with what it was asked; audit_all runs
// it with the defaults.
func addAudit[In any](r *registry, t *auditTable, tool *mcp.Tool, summary string, run func(ctx context.Context, s *snapshot, in In) (auditOut, error)) {
	add(r, readTool, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, auditOut, error) {
		s, err := r.snap(ctx)
		if err != nil {
			return nil, auditOut{}, err
		}
		out, err := run(ctx, s, in)

		return nil, out, err
	})
	t.list = append(t.list, audit{
		name:    tool.Name,
		summary: summary,
		run: func(ctx context.Context, s *snapshot) (auditOut, error) {
			var zero In
			return run(ctx, s, zero)
		},
	})
}
