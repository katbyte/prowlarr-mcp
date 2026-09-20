package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// syncLevels are the sync levels an application takes, by the names the
// tools use.
var syncLevels = map[string]prowlarr.ApplicationSyncLevel{
	"full":     prowlarr.ApplicationSyncLevelFullSync,
	"fullsync": prowlarr.ApplicationSyncLevelFullSync,
	"add":      prowlarr.ApplicationSyncLevelAddOnly,
	"addonly":  prowlarr.ApplicationSyncLevelAddOnly,
	"disabled": prowlarr.ApplicationSyncLevelDisabled,
	"off":      prowlarr.ApplicationSyncLevelDisabled,
}

func syncLevel(s string) (prowlarr.ApplicationSyncLevel, error) {
	l, ok := syncLevels[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return "", fmt.Errorf("sync_level must be full, addOnly or disabled, got %q", s)
	}

	return l, nil
}

// appRow is an application as the listings show it.
type appRow struct {
	ID              int      `json:"id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"             jsonschema:"Sonarr, Radarr, Lidarr, Readarr, Whisparr, Mylar or LazyLibrarian"`
	SyncLevel       string   `json:"sync_level"       jsonschema:"fullSync keeps the application's indexers matching Prowlarr's, addOnly only adds, disabled does nothing"`
	Tags            []string `json:"tags"             jsonschema:"only indexers sharing one of these reach it; none means every indexer"`
	BaseURL         string   `json:"base_url"`
	ProwlarrURL     string   `json:"prowlarr_url"     jsonschema:"the address the application reaches Prowlarr by, which is written into every indexer it receives"`
	SyncCategories  string   `json:"sync_categories"  jsonschema:"the categories an indexer must carry one of to reach it"`
	IndexersSynced  int      `json:"indexers_synced"  jsonschema:"how many of Prowlarr's indexers it receives"`
	IndexersSkipped int      `json:"indexers_skipped" jsonschema:"how many enabled indexers it does not receive"`
}

func projectApp(a *prowlarr.ApplicationResource, idx []prowlarr.IndexerResource, tags map[int]string) appRow {
	row := appRow{
		ID: a.Id, Name: a.Name, Kind: a.Implementation, SyncLevel: string(a.SyncLevel), Tags: labels(a.Tags, tags),
		BaseURL: fieldString(a.Fields, "baseUrl"), ProwlarrURL: fieldString(a.Fields, "prowlarrUrl"),
		SyncCategories: categoryRanges(appCategories(a)),
	}
	for i := range idx {
		if syncs(a, &idx[i], tags).Synced {
			row.IndexersSynced++
		} else if boolv(idx[i].Enable) {
			row.IndexersSkipped++
		}
	}

	return row
}

func registerAppTools(r *registry) {
	pc := r.client

	type listOut struct {
		Apps []appRow `json:"apps"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "app_list",
		Description: "The applications Prowlarr feeds indexers to - Sonarr, Radarr, Lidarr, Readarr and the rest: each one's sync level, tags, address, the address it reaches Prowlarr by, the categories it syncs, and how many indexers it receives and does not.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		apps, err := pc.GetApplications(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{}
		for i := range apps.Model {
			out.Apps = append(out.Apps, projectApp(&apps.Model[i], idx, tags))
		}

		return nil, out, nil
	})

	type skipped struct {
		Indexer string `json:"indexer"`
		Rule    string `json:"rule"    jsonschema:"no_shared_tag, no_shared_category, no_search_type or app_sync_disabled"`
		Why     string `json:"why"`
	}
	type getOut struct {
		appRow
		Settings   map[string]any `json:"settings"    jsonschema:"everything but the credentials"`
		SecretsSet []string       `json:"secrets_set" jsonschema:"the credentials that are set, by name"`
		Synced     []string       `json:"synced"      jsonschema:"the indexers it receives"`
		HeldBack   []string       `json:"held_back"   jsonschema:"of those, the ones Prowlarr is holding back after failures: it keeps one the application already has, but a sync adds one only once it recovers"`
		Skipped    []skipped      `json:"skipped"     jsonschema:"the enabled indexers it does not receive, and why"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "app_get",
		Description: "One application, by name or id: its settings (credentials only named), the indexers it receives, and every enabled indexer it does not with the reason - a tag it lacks, no category it syncs, no search of the kind it needs, or its sync switched off.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		App string `json:"app" jsonschema:"the application's name or id"`
	},
	) (*mcp.CallToolResult, getOut, error) {
		a, err := r.resolveApp(ctx, in.App)
		if err != nil {
			return nil, getOut{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		failing, err := r.failingNow(ctx)
		if err != nil {
			return nil, getOut{}, err
		}
		out := getOut{appRow: projectApp(a, idx, tags)}
		out.Settings, out.SecretsSet = settings(a.Fields)
		for i := range idx {
			v := syncs(a, &idx[i], tags)
			switch {
			case v.Synced:
				out.Synced = append(out.Synced, idx[i].Name)
				if _, held := failing[idx[i].Id]; held {
					out.HeldBack = append(out.HeldBack, idx[i].Name)
				}
			case boolv(idx[i].Enable):
				out.Skipped = append(out.Skipped, skipped{Indexer: idx[i].Name, Rule: v.Rule, Why: v.Reason})
			}
		}
		slices.Sort(out.Synced)
		slices.Sort(out.HeldBack)

		return nil, out, nil
	})

	type appIn struct {
		BaseURL             string         `json:"base_url,omitempty"              jsonschema:"the application's address as Prowlarr reaches it, e.g. http://sonarr:8989"`
		APIKey              string         `json:"api_key,omitempty"               jsonschema:"the application's API key, from its Settings > General"`
		ProwlarrURL         string         `json:"prowlarr_url,omitempty"          jsonschema:"Prowlarr's address as the application reaches it, written into every indexer it receives"`
		SyncLevel           string         `json:"sync_level,omitempty"            jsonschema:"full (keep its indexers matching Prowlarr's), addOnly, or disabled"`
		SyncCategories      []int          `json:"sync_categories,omitempty"       jsonschema:"the categories an indexer must carry one of to reach it; default the kind's own"`
		AnimeSyncCategories []int          `json:"anime_sync_categories,omitempty" jsonschema:"Sonarr only: its anime categories"`
		Settings            map[string]any `json:"settings,omitempty"              jsonschema:"any other setting by name"`
		Force               bool           `json:"force,omitempty"                 jsonschema:"save even if Prowlarr cannot reach the application"`
	}
	apply := func(a *prowlarr.ApplicationResource, in *appIn) ([]string, error) {
		values := map[string]any{}
		maps.Copy(values, in.Settings)
		if in.BaseURL != "" {
			values["baseUrl"] = strings.TrimRight(in.BaseURL, "/")
		}
		if in.APIKey != "" {
			values["apiKey"] = in.APIKey
		}
		if in.ProwlarrURL != "" {
			values["prowlarrUrl"] = strings.TrimRight(in.ProwlarrURL, "/")
		}
		if in.SyncCategories != nil {
			values["syncCategories"] = toAnys(in.SyncCategories)
		}
		if in.AnimeSyncCategories != nil {
			values["animeSyncCategories"] = toAnys(in.AnimeSyncCategories)
		}
		if in.SyncLevel != "" {
			l, err := syncLevel(in.SyncLevel)
			if err != nil {
				return nil, err
			}
			a.SyncLevel = l
		}
		if err := setFields(a.Fields, values); err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}

		return sortedKeys(values), nil
	}

	type addIn struct {
		Kind string   `json:"kind"           jsonschema:"Sonarr, Radarr, Lidarr, Readarr, Whisparr, Mylar or LazyLibrarian"`
		Name string   `json:"name,omitempty" jsonschema:"what to call it, default the kind"`
		Tags []string `json:"tags,omitempty" jsonschema:"only indexers sharing one of these tags will reach it; created when new"`
		appIn
	}
	type appOut struct {
		App  appRow `json:"app"`
		Next string `json:"next,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "app_add",
		Description: "Connect an application - Sonarr, Radarr, Lidarr, Readarr, Whisparr, Mylar or LazyLibrarian - so Prowlarr pushes its indexers to it: its address and API key, the address it reaches Prowlarr by, the sync level, tags and categories. Prowlarr tests the connection first unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, appOut, error) {
		schema, err := pc.GetApplicationsSchema(ctx)
		if err != nil {
			return nil, appOut{}, err
		}
		var a *prowlarr.ApplicationResource
		kinds := make([]string, 0, len(schema.Model))
		for i := range schema.Model {
			if strings.EqualFold(schema.Model[i].Implementation, in.Kind) {
				a = &schema.Model[i]
			}
			kinds = append(kinds, schema.Model[i].Implementation)
		}
		if a == nil {
			return nil, appOut{}, fmt.Errorf("no application kind %q (have: %s)", in.Kind, strings.Join(kinds, ", "))
		}
		if in.BaseURL == "" || in.APIKey == "" {
			return nil, appOut{}, errors.New("give the application's base_url and api_key")
		}
		a.Name = a.Implementation
		if in.Name != "" {
			a.Name = in.Name
		}
		a.Presets = nil
		// full unless asked otherwise: apply sets the one asked for
		a.SyncLevel = prowlarr.ApplicationSyncLevelFullSync
		if a.Tags, err = r.resolveTags(ctx, in.Tags, true); err != nil {
			return nil, appOut{}, err
		}
		if in.ProwlarrURL == "" {
			in.ProwlarrURL = r.client.Client.BaseURL
		}
		if _, err := apply(a, &in.appIn); err != nil {
			return nil, appOut{}, err
		}
		// an application is on when it syncs: a forced add creates it with
		// its sync off, and sets it with the update
		level := a.SyncLevel
		made, err := forcedAdd(in.Force && level != prowlarr.ApplicationSyncLevelDisabled,
			func(on bool) {
				a.SyncLevel = prowlarr.ApplicationSyncLevelDisabled
				if on {
					a.SyncLevel = level
				}
			},
			func() (*prowlarr.ApplicationResource, error) {
				res, addErr := pc.PostApplications(ctx, *a, prowlarr.PostApplicationsOperationOptions{ForceSave: new(in.Force)})
				return res.Model, addErr
			},
			func(created *prowlarr.ApplicationResource) (*prowlarr.ApplicationResource, error) {
				a.Id = created.Id
				res, saveErr := pc.PutApplicationsById(ctx, created.Id, *a, prowlarr.PutApplicationsByIdOperationOptions{ForceSave: new(true)})
				return res.Model, saveErr
			})
		if err != nil {
			return nil, appOut{}, saveError("the application", err)
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, appOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, appOut{}, err
		}

		next := "Prowlarr syncs the indexers to it shortly; app_sync does it now"
		if slices.Contains(localHosts, hostOf(in.ProwlarrURL)) && !slices.Contains(localHosts, hostOf(in.BaseURL)) {
			next = fmt.Sprintf("it was told to reach Prowlarr at %s, which from where it runs is itself unless it shares Prowlarr's host: give prowlarr_url as the application sees Prowlarr (app_edit); ", in.ProwlarrURL) + next
		}

		return nil, appOut{App: projectApp(made, idx, tags), Next: next}, nil
	})

	type editIn struct {
		App        string   `json:"app"                   jsonschema:"the application to change, by name or id"`
		Name       string   `json:"name,omitempty"`
		Tags       []string `json:"tags,omitempty"        jsonschema:"replace its tags, created when new; an empty list clears them, and then every indexer reaches it"`
		AddTags    []string `json:"add_tags,omitempty"`
		RemoveTags []string `json:"remove_tags,omitempty"`
		appIn
	}
	type editOut struct {
		App     appRow   `json:"app"`
		Changed []string `json:"changed"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "app_edit",
		Description: "Change an application: its address, API key, the address it reaches Prowlarr by, its sync level, tags or sync categories, or any other setting by name. Prowlarr tests the connection before saving unless force is set; app_sync pushes the result.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, editOut, error) {
		a, err := r.resolveApp(ctx, in.App)
		if err != nil {
			return nil, editOut{}, err
		}
		changed, err := apply(a, &in.appIn)
		if err != nil {
			return nil, editOut{}, err
		}
		if in.SyncLevel != "" {
			changed = append(changed, "sync_level")
		}
		if in.Name != "" && in.Name != a.Name {
			a.Name = in.Name
			changed = append(changed, "name")
		}
		if in.Tags != nil || in.AddTags != nil || in.RemoveTags != nil {
			var set, addIDs, removeIDs []int
			if set, err = r.resolveTags(ctx, in.Tags, true); err != nil {
				return nil, editOut{}, err
			}
			if addIDs, err = r.resolveTags(ctx, in.AddTags, true); err != nil {
				return nil, editOut{}, err
			}
			if removeIDs, err = r.resolveTags(ctx, in.RemoveTags, false); err != nil {
				return nil, editOut{}, err
			}
			a.Tags = editTags(a.Tags, set, addIDs, removeIDs, in.Tags != nil)
			changed = append(changed, "tags")
		}
		if len(changed) == 0 {
			return nil, editOut{}, errors.New("nothing to change: name a setting to edit")
		}
		res, err := pc.PutApplicationsById(ctx, a.Id, *a, prowlarr.PutApplicationsByIdOperationOptions{ForceSave: new(in.Force)})
		if err != nil {
			return nil, editOut{}, saveError(a.Name, err)
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, editOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, editOut{}, err
		}

		return nil, editOut{App: projectApp(res.Model, idx, tags), Changed: changed}, nil
	})

	type testIn struct {
		App string `json:"app,omitempty" jsonschema:"one to test, by name or id; default every one"`
	}
	type testOut struct {
		Results []testRow `json:"results"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "app_test",
		Description: "Test applications the way Prowlarr's Test button does - reach it, check its API key and version - one by name or every one, and say what is wrong with any that fail. Changes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
		apps, err := pc.GetApplications(ctx)
		if err != nil {
			return nil, testOut{}, err
		}
		names := map[int]string{}
		for _, a := range apps.Model {
			names[a.Id] = a.Name
		}
		if in.App == "" {
			var results []prowlarr.ProviderTestAllResult
			res, testErr := pc.PostApplicationsTestAll(ctx)
			if results, err = testAllAnswer(res.HttpResponse, res.Model, testErr); err != nil {
				return nil, testOut{}, err
			}

			return nil, testOut{Results: testResults(results, names)}, nil
		}
		a, err := find(apps.Model, in.App, "application")
		if err != nil {
			return nil, testOut{}, err
		}
		_, err = pc.PostApplicationsTest(ctx, *a, prowlarr.PostApplicationsTestOperationOptions{ForceTest: new(true)})
		row, err := testOne(a.Name, err)

		return nil, testOut{Results: []testRow{row}}, err
	})

	type syncIn struct {
		Force bool `json:"force,omitempty" jsonschema:"rewrite every indexer in the applications, not only the ones that changed"`
		Wait  int  `json:"wait,omitempty"  jsonschema:"seconds to wait for the sync, default 60; -1 queues it and returns at once"`
	}
	type syncOut struct {
		Command commandOut `json:"command"`
		Apps    []appRow   `json:"apps"    jsonschema:"what each application should now hold"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "app_sync",
		Description: "Push the indexers to every application now rather than on Prowlarr's schedule - after adding, editing or retagging indexers or applications - and say how many each receives. force rewrites every indexer in them, which repairs one edited by hand in the application.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, syncOut, error) {
		cmd, err := r.runCommand(ctx, "ApplicationIndexerSync", map[string]any{"forceSync": in.Force}, waitFor(in.Wait))
		if err != nil {
			return nil, syncOut{Command: cmd}, err
		}
		apps, err := pc.GetApplications(ctx)
		if err != nil {
			return nil, syncOut{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, syncOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, syncOut{}, err
		}
		out := syncOut{Command: cmd}
		for i := range apps.Model {
			out.Apps = append(out.Apps, projectApp(&apps.Model[i], idx, tags))
		}

		return nil, out, nil
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "app_delete",
		Description: "Disconnect an application from Prowlarr. The indexers Prowlarr put in it stay there, no longer kept in step; setting its sync level to disabled pauses it instead.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		App string `json:"app" jsonschema:"the application to remove, by name or id"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		a, err := r.resolveApp(ctx, in.App)
		if err != nil {
			return nil, deletedOut{}, err
		}
		if _, err := pc.DeleteApplicationsById(ctx, a.Id); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: a.Name}, nil
	})
}

// deletedOut is what a delete answers: the name of what it removed.
type deletedOut struct {
	Deleted string `json:"deleted"`
}

// toAnys turns a list into the []any a provider field holds.
func toAnys[T any](list []T) []any {
	out := make([]any, len(list))
	for i, v := range list {
		out[i] = v
	}

	return out
}
