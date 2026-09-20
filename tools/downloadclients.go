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

// Prowlarr's own download clients take what a person grabs from Prowlarr's
// search page (release_grab), not what the applications grab: those go to
// the applications' own download clients.

type downloadClientRow struct {
	ID         int            `json:"id"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"       jsonschema:"e.g. Sabnzbd, QBittorrent, Transmission, UsenetBlackhole, TorrentBlackhole"`
	Protocol   string         `json:"protocol"`
	Enabled    bool           `json:"enabled"`
	Priority   int            `json:"priority"`
	Tags       []string       `json:"tags"`
	Categories []string       `json:"categories" jsonschema:"how Prowlarr's categories map to the client's own, e.g. tv-sonarr <- 5000"`
	Settings   map[string]any `json:"settings"   jsonschema:"its host, port, folder and the rest, without credentials"`
	Indexers   []string       `json:"indexers"   jsonschema:"indexers pinned to it"`
}

func projectDownloadClient(c *prowlarr.DownloadClientResource, idx []prowlarr.IndexerResource, tags map[int]string) downloadClientRow {
	row := downloadClientRow{
		ID: c.Id, Name: c.Name, Kind: c.Implementation, Protocol: string(c.Protocol), Enabled: boolv(c.Enable),
		Priority: c.Priority, Tags: labels(c.Tags, tags),
	}
	row.Settings, _ = settings(c.Fields)
	for _, m := range c.Categories {
		row.Categories = append(row.Categories, fmt.Sprintf("%s <- %s", m.ClientCategory, categoryRanges(m.Categories)))
	}
	for _, i := range idx {
		if i.DownloadClientId == c.Id {
			row.Indexers = append(row.Indexers, i.Name)
		}
	}
	slices.Sort(row.Indexers)

	return row
}

func registerDownloadClientTools(r *registry) {
	pc := r.client

	project := func(ctx context.Context, c *prowlarr.DownloadClientResource) (downloadClientRow, error) {
		idx, err := r.indexers(ctx)
		if err != nil {
			return downloadClientRow{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return downloadClientRow{}, err
		}

		return projectDownloadClient(c, idx, tags), nil
	}

	type listOut struct {
		Clients []downloadClientRow `json:"download_clients"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "downloadclient_list",
		Description: "Prowlarr's own download clients, which take the releases grabbed from Prowlarr itself (the applications grab to their own): each one's kind, protocol, whether it is enabled, its priority, category mapping, settings without credentials, and the indexers pinned to it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetDownloadClient(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{}
		for i := range res.Model {
			row, err := project(ctx, &res.Model[i])
			if err != nil {
				return nil, listOut{}, err
			}
			out.Clients = append(out.Clients, row)
		}

		return nil, out, nil
	})

	type addIn struct {
		Kind     string         `json:"kind"               jsonschema:"the client's kind, e.g. Sabnzbd, NzbGet, QBittorrent, Transmission, Deluge, UsenetBlackhole, TorrentBlackhole"`
		Name     string         `json:"name,omitempty"     jsonschema:"default the kind"`
		Settings map[string]any `json:"settings,omitempty" jsonschema:"its settings by name: host, port, apiKey, username, password, category, or nzbFolder and torrentFolder for a blackhole"`
		Priority int            `json:"priority,omitempty" jsonschema:"1 to 50, default 1"`
		Tags     []string       `json:"tags,omitempty"     jsonschema:"created when new"`
		Force    bool           `json:"force,omitempty"    jsonschema:"save even if Prowlarr cannot reach it"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "downloadclient_add",
		Description: "Add a download client for the releases grabbed from Prowlarr itself: SABnzbd, NZBGet, qBittorrent, Transmission, Deluge, a blackhole folder and the rest, with its settings by name. Prowlarr tests it first unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, downloadClientRow, error) {
		schema, err := pc.GetDownloadClientSchema(ctx)
		if err != nil {
			return nil, downloadClientRow{}, err
		}
		var c *prowlarr.DownloadClientResource
		kinds := make([]string, 0, len(schema.Model))
		for i := range schema.Model {
			if strings.EqualFold(schema.Model[i].Implementation, in.Kind) {
				c = &schema.Model[i]
			}
			kinds = append(kinds, schema.Model[i].Implementation)
		}
		if c == nil {
			slices.Sort(kinds)
			return nil, downloadClientRow{}, fmt.Errorf("no download client kind %q (have: %s)", in.Kind, strings.Join(kinds, ", "))
		}
		c.Name = c.Implementation
		if in.Name != "" {
			c.Name = in.Name
		}
		c.Presets, c.Enable, c.Priority = nil, new(true), limitOr(in.Priority, 1)
		if c.Tags, err = r.resolveTags(ctx, in.Tags, true); err != nil {
			return nil, downloadClientRow{}, err
		}
		if err := setFields(c.Fields, in.Settings); err != nil {
			return nil, downloadClientRow{}, fmt.Errorf("%s: %w", c.Name, err)
		}
		made, err := forcedAdd(in.Force,
			func(on bool) { c.Enable = new(on) },
			func() (*prowlarr.DownloadClientResource, error) {
				res, addErr := pc.PostDownloadClient(ctx, *c, prowlarr.PostDownloadClientOperationOptions{ForceSave: new(in.Force)})
				return res.Model, addErr
			},
			func(created *prowlarr.DownloadClientResource) (*prowlarr.DownloadClientResource, error) {
				c.Id = created.Id
				res, saveErr := pc.PutDownloadClientById(ctx, created.Id, *c, prowlarr.PutDownloadClientByIdOperationOptions{ForceSave: new(true)})
				return res.Model, saveErr
			})
		if err != nil {
			return nil, downloadClientRow{}, saveError("the download client", err)
		}
		row, err := project(ctx, made)

		return nil, row, err
	})

	type editIn struct {
		Client   string         `json:"client"             jsonschema:"the download client to change, by name or id"`
		Name     string         `json:"name,omitempty"`
		Enable   *bool          `json:"enable,omitempty"`
		Priority int            `json:"priority,omitempty" jsonschema:"1 to 50"`
		Settings map[string]any `json:"settings,omitempty" jsonschema:"settings by name"`
		Force    bool           `json:"force,omitempty"    jsonschema:"save even if Prowlarr cannot reach it"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "downloadclient_edit",
		Description: "Change one of Prowlarr's download clients: enable or disable it, rename it, set its priority, or change a setting by name. Prowlarr tests an enabled one before saving unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, downloadClientRow, error) {
		c, err := r.resolveDownloadClient(ctx, in.Client)
		if err != nil {
			return nil, downloadClientRow{}, err
		}
		if in.Name == "" && in.Enable == nil && in.Priority == 0 && len(in.Settings) == 0 {
			return nil, downloadClientRow{}, errors.New("nothing to change: name a setting to edit")
		}
		if in.Name != "" {
			c.Name = in.Name
		}
		if in.Enable != nil {
			c.Enable = in.Enable
		}
		if in.Priority != 0 {
			c.Priority = in.Priority
		}
		if err := setFields(c.Fields, in.Settings); err != nil {
			return nil, downloadClientRow{}, fmt.Errorf("%s: %w", c.Name, err)
		}
		res, err := pc.PutDownloadClientById(ctx, c.Id, *c, prowlarr.PutDownloadClientByIdOperationOptions{ForceSave: new(in.Force)})
		if err != nil {
			return nil, downloadClientRow{}, saveError(c.Name, err)
		}
		row, err := project(ctx, res.Model)

		return nil, row, err
	})

	type testOut struct {
		Results []testRow `json:"results"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "downloadclient_test",
		Description: "Test Prowlarr's download clients the way its Test button does - reach it, log in, check its category or folder - one by name or every enabled one, and say what is wrong with any that fail. Changes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Client string `json:"client,omitempty" jsonschema:"one to test, by name or id; default every enabled one"`
	},
	) (*mcp.CallToolResult, testOut, error) {
		res, err := pc.GetDownloadClient(ctx)
		if err != nil {
			return nil, testOut{}, err
		}
		names := map[int]string{}
		for _, c := range res.Model {
			names[c.Id] = c.Name
		}
		if in.Client == "" {
			var results []prowlarr.ProviderTestAllResult
			all, testErr := pc.PostDownloadClientTestAll(ctx)
			if results, err = testAllAnswer(all.HttpResponse, all.Model, testErr); err != nil {
				return nil, testOut{}, err
			}

			return nil, testOut{Results: testResults(results, names)}, nil
		}
		c, err := find(res.Model, in.Client, "download client")
		if err != nil {
			return nil, testOut{}, err
		}
		_, err = pc.PostDownloadClientTest(ctx, *c, prowlarr.PostDownloadClientTestOperationOptions{ForceTest: new(true)})
		row, err := testOne(c.Name, err)

		return nil, testOut{Results: []testRow{row}}, err
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "downloadclient_delete",
		Description: "Delete one of Prowlarr's download clients. Indexers pinned to it fall back to the first enabled client of their protocol.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Client string `json:"client" jsonschema:"the download client to delete, by name or id"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		c, err := r.resolveDownloadClient(ctx, in.Client)
		if err != nil {
			return nil, deletedOut{}, err
		}
		if _, err := pc.DeleteDownloadClientById(ctx, c.Id); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: c.Name}, nil
	})
}
