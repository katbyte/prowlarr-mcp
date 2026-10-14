package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
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

	return r.tagRows(ctx, idx, apps.Answer, proxies.Answer)
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
	appNames, proxyNames, noteNames := map[prowlarr.ApplicationID]string{}, map[prowlarr.IndexerProxyID]string{}, map[prowlarr.NotificationID]string{}
	for _, a := range apps {
		appNames[a.Id] = a.Name
	}
	for _, p := range proxies {
		proxyNames[p.Id] = p.Name
	}
	for _, n := range notes.Answer {
		noteNames[n.Id] = n.Name
	}
	out := make([]tagRow, 0, len(res.Answer))
	for _, t := range res.Answer {
		row := tagRow{
			ID: int(t.Id), Label: t.Label, Indexers: namesOf(t.IndexerIds, indexerNames(idx)), Apps: namesOf(t.ApplicationIds, appNames),
			Proxies: namesOf(t.IndexerProxyIds, proxyNames), Notifications: namesOf(t.NotificationIds, noteNames),
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
		pageOut
		Tags []tagRow `json:"tags"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "tag_list",
		Description: "Every tag with what carries it - indexers, applications, proxies, notifications. Tags route indexers: an application with tags receives only the indexers sharing one, and an indexer goes through the proxies sharing one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, listOut, error) {
		tags, err := r.tagDetails(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		page, where := paged(tags, in.Limit, in.Offset, defaultLimit)

		return nil, listOut{pageOut: where, Tags: page}, nil
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

		// the tags are read before one is added: held, so that two calls
		// making the same tag do not each find it missing
		unlock := r.locks.ByString(tagsLock)
		defer unlock()

		res, err := pc.GetTag(ctx)
		if err != nil {
			return nil, tagRow{}, err
		}
		if slices.ContainsFunc(res.Answer, func(t prowlarr.TagResource) bool { return strings.EqualFold(t.Label, label) }) {
			return nil, tagRow{}, fmt.Errorf("tag %q already exists", label)
		}
		made, err := pc.PostTag(ctx, prowlarr.TagResource{Label: label})
		if err != nil {
			return nil, tagRow{}, err
		}

		return nil, tagRow{ID: int(made.Answer.Id), Label: made.Answer.Label, Unused: true}, nil
	})

	add(r, writeTool, &mcp.Tool{
		Name:        "tag_edit",
		Description: "Rename a tag. Everything that carries it carries the new name, so what reaches which application and goes through which proxy stays as it was.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Tag   string `json:"tag"   jsonschema:"the tag to rename, by its label or id"`
		Label string `json:"label" jsonschema:"its new label, stored lower case"`
	},
	) (*mcp.CallToolResult, tagRow, error) {
		label := strings.ToLower(strings.TrimSpace(in.Label))
		if label == "" {
			return nil, tagRow{}, errors.New("give the tag its new label")
		}

		// the tags are read before one is written: held, so that a tag made
		// under the new label in the meantime is seen
		unlock := r.locks.ByString(tagsLock)
		defer unlock()

		res, err := pc.GetTag(ctx)
		if err != nil {
			return nil, tagRow{}, err
		}
		ref := strings.TrimSpace(in.Tag)
		i := slices.IndexFunc(res.Answer, func(t prowlarr.TagResource) bool {
			return strings.EqualFold(t.Label, ref) || strconv.Itoa(int(t.Id)) == ref
		})
		if i < 0 {
			have := make([]string, 0, len(res.Answer))
			for _, t := range res.Answer {
				have = append(have, t.Label)
			}
			slices.Sort(have)

			return nil, tagRow{}, fmt.Errorf("no tag %q (have: %s)", in.Tag, strings.Join(have, ", "))
		}
		tag := res.Answer[i]
		if slices.ContainsFunc(res.Answer, func(t prowlarr.TagResource) bool { return t.Id != tag.Id && strings.EqualFold(t.Label, label) }) {
			return nil, tagRow{}, fmt.Errorf("tag %q already exists: two tags cannot share a label, and what carries %q would have to be moved to it one thing at a time", label, tag.Label)
		}
		tag.Label = label
		if _, err := pc.PutTagById(ctx, tag.Id, tag); err != nil {
			return nil, tagRow{}, err
		}

		// read back with what carries it, which is what a rename leaves alone
		rows, err := r.tagDetails(ctx)
		if err != nil {
			return nil, tagRow{}, err
		}
		if j := slices.IndexFunc(rows, func(row tagRow) bool { return row.ID == int(tag.Id) }); j >= 0 {
			return nil, rows[j], nil
		}

		return nil, tagRow{}, fmt.Errorf("the rename was accepted, but tag %d is no longer listed", tag.Id)
	})

	add(r, writeTool, &mcp.Tool{
		Name:        "tag_delete",
		Description: "Delete a tag nothing carries (tag_list and audit_tags find them). Prowlarr refuses to delete a tag in use, so this never changes what reaches an application or goes through a proxy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Label string `json:"label" jsonschema:"the tag to delete"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		unlock := r.locks.ByString(tagsLock)
		defer unlock()

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
		if _, err := pc.DeleteTagById(ctx, prowlarr.TagID(t.ID)); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: t.Label}, nil
	})
}

// namesOf is the names of the ids that have one, sorted.
func namesOf[K comparable](ids []K, byID map[K]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	slices.Sort(out)

	return out
}
