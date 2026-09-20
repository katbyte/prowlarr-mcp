package tools

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
)

// Which indexers reach which applications. Prowlarr keeps the answer to
// itself (its map of synced indexers is not in the API), so it is worked out
// here the way ApplicationService and each application decide it, from
// Prowlarr's source:
//
//   - the application syncs at all (its sync level is not disabled);
//   - the indexer is enabled;
//   - the application has no tags, or shares one with the indexer;
//   - the indexer searches the way the application asks (Sonarr needs TV
//     search or a plain search, Radarr movie search, Lidarr music, Readarr
//     books; Whisparr, Mylar and LazyLibrarian take any);
//   - the indexer carries at least one of the categories the application
//     syncs.
//
// The live suite proves this against what Prowlarr actually pushes to a fake
// application.

// searchNeeds is the search an application needs an indexer to offer, beyond
// a plain one: "" for none.
var searchNeeds = map[string]string{
	"Sonarr":  "tv",
	"Radarr":  "movie",
	"Lidarr":  "music",
	"Readarr": "book",
}

// appCategories are the categories an application syncs: its sync
// categories, and Sonarr's anime ones.
func appCategories(app *prowlarr.ApplicationResource) []int {
	cats := fieldInts(app.Fields, "syncCategories")
	for _, c := range fieldInts(app.Fields, "animeSyncCategories") {
		if !slices.Contains(cats, c) {
			cats = append(cats, c)
		}
	}
	slices.Sort(cats)

	return cats
}

// indexerCategories are every category an indexer carries, its
// subcategories included.
func indexerCategories(i *prowlarr.IndexerResource) []int {
	if i.Capabilities == nil {
		return nil
	}
	var out []int
	var walk func([]prowlarr.IndexerCategory)
	walk = func(cats []prowlarr.IndexerCategory) {
		for _, c := range cats {
			if !slices.Contains(out, c.Id) {
				out = append(out, c.Id)
			}
			walk(c.SubCategories)
		}
	}
	walk(i.Capabilities.Categories)
	slices.Sort(out)

	return out
}

// searches reports which kinds of search an indexer offers.
func searches(i *prowlarr.IndexerResource) map[string]bool {
	out := map[string]bool{}
	if c := i.Capabilities; c != nil {
		out["search"] = len(c.SearchParams) > 0
		out["tv"] = len(c.TvSearchParams) > 0
		out["movie"] = len(c.MovieSearchParams) > 0
		out["music"] = len(c.MusicSearchParams) > 0
		out["book"] = len(c.BookSearchParams) > 0
	}

	return out
}

// syncVerdict says whether an indexer reaches an application, and if not,
// why not.
type syncVerdict struct {
	Synced bool
	Reason string // the first rule the pair fails, in words; "" when synced
	Rule   string // a short fixed name for that rule, for grouping
}

// syncs works out whether Prowlarr syncs an indexer to an application.
func syncs(app *prowlarr.ApplicationResource, idx *prowlarr.IndexerResource, tags map[int]string) syncVerdict {
	if app.SyncLevel == prowlarr.ApplicationSyncLevelDisabled {
		return syncVerdict{Rule: "app_sync_disabled", Reason: app.Name + "'s sync level is disabled"}
	}
	if !boolv(idx.Enable) {
		return syncVerdict{Rule: "indexer_disabled", Reason: idx.Name + " is disabled"}
	}
	if len(app.Tags) > 0 && !slices.ContainsFunc(app.Tags, func(t int) bool { return slices.Contains(idx.Tags, t) }) {
		return syncVerdict{Rule: "no_shared_tag", Reason: fmt.Sprintf("%s only takes indexers tagged %s, and %s has %s",
			app.Name, strings.Join(labels(app.Tags, tags), " or "), idx.Name, tagList(labels(idx.Tags, tags)))}
	}
	have := searches(idx)
	if need := searchNeeds[app.Implementation]; need != "" && !have[need] && !have["search"] {
		return syncVerdict{Rule: "no_search_type", Reason: fmt.Sprintf("%s needs an indexer with %s search, and %s offers %s",
			app.Name, need, idx.Name, searchList(have))}
	}
	cats := appCategories(app)
	if !slices.ContainsFunc(indexerCategories(idx), func(c int) bool { return slices.Contains(cats, c) }) {
		return syncVerdict{Rule: "no_shared_category", Reason: fmt.Sprintf("%s syncs categories %s, and %s carries none of them (it has %s)",
			app.Name, categoryRanges(cats), idx.Name, categoryRanges(topCategories(idx)))}
	}

	return syncVerdict{Synced: true}
}

// whyNothing says why an application receives none of the indexers, a clause
// per rule they fail rather than one per indexer: which rule, how many, and
// a few of their names.
func whyNothing(app *prowlarr.ApplicationResource, byRule map[string][]string, tags map[int]string) string {
	var parts []string
	for _, rule := range sortedKeys(byRule) {
		names := byRule[rule]
		some := names
		if len(some) > 3 {
			some = append(slices.Clone(some[:3]), fmt.Sprintf("%d more", len(names)-3))
		}
		who := fmt.Sprintf("%d (%s)", len(names), strings.Join(some, ", "))
		switch rule {
		case "no_shared_tag":
			parts = append(parts, fmt.Sprintf("it only takes indexers tagged %s, which %s do not carry", strings.Join(labels(app.Tags, tags), " or "), who))
		case "no_shared_category":
			parts = append(parts, fmt.Sprintf("%s carry none of the categories it syncs (%s)", who, categoryRanges(appCategories(app))))
		case "no_search_type":
			parts = append(parts, fmt.Sprintf("%s lack the %s search it needs", who, searchNeeds[app.Implementation]))
		default:
			parts = append(parts, fmt.Sprintf("%s: %s", rule, who))
		}
	}

	return strings.Join(parts, "; ")
}

// topCategories are an indexer's top-level categories, 2000 for movies, 5000
// for TV, the shortest honest summary of what it carries.
func topCategories(i *prowlarr.IndexerResource) []int {
	if i.Capabilities == nil {
		return nil
	}
	out := make([]int, 0, len(i.Capabilities.Categories))
	for _, c := range i.Capabilities.Categories {
		out = append(out, c.Id)
	}
	slices.Sort(out)

	return out
}

func tagList(l []string) string {
	if len(l) == 0 {
		return "no tags"
	}

	return "tags " + strings.Join(l, ", ")
}

func searchList(have map[string]bool) string {
	var out []string
	for _, k := range []string{"search", "tv", "movie", "music", "book"} {
		if have[k] {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return "no search at all"
	}

	return strings.Join(out, ", ") + " search"
}

// categoryRanges writes a sorted list of categories compactly: 5000, 5010,
// 5020 and 5030 are "5000-5030"; a gap of more than ten starts a new range.
func categoryRanges(cats []int) string {
	if len(cats) == 0 {
		return "none"
	}
	cats = slices.Clone(cats)
	slices.Sort(cats)
	var parts []string
	start, prev := cats[0], cats[0]
	flush := func() {
		if start == prev {
			parts = append(parts, strconv.Itoa(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, c := range cats[1:] {
		if c-prev > 10 {
			flush()
			start = c
		}
		prev = c
	}
	flush()

	return strings.Join(parts, ", ")
}
