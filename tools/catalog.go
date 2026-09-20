package tools

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The catalogue is every tracker Prowlarr can add: its native indexers, the
// presets of the generic Newznab and Torznab ones, and the Cardigann
// definitions it keeps up to date from its definitions repository - over six
// hundred entries and several megabytes, rebuilt by Prowlarr on every read.
// It changes when the definitions do, so a read is kept for a while.

// catalogTTL is how long a read of the catalogue is kept.
const catalogTTL = 10 * time.Minute

// catalogCache is the catalogue as last read, shared by every call.
type catalogCache struct {
	mu      sync.Mutex
	entries []prowlarr.IndexerResource
	read    time.Time
}

// catalog reads the catalogue, or answers the read kept from the last few
// minutes.
func (r *registry) catalog(ctx context.Context) ([]prowlarr.IndexerResource, error) {
	c := &r.catalogCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries != nil && time.Since(c.read) < catalogTTL {
		return c.entries, nil
	}
	res, err := r.client.GetIndexerSchema(ctx)
	if err != nil {
		return nil, err
	}
	c.entries, c.read = res.Model, time.Now()

	return c.entries, nil
}

// forgetCatalog drops the kept read, after something changed what it says
// (an indexer added from it is listed as added).
func (r *registry) forgetCatalog() {
	r.catalogCache.mu.Lock()
	r.catalogCache.entries = nil
	r.catalogCache.mu.Unlock()
}

// catalogName is how the catalogue and indexer_add name an entry: its name,
// and when two entries share one (a native indexer and a definition of the
// same site), the implementation in brackets after it.
func catalogName(e *prowlarr.IndexerResource, all []prowlarr.IndexerResource) string {
	n := 0
	for i := range all {
		if all[i].Name == e.Name {
			n++
		}
	}
	if n > 1 {
		return e.Name + " (" + e.Implementation + ")"
	}

	return e.Name
}

// findCatalog picks a catalogue entry by the name the catalogue gives it,
// ignoring case, or by its definition name when that is unambiguous.
func findCatalog(all []prowlarr.IndexerResource, ref string) (*prowlarr.IndexerResource, error) {
	ref = strings.TrimSpace(ref)
	var byDefinition []int
	for i := range all {
		if strings.EqualFold(catalogName(&all[i], all), ref) {
			return &all[i], nil
		}
		if strings.EqualFold(all[i].DefinitionName, ref) && all[i].Implementation == "Cardigann" {
			byDefinition = append(byDefinition, i)
		}
	}
	if len(byDefinition) == 1 {
		return &all[byDefinition[0]], nil
	}
	var near []string
	for i := range all {
		if strings.Contains(strings.ToLower(all[i].Name), strings.ToLower(ref)) {
			near = append(near, catalogName(&all[i], all))
		}
	}
	if len(near) > 0 && len(near) <= 20 {
		return nil, fmt.Errorf("no definition %q in the catalogue; did you mean: %s", ref, strings.Join(near, ", "))
	}

	return nil, fmt.Errorf("no definition %q in the catalogue; indexer_catalog searches it", ref)
}

// needs are the credentials an entry asks for, by field name.
func needs(e *prowlarr.IndexerResource) []string {
	var out []string
	for i := range e.Fields {
		f := &e.Fields[i]
		if f.Type == "info" || f.Hidden == "hidden" || f.Hidden == "hiddenIfNotSet" {
			continue
		}
		if secret(f) {
			out = append(out, f.Name)
		}
	}

	return out
}

// cloudflare reports whether a definition says the site sits behind
// Cloudflare's challenge, which Prowlarr only gets through with FlareSolverr.
func cloudflare(fields []prowlarr.Field) bool {
	return field(fields, "info_flaresolverr") != nil
}

func registerCatalogTools(r *registry) {
	pc := r.client

	type catalogIn struct {
		Search   string `json:"search,omitempty"   jsonschema:"words to find in the name or description, e.g. anime, nzbgeek"`
		Protocol string `json:"protocol,omitempty" jsonschema:"usenet or torrent"`
		Privacy  string `json:"privacy,omitempty"  jsonschema:"public, semiPrivate or private"`
		Language string `json:"language,omitempty" jsonschema:"the site's language, e.g. en or fr-FR"`
		Category int    `json:"category,omitempty" jsonschema:"only sites carrying this category, e.g. 5000 for TV or 2000 for movies (category_list names them)"`
		Limit    int    `json:"limit,omitempty"    jsonschema:"entries to return, default 25"`
	}
	type catalogRow struct {
		Definition  string   `json:"definition"            jsonschema:"what indexer_add takes"`
		Protocol    string   `json:"protocol"`
		Privacy     string   `json:"privacy"`
		Language    string   `json:"language,omitempty"`
		Description string   `json:"description,omitempty"`
		URLs        []string `json:"urls"                  jsonschema:"the addresses the definition knows the site by, first is the default"`
		Categories  []int    `json:"categories"            jsonschema:"top-level categories"`
		Searches    []string `json:"searches"`
		Needs       []string `json:"needs"                 jsonschema:"the credentials indexer_add must be given, by setting name"`
		Cloudflare  bool     `json:"cloudflare"            jsonschema:"the site is behind Cloudflare's challenge, so it needs a FlareSolverr proxy sharing a tag"`
		Added       []string `json:"added,omitempty"       jsonschema:"indexers already added from this definition"`
	}
	type catalogOut struct {
		Total   int          `json:"total"   jsonschema:"entries matching"`
		Entries []catalogRow `json:"entries"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "indexer_catalog",
		Description: "Search the catalogue of trackers Prowlarr can add - its native indexers, the Newznab and Torznab presets, and the definitions it keeps up to date - by words, protocol, privacy, language or category. Each entry says what indexer_add needs (API key, login, cookie), whether the site needs FlareSolverr, and whether it is already added.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in catalogIn) (*mcp.CallToolResult, catalogOut, error) {
		all, err := r.catalog(ctx)
		if err != nil {
			return nil, catalogOut{}, err
		}
		have, err := r.indexers(ctx)
		if err != nil {
			return nil, catalogOut{}, err
		}
		words := strings.Fields(strings.ToLower(in.Search))
		out := catalogOut{}
		for i := range all {
			e := &all[i]
			text := strings.ToLower(e.Name + " " + e.Description + " " + e.DefinitionName)
			switch {
			case slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) }):
				continue
			case in.Protocol != "" && !strings.EqualFold(string(e.Protocol), in.Protocol):
				continue
			case in.Privacy != "" && !strings.EqualFold(string(e.Privacy), in.Privacy):
				continue
			case in.Language != "" && !strings.HasPrefix(strings.ToLower(e.Language), strings.ToLower(in.Language)):
				continue
			case in.Category != 0 && !slices.Contains(indexerCategories(e), in.Category):
				continue
			}
			out.Total++
			if len(out.Entries) >= limitOr(in.Limit, 25) {
				continue
			}
			row := catalogRow{
				Definition: catalogName(e, all), Protocol: string(e.Protocol), Privacy: string(e.Privacy), Language: e.Language,
				Description: e.Description, URLs: nonEmpty(e.IndexerUrls), Categories: topCategories(e), Needs: needs(e),
				Cloudflare: cloudflare(e.Fields),
			}
			for k, ok := range searches(e) {
				if ok {
					row.Searches = append(row.Searches, k)
				}
			}
			slices.Sort(row.Searches)
			for _, h := range have {
				if h.Implementation == e.Implementation && definitionOf(&h) == definitionOf(e) && (e.Implementation == "Cardigann" || h.Name == e.Name) {
					row.Added = append(row.Added, h.Name)
				}
			}
			out.Entries = append(out.Entries, row)
		}

		return nil, out, nil
	})

	type addIn struct {
		Definition  string         `json:"definition"             jsonschema:"the catalogue entry to add, as indexer_catalog names it, e.g. 1337x or Generic Newznab"`
		Name        string         `json:"name,omitempty"         jsonschema:"what to call it, default the definition's name"`
		BaseURL     string         `json:"base_url,omitempty"     jsonschema:"the site address, default the definition's first; a generic Newznab or Torznab needs one"`
		Settings    map[string]any `json:"settings,omitempty"     jsonschema:"its settings by name: the credentials indexer_catalog lists under needs (apiKey, username, password, cookie) and any other"`
		Tags        []string       `json:"tags,omitempty"         jsonschema:"created when new; tags decide which applications and proxies it goes to"`
		SyncProfile string         `json:"sync_profile,omitempty" jsonschema:"by name or id, default the first profile"`
		Priority    int            `json:"priority,omitempty"     jsonschema:"1 to 50, default 25"`
		Disabled    bool           `json:"disabled,omitempty"     jsonschema:"add it disabled, which also skips the test"`
		Redirect    *bool          `json:"redirect,omitempty"     jsonschema:"the applications download releases straight from the site rather than through Prowlarr (usenet always does)"`
		Force       bool           `json:"force,omitempty"        jsonschema:"add it even if Prowlarr's test of it fails"`
		seedIn
	}
	type addOut struct {
		Indexer indexerRow `json:"indexer"`
		Next    string     `json:"next"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "indexer_add",
		Description: "Add an indexer from the catalogue (indexer_catalog finds it), with its credentials, URL, tags, sync profile, priority and seed goals. Prowlarr tests it first and refuses one that fails - wrong key, site unreachable, Cloudflare in the way - unless force is set; the applications receive it on the next sync.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
		all, err := r.catalog(ctx)
		if err != nil {
			return nil, addOut{}, err
		}
		e, err := findCatalog(all, in.Definition)
		if err != nil {
			return nil, addOut{}, err
		}
		i := *e
		i.Fields = slices.Clone(e.Fields)
		i.Presets = nil
		i.Name = e.Name
		if in.Name != "" {
			i.Name = in.Name
		}
		i.Enable = new(!in.Disabled)
		i.Priority = limitOr(in.Priority, 25)
		if i.Priority > 50 {
			return nil, addOut{}, fmt.Errorf("priority must be 1 to 50, got %d", i.Priority)
		}
		if in.Redirect != nil {
			i.Redirect = in.Redirect
		}
		if in.SyncProfile != "" {
			var p *prowlarr.AppProfileResource
			if p, err = r.resolveProfile(ctx, in.SyncProfile); err != nil {
				return nil, addOut{}, err
			}
			i.AppProfileId = p.Id
		} else {
			profiles, profileErr := pc.GetAppProfile(ctx)
			if profileErr != nil {
				return nil, addOut{}, profileErr
			}
			if len(profiles.Model) == 0 {
				return nil, addOut{}, fmt.Errorf("prowlarr has no sync profile to give %s; profile_create makes one", i.Name)
			}
			i.AppProfileId = profiles.Model[0].Id
		}
		if i.Tags, err = r.resolveTags(ctx, in.Tags, true); err != nil {
			return nil, addOut{}, err
		}
		values := in.values()
		maps.Copy(values, in.Settings)
		switch {
		case in.BaseURL != "":
			values["baseUrl"] = in.BaseURL
		case fieldString(i.Fields, "baseUrl") == "" && field(i.Fields, "baseUrl") != nil:
			urls := nonEmpty(e.IndexerUrls)
			if len(urls) == 0 {
				return nil, addOut{}, fmt.Errorf("%s has no address of its own: give it a base_url", e.Name)
			}
			values["baseUrl"] = urls[0]
		}
		if err := setFields(i.Fields, values); err != nil {
			return nil, addOut{}, fmt.Errorf("%s: %w", e.Name, err)
		}

		res, err := forcedAdd(in.Force && !in.Disabled,
			func(on bool) { i.Enable = new(on) },
			func() (*prowlarr.IndexerResource, error) {
				made, addErr := pc.PostIndexer(ctx, i, prowlarr.PostIndexerOperationOptions{ForceSave: new(in.Force)})
				return made.Model, addErr
			},
			func(made *prowlarr.IndexerResource) (*prowlarr.IndexerResource, error) {
				// the update writes back what it is sent, so it carries the
				// date the create stamped rather than the catalogue entry's
				// zero one
				i.Id, i.Added = made.Id, made.Added
				saved, saveErr := pc.PutIndexerById(ctx, made.Id, i, prowlarr.PutIndexerByIdOperationOptions{ForceSave: new(true)})
				return saved.Model, saveErr
			})
		if err != nil {
			return nil, addOut{}, saveError(i.Name, err)
		}
		r.forgetCatalog()
		l, err := r.lookups(ctx)
		if err != nil {
			return nil, addOut{}, err
		}
		next := "the applications receive it on their next sync; app_sync runs one now"
		if cloudflare(i.Fields) {
			next = "the site is behind Cloudflare: give it a tag a FlareSolverr proxy carries (audit_proxies checks); " + next
		}

		return nil, addOut{Indexer: l.indexerRow(res), Next: next}, nil
	})

	type categoryRow struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Parent int    `json:"parent,omitempty"`
	}
	type categoriesOut struct {
		Categories []categoryRow `json:"categories" jsonschema:"each top-level category followed by its subcategories"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "category_list",
		Description: "The standard Newznab categories Prowlarr files releases under and the applications sync by - 2000 movies, 5000 TV, 3000 audio, 7000 books and their subcategories - for release_search and an application's sync categories.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, categoriesOut, error) {
		res, err := pc.GetIndexerCategories(ctx)
		if err != nil {
			return nil, categoriesOut{}, err
		}
		out := categoriesOut{}
		for _, c := range res.Model {
			out.Categories = append(out.Categories, categoryRow{ID: c.Id, Name: c.Name})
			for _, s := range c.SubCategories {
				out.Categories = append(out.Categories, categoryRow{ID: s.Id, Name: s.Name, Parent: c.Id})
			}
		}

		return nil, out, nil
	})
}

// categoryNames maps every standard category id to its name, subcategories
// as "Parent/Child".
func (r *registry) categoryNames(ctx context.Context) (map[int]string, error) {
	res, err := r.client.GetIndexerCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, c := range res.Model {
		out[c.Id] = c.Name
		for _, s := range c.SubCategories {
			name := s.Name
			if !strings.Contains(name, "/") {
				name = c.Name + "/" + s.Name
			}
			out[s.Id] = name
		}
	}

	return out, nil
}

// categoryIDs reads categories a caller gave as numbers or names ("5000",
// "tv", "Movies/HD"), matched against the standard list.
func (r *registry) categoryIDs(ctx context.Context, refs []string) ([]int, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	names, err := r.categoryNames(ctx)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if n, err := strconv.Atoi(ref); err == nil {
			out = append(out, n)
			continue
		}
		found := 0
		for id, name := range names {
			if strings.EqualFold(name, ref) {
				found = id
				break
			}
		}
		if found == 0 {
			return nil, fmt.Errorf("no category %q: give its number, or a name category_list lists", ref)
		}
		out = append(out, found)
	}

	return out, nil
}
