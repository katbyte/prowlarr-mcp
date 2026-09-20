package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A sync profile (an app profile in the API) says what the applications may
// use the indexers carrying it for: RSS, automatic search, interactive
// search, and the fewest seeders a torrent may have.

type profileRow struct {
	ID                int      `json:"id"`
	Name              string   `json:"name"`
	RSS               bool     `json:"rss"                jsonschema:"the applications watch the indexers' new releases"`
	AutomaticSearch   bool     `json:"automatic_search"   jsonschema:"the applications search them on their own, for something wanted"`
	InteractiveSearch bool     `json:"interactive_search" jsonschema:"a person can search them from an application"`
	MinimumSeeders    int      `json:"minimum_seeders"`
	Indexers          []string `json:"indexers"           jsonschema:"the indexers using it"`
}

func projectProfile(p *prowlarr.AppProfileResource, idx []prowlarr.IndexerResource) profileRow {
	row := profileRow{
		ID: p.Id, Name: p.Name, RSS: boolv(p.EnableRss), AutomaticSearch: boolv(p.EnableAutomaticSearch),
		InteractiveSearch: boolv(p.EnableInteractiveSearch), MinimumSeeders: p.MinimumSeeders,
	}
	for _, i := range idx {
		if i.AppProfileId == p.Id {
			row.Indexers = append(row.Indexers, i.Name)
		}
	}
	slices.Sort(row.Indexers)

	return row
}

func registerProfileTools(r *registry) {
	pc := r.client

	type listOut struct {
		Profiles []profileRow `json:"profiles"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "profile_list",
		Description: "The sync profiles, which decide what the applications use an indexer for - RSS, automatic search, interactive search - and the fewest seeders a torrent may have, with the indexers using each.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetAppProfile(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{}
		for i := range res.Model {
			out.Profiles = append(out.Profiles, projectProfile(&res.Model[i], idx))
		}

		return nil, out, nil
	})

	type createIn struct {
		Name string `json:"name"`
		profileFields
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "profile_create",
		Description: "Create a sync profile - which of RSS, automatic and interactive search the applications may use an indexer for (each on unless turned off), and the fewest seeders a torrent may have (default 1) - to give to indexers.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, profileRow, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, profileRow{}, errors.New("give the profile a name")
		}
		p := prowlarr.AppProfileResource{
			Name: in.Name, EnableRss: new(true), EnableAutomaticSearch: new(true), EnableInteractiveSearch: new(true), MinimumSeeders: 1,
		}
		in.apply(&p)
		res, err := pc.PostAppProfile(ctx, p)
		if err != nil {
			return nil, profileRow{}, err
		}

		return nil, projectProfile(res.Model, nil), nil
	})

	type editIn struct {
		Profile string `json:"profile"        jsonschema:"the profile to change, by name or id"`
		Name    string `json:"name,omitempty" jsonschema:"a new name"`
		profileFields
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "profile_edit",
		Description: "Change a sync profile: rename it, turn RSS, automatic or interactive search on or off for every indexer using it, or set the fewest seeders a torrent may have. The applications see the change on the next sync.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, profileRow, error) {
		p, err := r.resolveProfile(ctx, in.Profile)
		if err != nil {
			return nil, profileRow{}, err
		}
		if in.Name != "" {
			p.Name = in.Name
		}
		in.apply(p)
		res, err := pc.PutAppProfileById(ctx, p.Id, *p)
		if err != nil {
			return nil, profileRow{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, profileRow{}, err
		}

		return nil, projectProfile(res.Model, idx), nil
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "profile_delete",
		Description: "Delete a sync profile no indexer uses. Prowlarr keeps at least one, and refuses to delete one in use: move its indexers to another first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Profile string `json:"profile" jsonschema:"the profile to delete, by name or id"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		p, err := r.resolveProfile(ctx, in.Profile)
		if err != nil {
			return nil, deletedOut{}, err
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, deletedOut{}, err
		}
		if row := projectProfile(p, idx); len(row.Indexers) > 0 {
			return nil, deletedOut{}, fmt.Errorf("%s is used by %s; move them to another profile first (indexer_bulk_edit sync_profile)", p.Name, strings.Join(row.Indexers, ", "))
		}
		if _, err := pc.DeleteAppProfileById(ctx, p.Id); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: p.Name}, nil
	})
}

// profileFields are what a sync profile says, as the tools take it.
type profileFields struct {
	RSS               *bool `json:"rss,omitempty"`
	AutomaticSearch   *bool `json:"automatic_search,omitempty"`
	InteractiveSearch *bool `json:"interactive_search,omitempty"`
	MinimumSeeders    *int  `json:"minimum_seeders,omitempty"`
}

// apply sets what was given on a profile, leaving the rest.
func (f *profileFields) apply(p *prowlarr.AppProfileResource) {
	if f.RSS != nil {
		p.EnableRss = f.RSS
	}
	if f.AutomaticSearch != nil {
		p.EnableAutomaticSearch = f.AutomaticSearch
	}
	if f.InteractiveSearch != nil {
		p.EnableInteractiveSearch = f.InteractiveSearch
	}
	if f.MinimumSeeders != nil {
		p.MinimumSeeders = *f.MinimumSeeders
	}
}
