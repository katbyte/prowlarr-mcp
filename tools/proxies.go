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

// A proxy carries an indexer's requests when the two share a tag: an HTTP or
// SOCKS proxy to reach a site from somewhere else, or FlareSolverr to get
// through a site's Cloudflare challenge. A proxy with no tags is used by
// nothing.

type proxyRow struct {
	ID       int            `json:"id"`
	Name     string         `json:"name"`
	Kind     string         `json:"kind"     jsonschema:"FlareSolverr, Http, Socks4 or Socks5"`
	Tags     []string       `json:"tags"     jsonschema:"indexers sharing one of these send their requests through it; none means nothing does"`
	Settings map[string]any `json:"settings" jsonschema:"its address and the rest, without credentials"`
	Indexers []string       `json:"indexers" jsonschema:"the indexers that use it"`
}

func projectProxy(p *prowlarr.IndexerProxyResource, idx []prowlarr.IndexerResource, tags map[int]string) proxyRow {
	row := proxyRow{ID: p.Id, Name: p.Name, Kind: p.Implementation, Tags: labels(p.Tags, tags)}
	row.Settings, _ = settings(p.Fields)
	for _, i := range idx {
		if slices.ContainsFunc(p.Tags, func(t int) bool { return slices.Contains(i.Tags, t) }) {
			row.Indexers = append(row.Indexers, i.Name)
		}
	}
	slices.Sort(row.Indexers)

	return row
}

func (r *registry) resolveProxy(ctx context.Context, ref string) (*prowlarr.IndexerProxyResource, error) {
	res, err := r.client.GetIndexerProxy(ctx)
	if err != nil {
		return nil, err
	}

	return find(res.Model, ref, "proxy")
}

func registerProxyTools(r *registry) {
	pc := r.client

	type listOut struct {
		Proxies []proxyRow `json:"proxies"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "proxy_list",
		Description: "The proxies indexers send their requests through - FlareSolverr for sites behind Cloudflare, HTTP and SOCKS proxies - with each one's address, its tags, and the indexers that use it by sharing one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetIndexerProxy(ctx)
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
		for i := range res.Model {
			out.Proxies = append(out.Proxies, projectProxy(&res.Model[i], idx, tags))
		}

		return nil, out, nil
	})

	type addIn struct {
		Kind     string         `json:"kind"               jsonschema:"FlareSolverr, Http, Socks4 or Socks5"`
		Name     string         `json:"name,omitempty"     jsonschema:"default the kind"`
		Host     string         `json:"host,omitempty"     jsonschema:"FlareSolverr: its URL, e.g. http://flaresolverr:8191; HTTP and SOCKS: the host name"`
		Port     int            `json:"port,omitempty"     jsonschema:"HTTP and SOCKS"`
		Tags     []string       `json:"tags"               jsonschema:"indexers sharing one of these send their requests through it; created when new"`
		Settings map[string]any `json:"settings,omitempty" jsonschema:"any other setting by name, e.g. username, password, requestTimeout"`
		Force    bool           `json:"force,omitempty"    jsonschema:"save even if Prowlarr cannot reach it"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "proxy_add",
		Description: "Add a proxy for indexers to send their requests through - FlareSolverr (the fix for a site behind Cloudflare), HTTP or SOCKS - and the tags that pick which indexers use it. Prowlarr tests it first unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, proxyRow, error) {
		schema, err := pc.GetIndexerProxySchema(ctx)
		if err != nil {
			return nil, proxyRow{}, err
		}
		var p *prowlarr.IndexerProxyResource
		kinds := make([]string, 0, len(schema.Model))
		for i := range schema.Model {
			if strings.EqualFold(schema.Model[i].Implementation, in.Kind) {
				p = &schema.Model[i]
			}
			kinds = append(kinds, schema.Model[i].Implementation)
		}
		if p == nil {
			return nil, proxyRow{}, fmt.Errorf("no proxy kind %q (have: %s)", in.Kind, strings.Join(kinds, ", "))
		}
		if len(in.Tags) == 0 {
			return nil, proxyRow{}, errors.New("give the proxy a tag: an indexer uses it only when they share one")
		}
		p.Name = p.Implementation
		if in.Name != "" {
			p.Name = in.Name
		}
		p.Presets = nil
		if p.Tags, err = r.resolveTags(ctx, in.Tags, true); err != nil {
			return nil, proxyRow{}, err
		}
		values := map[string]any{}
		maps.Copy(values, in.Settings)
		if in.Host != "" {
			values["host"] = in.Host
		}
		if in.Port != 0 {
			values["port"] = in.Port
		}
		if err := setFields(p.Fields, values); err != nil {
			return nil, proxyRow{}, fmt.Errorf("%s: %w", p.Name, err)
		}
		// a proxy is on when it has tags: a forced add creates it without
		// them, and tags it with the update
		tagged := p.Tags
		made, err := forcedAdd(in.Force,
			func(on bool) {
				p.Tags = []int{}
				if on {
					p.Tags = tagged
				}
			},
			func() (*prowlarr.IndexerProxyResource, error) {
				res, addErr := pc.PostIndexerProxy(ctx, *p, prowlarr.PostIndexerProxyOperationOptions{ForceSave: new(in.Force)})
				return res.Model, addErr
			},
			func(created *prowlarr.IndexerProxyResource) (*prowlarr.IndexerProxyResource, error) {
				p.Id = created.Id
				res, saveErr := pc.PutIndexerProxyById(ctx, created.Id, *p, prowlarr.PutIndexerProxyByIdOperationOptions{ForceSave: new(true)})
				return res.Model, saveErr
			})
		if err != nil {
			return nil, proxyRow{}, saveError("the proxy", err)
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, proxyRow{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, proxyRow{}, err
		}

		return nil, projectProxy(made, idx, tags), nil
	})

	type editIn struct {
		Proxy      string         `json:"proxy"                 jsonschema:"the proxy to change, by name or id"`
		Name       string         `json:"name,omitempty"`
		Tags       []string       `json:"tags,omitempty"        jsonschema:"replace its tags, created when new"`
		AddTags    []string       `json:"add_tags,omitempty"`
		RemoveTags []string       `json:"remove_tags,omitempty"`
		Settings   map[string]any `json:"settings,omitempty"    jsonschema:"settings by name, e.g. host, port, requestTimeout"`
		Force      bool           `json:"force,omitempty"       jsonschema:"save even if Prowlarr cannot reach it"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "proxy_edit",
		Description: "Change a proxy: its address or other settings, its name, or the tags that pick which indexers use it. Prowlarr tests it before saving unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, proxyRow, error) {
		p, err := r.resolveProxy(ctx, in.Proxy)
		if err != nil {
			return nil, proxyRow{}, err
		}
		if in.Name != "" {
			p.Name = in.Name
		}
		if in.Tags != nil || in.AddTags != nil || in.RemoveTags != nil {
			var set, addIDs, removeIDs []int
			if set, err = r.resolveTags(ctx, in.Tags, true); err != nil {
				return nil, proxyRow{}, err
			}
			if addIDs, err = r.resolveTags(ctx, in.AddTags, true); err != nil {
				return nil, proxyRow{}, err
			}
			if removeIDs, err = r.resolveTags(ctx, in.RemoveTags, false); err != nil {
				return nil, proxyRow{}, err
			}
			p.Tags = editTags(p.Tags, set, addIDs, removeIDs, in.Tags != nil)
		}
		if err := setFields(p.Fields, in.Settings); err != nil {
			return nil, proxyRow{}, fmt.Errorf("%s: %w", p.Name, err)
		}
		res, err := pc.PutIndexerProxyById(ctx, p.Id, *p, prowlarr.PutIndexerProxyByIdOperationOptions{ForceSave: new(in.Force)})
		if err != nil {
			return nil, proxyRow{}, saveError(p.Name, err)
		}
		idx, err := r.indexers(ctx)
		if err != nil {
			return nil, proxyRow{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, proxyRow{}, err
		}

		return nil, projectProxy(res.Model, idx, tags), nil
	})

	type testOut struct {
		Results []testRow `json:"results"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "proxy_test",
		Description: "Test proxies the way Prowlarr's Test button does - reach it and send a request through it - one by name or every one, and say what is wrong with any that fail. Changes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Proxy string `json:"proxy,omitempty" jsonschema:"one to test, by name or id; default every one"`
	},
	) (*mcp.CallToolResult, testOut, error) {
		res, err := pc.GetIndexerProxy(ctx)
		if err != nil {
			return nil, testOut{}, err
		}
		names := map[int]string{}
		for _, p := range res.Model {
			names[p.Id] = p.Name
		}
		if in.Proxy == "" {
			var results []prowlarr.ProviderTestAllResult
			all, testErr := pc.PostIndexerProxyTestAll(ctx)
			if results, err = testAllAnswer(all.HttpResponse, all.Model, testErr); err != nil {
				return nil, testOut{}, err
			}

			return nil, testOut{Results: testResults(results, names)}, nil
		}
		p, err := find(res.Model, in.Proxy, "proxy")
		if err != nil {
			return nil, testOut{}, err
		}
		_, err = pc.PostIndexerProxyTest(ctx, *p, prowlarr.PostIndexerProxyTestOperationOptions{ForceTest: new(true)})
		row, err := testOne(p.Name, err)

		return nil, testOut{Results: []testRow{row}}, err
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "proxy_delete",
		Description: "Delete a proxy. The indexers that used it send their requests directly from then on, which a site behind Cloudflare refuses.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Proxy string `json:"proxy" jsonschema:"the proxy to delete, by name or id"`
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		p, err := r.resolveProxy(ctx, in.Proxy)
		if err != nil {
			return nil, deletedOut{}, err
		}
		if _, err := pc.DeleteIndexerProxyById(ctx, p.Id); err != nil {
			return nil, deletedOut{}, err
		}

		return nil, deletedOut{Deleted: p.Name}, nil
	})
}
