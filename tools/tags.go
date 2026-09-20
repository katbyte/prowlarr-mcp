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

// Tags are how Prowlarr routes things: an indexer reaches an application
// that has tags only when they share one, and goes through a proxy only when
// they share one. So a tag's meaning is what carries it.

type tagRow struct {
	ID            int      `json:"id"`
	Label         string   `json:"label"`
	Indexers      []string `json:"indexers"`
	Apps          []string `json:"apps"`
	Proxies       []string `json:"proxies"`
	Notifications []string `json:"notifications"`
	Unused        bool     `json:"unused"        jsonschema:"nothing carries it"`
}

// tagDetails reads every tag with what carries it, by name.
func (r *registry) tagDetails(ctx context.Context) ([]tagRow, error) {
	idx, err := r.indexers(ctx)
	if err != nil {
		return nil, err
	}
	apps, err := r.client.GetApplications(ctx)
	if err != nil {
		return nil, err
	}
	proxies, err := r.client.GetIndexerProxy(ctx)
	if err != nil {
		return nil, err
	}

	return r.tagRows(ctx, idx, apps.Model, proxies.Model)
}

// tagRows reads the tags and names what carries each, given the indexers,
// applications and proxies already read.
func (r *registry) tagRows(ctx context.Context, idx []prowlarr.IndexerResource, apps []prowlarr.ApplicationResource, proxies []prowlarr.IndexerProxyResource) ([]tagRow, error) {
	res, err := r.client.GetTagDetail(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := r.client.GetNotification(ctx)
	if err != nil {
		return nil, err
	}
	names := func(ids []int, byID map[int]string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if n, ok := byID[id]; ok {
				out = append(out, n)
			}
		}
		slices.Sort(out)
		return out
	}
	appNames, proxyNames, noteNames := map[int]string{}, map[int]string{}, map[int]string{}
	for _, a := range apps {
		appNames[a.Id] = a.Name
	}
	for _, p := range proxies {
		proxyNames[p.Id] = p.Name
	}
	for _, n := range notes.Model {
		noteNames[n.Id] = n.Name
	}
	out := make([]tagRow, 0, len(res.Model))
	for _, t := range res.Model {
		row := tagRow{
			ID: t.Id, Label: t.Label, Indexers: names(t.IndexerIds, indexerNames(idx)), Apps: names(t.ApplicationIds, appNames),
			Proxies: names(t.IndexerProxyIds, proxyNames), Notifications: names(t.NotificationIds, noteNames),
		}
		row.Unused = len(row.Indexers)+len(row.Apps)+len(row.Proxies)+len(row.Notifications) == 0
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b tagRow) int { return strings.Compare(a.Label, b.Label) })

	return out, nil
}

func registerTagTools(r *registry) {
	pc := r.client

	type listOut struct {
		Tags []tagRow `json:"tags"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "tag_list",
		Description: "Every tag with what carries it - indexers, applications, proxies, notifications. Tags route indexers: an application with tags receives only the indexers sharing one, and an indexer goes through the proxies sharing one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		tags, err := r.tagDetails(ctx)

		return nil, listOut{Tags: tags}, err
	})

	add(r, writeTool, &mcp.Tool{
		Name:        "tag_create",
		Description: "Create a tag, to route indexers to applications and proxies with. The tools that set tags also create one they are given that does not exist.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Label string `json:"label" jsonschema:"the tag, stored lower case"`
	},
	) (*mcp.CallToolResult, tagRow, error) {
		label := strings.ToLower(strings.TrimSpace(in.Label))
		if label == "" {
			return nil, tagRow{}, errors.New("give the tag a label")
		}
		res, err := pc.GetTag(ctx)
		if err != nil {
			return nil, tagRow{}, err
		}
		if slices.ContainsFunc(res.Model, func(t prowlarr.TagResource) bool { return strings.EqualFold(t.Label, label) }) {
			return nil, tagRow{}, fmt.Errorf("tag %q already exists", label)
		}
		made, err := pc.PostTag(ctx, prowlarr.TagResource{Label: label})
		if err != nil {
			return nil, tagRow{}, err
		}

		return nil, tagRow{ID: made.Model.Id, Label: made.Model.Label, Unused: true}, nil
	})

	add(r, writeTool, &mcp.Tool{
		Name:        "tag_delete",
		Description: "Delete a tag nothing carries (tag_list and audit_tags find them). Prowlarr refuses to delete a tag in use, so this never changes what reaches an application or goes through a proxy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Label string `json:"label" jsonschema:"the tag to delete"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		tags, err := r.tagDetails(ctx)
		if err != nil {
			return nil, deletedOut{}, err
		}
		i := slices.IndexFunc(tags, func(t tagRow) bool { return strings.EqualFold(t.Label, strings.TrimSpace(in.Label)) })
		if i < 0 {
			have := make([]string, 0, len(tags))
			for _, t := range tags {
				have = append(have, t.Label)
			}
			return nil, deletedOut{}, fmt.Errorf("no tag %q (have: %s)", in.Label, strings.Join(have, ", "))
		}
		t := tags[i]
		if !t.Unused {
			var users []string
			for _, group := range [][]string{t.Indexers, t.Apps, t.Proxies, t.Notifications} {
				users = append(users, group...)
			}
			return nil, deletedOut{}, fmt.Errorf("tag %q is carried by %s; take it off them first", t.Label, strings.Join(users, ", "))
		}
		if _, err := pc.DeleteTagById(ctx, t.ID); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: t.Label}, nil
	})
}
