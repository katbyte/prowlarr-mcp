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

// A saved filter is a named set of conditions on one page of Prowlarr's web
// interface: "private torrent indexers" on the indexers page, "failed grabs"
// on the history. Prowlarr only stores them: the page applies them, and no
// other part of it reads them.

type filterRow struct {
	ID      int              `json:"id"`
	Label   string           `json:"label"`
	Page    string           `json:"page"    jsonschema:"the page of the web interface it filters: indexerIndex (the indexers), indexerStats (the statistics), history or releases (search)"`
	Filters []map[string]any `json:"filters" jsonschema:"its conditions, each a key (the column), a type (equal, notEqual, contains, greaterThan...) and a value"`
}

func projectFilter(f *prowlarr.CustomFilterResource) filterRow {
	return filterRow{ID: f.Id, Label: f.Label, Page: f.Type, Filters: f.Filters}
}

// filterPages are the pages of the web interface that keep saved filters, by
// the name it stores them under.
var filterPages = []string{"indexerIndex", "indexerStats", "history", "releases"}

// findFilter picks a saved filter by its label or id, and otherwise answers
// an error naming the ones there are.
func findFilter(list []prowlarr.CustomFilterResource, ref string) (*prowlarr.CustomFilterResource, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("name the filter")
	}
	labels := make([]string, 0, len(list))
	var found []*prowlarr.CustomFilterResource
	for i := range list {
		if strings.EqualFold(list[i].Label, ref) || strconv.Itoa(list[i].Id) == ref {
			found = append(found, &list[i])
		}
		labels = append(labels, fmt.Sprintf("%s (%s, id %d)", list[i].Label, list[i].Type, list[i].Id))
	}
	slices.Sort(labels)
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return nil, fmt.Errorf("%d filters are called %q, one on each of several pages: name it by id (%s)", len(found), ref, strings.Join(labels, ", "))
	case len(labels) == 0:
		return nil, fmt.Errorf("no filter %q: Prowlarr has none saved", ref)
	}

	return nil, fmt.Errorf("no filter %q (have: %s)", ref, strings.Join(labels, ", "))
}

// checkConditions refuses a condition with no key or no type, which the web
// interface cannot show and would drop.
func checkConditions(conditions []map[string]any) error {
	if len(conditions) == 0 {
		return errors.New("give the filter at least one condition: a key, a type and a value")
	}
	for i, c := range conditions {
		for _, part := range []string{"key", "type"} {
			if s, ok := c[part].(string); !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("condition %d has no %s: each is a key (the column), a type (equal, notEqual, contains...) and a value", i+1, part)
			}
		}
		if _, ok := c["value"]; !ok {
			return fmt.Errorf("condition %d has no value", i+1)
		}
	}

	return nil
}

func registerFilterTools(r *registry) {
	pc := r.client

	type listIn struct {
		Page string `json:"page,omitempty" jsonschema:"only the filters of one page: indexerIndex, indexerStats, history or releases"`
		pageIn
	}
	type listOut struct {
		pageOut
		Filters []filterRow `json:"filters" jsonschema:"by page, then label"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "filter_list",
		Description: "The filters saved in Prowlarr's web interface - a named set of conditions on its indexers page, its statistics, its history or its search - with the conditions each holds. They narrow what a person sees on that page and nothing else: no search, sync or tool reads them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetCustomFilter(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		var rows []filterRow
		for i := range res.Model {
			if in.Page == "" || strings.EqualFold(res.Model[i].Type, in.Page) {
				rows = append(rows, projectFilter(&res.Model[i]))
			}
		}
		slices.SortStableFunc(rows, func(a, b filterRow) int {
			if c := strings.Compare(a.Page, b.Page); c != 0 {
				return c
			}

			return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
		})
		out := listOut{}
		out.Filters, out.pageOut = paged(rows, in.Limit, in.Offset, defaultLimit)

		return nil, out, nil
	})

	type createIn struct {
		Label   string           `json:"label"   jsonschema:"what to call it"`
		Page    string           `json:"page"    jsonschema:"the page it filters: indexerIndex (the indexers), indexerStats (the statistics), history or releases (search)"`
		Filters []map[string]any `json:"filters" jsonschema:"its conditions, all of which a row must meet: each a key (the column, e.g. protocol, privacy, enable, tags), a type (equal, notEqual, contains, notContains, greaterThan, lessThan...) and a value, a list for a column that holds several"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "filter_create",
		Description: "Save a filter for a page of Prowlarr's web interface: a name and the conditions a row must meet, such as the torrent indexers that are private. It appears in that page's filter menu for whoever uses the web interface, and changes nothing about what Prowlarr searches or syncs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, filterRow, error) {
		if strings.TrimSpace(in.Label) == "" {
			return nil, filterRow{}, errors.New("give the filter a label")
		}
		i := slices.IndexFunc(filterPages, func(p string) bool { return strings.EqualFold(p, strings.TrimSpace(in.Page)) })
		if i < 0 {
			return nil, filterRow{}, fmt.Errorf("no page %q: a filter is saved for %s", in.Page, strings.Join(filterPages, ", "))
		}
		if err := checkConditions(in.Filters); err != nil {
			return nil, filterRow{}, err
		}
		res, err := pc.PostCustomFilter(ctx, prowlarr.CustomFilterResource{Label: strings.TrimSpace(in.Label), Type: filterPages[i], Filters: in.Filters})
		if err != nil {
			return nil, filterRow{}, err
		}

		return nil, projectFilter(res.Model), nil
	})

	type editIn struct {
		Filter  string           `json:"filter"            jsonschema:"the filter to change, by its label or id"`
		Label   string           `json:"label,omitempty"   jsonschema:"a new label"`
		Filters []map[string]any `json:"filters,omitempty" jsonschema:"its conditions, in place of the ones it has: each a key, a type and a value"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "filter_edit",
		Description: "Change a saved filter: its label, or the conditions it holds, which replace the ones it had. The page it belongs to stays as it is.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, filterRow, error) {
		all, err := pc.GetCustomFilter(ctx)
		if err != nil {
			return nil, filterRow{}, err
		}
		found, err := findFilter(all.Model, in.Filter)
		if err != nil {
			return nil, filterRow{}, err
		}
		if strings.TrimSpace(in.Label) == "" && in.Filters == nil {
			return nil, filterRow{}, errors.New("nothing to change: give a label or conditions")
		}

		// what is sent back is the whole filter, so it is read once no other
		// edit of it can run
		unlock := editing[prowlarr.CustomFilterResource](r, found.Id, false)
		defer unlock()

		read, err := pc.GetCustomFilterById(ctx, found.Id)
		if err != nil {
			return nil, filterRow{}, err
		}
		f := read.Model
		if f == nil {
			return nil, filterRow{}, errEmptyAnswer
		}

		if label := strings.TrimSpace(in.Label); label != "" {
			f.Label = label
		}
		if in.Filters != nil {
			if err := checkConditions(in.Filters); err != nil {
				return nil, filterRow{}, err
			}
			f.Filters = in.Filters
		}
		res, err := pc.PutCustomFilterById(ctx, f.Id, *f)
		if err != nil {
			return nil, filterRow{}, err
		}

		return nil, projectFilter(res.Model), nil
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "filter_delete",
		Description: "Delete a saved filter from Prowlarr's web interface. Without confirm nothing changes and the answer says what would go.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Filter string `json:"filter" jsonschema:"the filter to delete, by its label or id"`
		confirmIn
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		all, err := pc.GetCustomFilter(ctx)
		if err != nil {
			return nil, deletedOut{}, err
		}
		f, err := findFilter(all.Model, in.Filter)
		if err != nil {
			return nil, deletedOut{}, err
		}
		loses := []string{fmt.Sprintf("its %d conditions on the %s page, which only it records", len(f.Filters), f.Type)}
		out, err := deleting(in.Confirm, f.Label, loses, "", func() error {
			_, delErr := pc.DeleteCustomFilterById(ctx, f.Id)

			return delErr
		})

		return nil, out, err
	})
}
