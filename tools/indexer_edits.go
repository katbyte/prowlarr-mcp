package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/lib/client"
	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// limitsUnits are the windows an indexer's query and grab limits count over.
var limitsUnits = map[string]int{"day": 0, "hour": 1}

// seedIn are the torrent settings the applications are told to seed a
// release from an indexer to, which private trackers require.
type seedIn struct {
	SeedRatio    *float64 `json:"seed_ratio,omitempty"     jsonschema:"torrent indexers: the ratio the applications seed to before removing a download; 0 clears it"`
	SeedTime     *int     `json:"seed_time,omitempty"      jsonschema:"torrent indexers: minutes the applications seed for; 0 clears it"`
	PackSeedTime *int     `json:"pack_seed_time,omitempty" jsonschema:"torrent indexers: minutes the applications seed a season pack for; 0 clears it"`
	MinSeeders   *int     `json:"min_seeders,omitempty"    jsonschema:"torrent indexers: the fewest seeders a release may have for the applications to take it; 0 clears it"`
}

// values are the settings seedIn changes, by field name; a zero clears one.
func (s *seedIn) values() map[string]any {
	out := map[string]any{}
	orNil := func(v float64) any {
		if v == 0 {
			return nil
		}
		return v
	}
	if s.SeedRatio != nil {
		out["torrentBaseSettings.seedRatio"] = orNil(*s.SeedRatio)
	}
	if s.SeedTime != nil {
		out["torrentBaseSettings.seedTime"] = orNil(float64(*s.SeedTime))
	}
	if s.PackSeedTime != nil {
		out["torrentBaseSettings.packSeedTime"] = orNil(float64(*s.PackSeedTime))
	}
	if s.MinSeeders != nil {
		out["torrentBaseSettings.appMinimumSeeders"] = orNil(float64(*s.MinSeeders))
	}

	return out
}

// forcedAdd creates something Prowlarr tests before saving - an indexer, an
// application, a proxy, a download client - when it may fail the test. A
// create tests an enabled one and refuses it on a failure whatever forceSave
// says (forceSave skips only the test's warnings), while an update with
// forceSave skips the test altogether. So a forced add creates it switched
// off, which is not tested, and switches it on with an update. setOn switches
// what is being added on or off, however that kind says it: an indexer's
// enable flag, an application's sync level, a proxy's tags.
func forcedAdd[T any](force bool, setOn func(on bool), create func() (*T, error), update func(created *T) (*T, error)) (*T, error) {
	if !force {
		return create()
	}
	setOn(false)
	made, err := create()
	if err != nil {
		return nil, err
	}
	setOn(true)

	return update(made)
}

// saveError explains a failed save: Prowlarr tests an enabled indexer before
// saving it and refuses one that fails, which force skips.
func saveError(what string, err error) error {
	if client.StatusCode(err) == http.StatusBadRequest {
		return fmt.Errorf("%w (Prowlarr tests %s before saving it; fix what the test says, or pass force=true to save it anyway)", err, what)
	}

	return err
}

func registerIndexerEditTools(r *registry) {
	pc := r.client

	type editIn struct {
		Indexer        string         `json:"indexer"                   jsonschema:"the indexer to change, by name or id"`
		Name           string         `json:"name,omitempty"            jsonschema:"a new name"`
		Enable         *bool          `json:"enable,omitempty"`
		Priority       int            `json:"priority,omitempty"        jsonschema:"1 to 50, lower is preferred by the applications"`
		Tags           []string       `json:"tags,omitempty"            jsonschema:"replace its tags with these, created when new; tags decide which applications and proxies it goes to"`
		AddTags        []string       `json:"add_tags,omitempty"        jsonschema:"tags to add, created when new"`
		RemoveTags     []string       `json:"remove_tags,omitempty"`
		SyncProfile    string         `json:"sync_profile,omitempty"    jsonschema:"the sync profile, by name or id"`
		Redirect       *bool          `json:"redirect,omitempty"        jsonschema:"the applications download straight from the indexer rather than through Prowlarr (usenet indexers always do)"`
		DownloadClient string         `json:"download_client,omitempty" jsonschema:"the download client Prowlarr's own grabs from it go to, by name or id; none to unpin it"`
		BaseURL        string         `json:"base_url,omitempty"        jsonschema:"the site address it uses, ideally one of indexer_get's indexer_urls"`
		VIPExpiration  *string        `json:"vip_expiration,omitempty"  jsonschema:"when its paid membership ends, YYYY-MM-DD, for audit_vip to warn ahead of; empty clears it"`
		QueryLimit     *int           `json:"query_limit,omitempty"     jsonschema:"most queries Prowlarr sends it per limits_unit; 0 clears it"`
		GrabLimit      *int           `json:"grab_limit,omitempty"      jsonschema:"most grabs Prowlarr sends it per limits_unit; 0 clears it"`
		LimitsUnit     string         `json:"limits_unit,omitempty"     jsonschema:"day or hour"`
		Settings       map[string]any `json:"settings,omitempty"        jsonschema:"any other setting by its name in indexer_get's settings or secrets_set, e.g. apiKey, cookie, additionalParameters"`
		Force          bool           `json:"force,omitempty"           jsonschema:"save even if Prowlarr's test of the indexer fails"`
		seedIn
	}
	type editOut struct {
		Indexer indexerRow `json:"indexer"`
		Changed []string   `json:"changed" jsonschema:"what was changed"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "indexer_edit",
		Description: "Change an indexer: enable or disable it, rename it, set its priority, tags, sync profile, redirect, download client, base URL, VIP expiry, query and grab limits, seed goals, or any other setting by name. Prowlarr tests an enabled indexer before saving it and refuses one that fails unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, editOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, editOut{}, err
		}
		var changed []string
		if in.Name != "" && in.Name != i.Name {
			i.Name = in.Name
			changed = append(changed, "name")
		}
		if in.Enable != nil {
			i.Enable = in.Enable
			changed = append(changed, "enable")
		}
		if in.Priority != 0 {
			if in.Priority < 1 || in.Priority > 50 {
				return nil, editOut{}, fmt.Errorf("priority must be 1 to 50, got %d", in.Priority)
			}
			i.Priority = in.Priority
			changed = append(changed, "priority")
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
			i.Tags = editTags(i.Tags, set, addIDs, removeIDs, in.Tags != nil)
			changed = append(changed, "tags")
		}
		if in.SyncProfile != "" {
			var p *prowlarr.AppProfileResource
			if p, err = r.resolveProfile(ctx, in.SyncProfile); err != nil {
				return nil, editOut{}, err
			}
			i.AppProfileId = p.Id
			changed = append(changed, "sync_profile")
		}
		if in.Redirect != nil {
			i.Redirect = in.Redirect
			changed = append(changed, "redirect")
		}
		if in.DownloadClient != "" {
			if strings.EqualFold(in.DownloadClient, "none") {
				i.DownloadClientId = 0
			} else {
				var dc *prowlarr.DownloadClientResource
				if dc, err = r.resolveDownloadClient(ctx, in.DownloadClient); err != nil {
					return nil, editOut{}, err
				}
				i.DownloadClientId = dc.Id
			}
			changed = append(changed, "download_client")
		}
		values := in.values()
		maps.Copy(values, in.Settings)
		if in.BaseURL != "" {
			values["baseUrl"] = in.BaseURL
		}
		if in.VIPExpiration != nil {
			values["vipExpiration"] = *in.VIPExpiration
		}
		if in.QueryLimit != nil {
			values["baseSettings.queryLimit"] = nilIfZero(*in.QueryLimit)
		}
		if in.GrabLimit != nil {
			values["baseSettings.grabLimit"] = nilIfZero(*in.GrabLimit)
		}
		if in.LimitsUnit != "" {
			unit, ok := limitsUnits[strings.ToLower(in.LimitsUnit)]
			if !ok {
				return nil, editOut{}, fmt.Errorf("limits_unit must be day or hour, got %q", in.LimitsUnit)
			}
			values["baseSettings.limitsUnit"] = unit
		}
		if len(values) > 0 {
			if err := setFields(i.Fields, values); err != nil {
				return nil, editOut{}, fmt.Errorf("%s: %w", i.Name, err)
			}
			changed = append(changed, sortedKeys(values)...)
		}
		if len(changed) == 0 {
			return nil, editOut{}, errors.New("nothing to change: name a setting to edit")
		}

		res, err := pc.PutIndexerById(ctx, i.Id, *i, prowlarr.PutIndexerByIdOperationOptions{ForceSave: new(in.Force)})
		if err != nil {
			// Prowlarr reads a Newznab or Torznab site's capabilities afresh
			// whenever it saves one that is enabled, force or no force
			if client.StatusCode(err) >= http.StatusInternalServerError && slices.Contains([]string{"Newznab", "Torznab"}, i.Implementation) && boolv(i.Enable) {
				return nil, editOut{}, fmt.Errorf("%w (Prowlarr reads a %s site's capabilities whenever it saves it enabled, so while the site is down it can only be saved with enable=false)", err, i.Implementation)
			}
			return nil, editOut{}, saveError(i.Name, err)
		}
		l, err := r.lookups(ctx)
		if err != nil {
			return nil, editOut{}, err
		}

		return nil, editOut{Indexer: l.indexerRow(res.Model), Changed: changed}, nil
	})

	type bulkIn struct {
		Indexers     []string `json:"indexers,omitempty"      jsonschema:"the indexers to change, by name or id; or use the filters"`
		Protocol     string   `json:"protocol,omitempty"      jsonschema:"every usenet or every torrent indexer"`
		Privacy      string   `json:"privacy,omitempty"       jsonschema:"every public, semiPrivate or private indexer"`
		Tag          string   `json:"tag,omitempty"           jsonschema:"every indexer carrying this tag"`
		Enable       *bool    `json:"enable,omitempty"`
		Priority     int      `json:"priority,omitempty"      jsonschema:"1 to 50"`
		SyncProfile  string   `json:"sync_profile,omitempty"  jsonschema:"by name or id"`
		AddTags      []string `json:"add_tags,omitempty"      jsonschema:"created when new"`
		RemoveTags   []string `json:"remove_tags,omitempty"`
		PreferMagnet *bool    `json:"prefer_magnet,omitempty" jsonschema:"torrent indexers: hand the applications magnet links rather than .torrent files"`
		seedIn
	}
	type bulkOut struct {
		Changed  int          `json:"changed"  jsonschema:"how many indexers were changed"`
		Indexers []indexerRow `json:"indexers"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "indexer_bulk_edit",
		Description: "Change many indexers at once - named ones, or every usenet, torrent, private or tagged one: enable or disable them, set their priority or sync profile, add or remove tags, and give the torrent ones seed goals (the fix for audit_seeding). Settings not named are left as they are.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bulkIn) (*mcp.CallToolResult, bulkOut, error) {
		all, err := r.resolveIndexers(ctx, in.Indexers)
		if err != nil {
			return nil, bulkOut{}, err
		}
		l, err := r.lookups(ctx)
		if err != nil {
			return nil, bulkOut{}, err
		}
		if len(in.Indexers) == 0 && in.Protocol == "" && in.Privacy == "" && in.Tag == "" {
			return nil, bulkOut{}, errors.New("name the indexers, or pick them by protocol, privacy or tag")
		}
		filter := indexerFilter{Protocol: in.Protocol, Privacy: in.Privacy, Tag: in.Tag}
		body := prowlarr.IndexerBulkResource{Enable: in.Enable, PreferMagnetUrl: in.PreferMagnet}
		for i := range all {
			row := l.indexerRow(&all[i])
			if filter.keep(&row) {
				body.Ids = append(body.Ids, all[i].Id)
			}
		}
		if len(body.Ids) == 0 {
			return nil, bulkOut{}, errors.New("no indexer matches")
		}
		changes := 0
		if in.Enable != nil || in.PreferMagnet != nil {
			changes++
		}
		if in.Priority != 0 {
			if in.Priority < 1 || in.Priority > 50 {
				return nil, bulkOut{}, fmt.Errorf("priority must be 1 to 50, got %d", in.Priority)
			}
			body.Priority = in.Priority
			changes++
		}
		if in.SyncProfile != "" {
			var p *prowlarr.AppProfileResource
			if p, err = r.resolveProfile(ctx, in.SyncProfile); err != nil {
				return nil, bulkOut{}, err
			}
			body.AppProfileId = p.Id
			changes++
		}
		// a zero is left out of the body, which the bulk update reads as
		// "keep": clearing a seed goal is indexer_edit's job
		if in.SeedRatio != nil {
			body.SeedRatio = *in.SeedRatio
			changes++
		}
		if in.SeedTime != nil {
			body.SeedTime = *in.SeedTime
			changes++
		}
		if in.PackSeedTime != nil {
			body.PackSeedTime = *in.PackSeedTime
			changes++
		}
		if in.MinSeeders != nil {
			body.MinimumSeeders = *in.MinSeeders
			changes++
		}

		// the bulk update takes one tag change at a time: add, then remove
		var tagChanges []struct {
			apply prowlarr.ApplyTags
			ids   []int
		}
		if len(in.AddTags) > 0 {
			var ids []int
			if ids, err = r.resolveTags(ctx, in.AddTags, true); err != nil {
				return nil, bulkOut{}, err
			}
			tagChanges = append(tagChanges, struct {
				apply prowlarr.ApplyTags
				ids   []int
			}{prowlarr.ApplyTagsAdd, ids})
		}
		if len(in.RemoveTags) > 0 {
			var ids []int
			if ids, err = r.resolveTags(ctx, in.RemoveTags, false); err != nil {
				return nil, bulkOut{}, err
			}
			tagChanges = append(tagChanges, struct {
				apply prowlarr.ApplyTags
				ids   []int
			}{prowlarr.ApplyTagsRemove, ids})
		}
		if changes == 0 && len(tagChanges) == 0 {
			return nil, bulkOut{}, errors.New("nothing to change: name a setting to edit")
		}
		if len(tagChanges) > 0 {
			body.ApplyTags, body.Tags = tagChanges[0].apply, tagChanges[0].ids
		}
		res, err := pc.PutIndexerBulk(ctx, body)
		if err != nil {
			return nil, bulkOut{}, err
		}
		for _, tc := range tagChanges[min(1, len(tagChanges)):] {
			if res, err = pc.PutIndexerBulk(ctx, prowlarr.IndexerBulkResource{Ids: body.Ids, ApplyTags: tc.apply, Tags: tc.ids}); err != nil {
				return nil, bulkOut{}, err
			}
		}
		l, err = r.lookups(ctx)
		if err != nil {
			return nil, bulkOut{}, err
		}
		out := bulkOut{Changed: len(res.Model)}
		for i := range res.Model {
			out.Indexers = append(out.Indexers, l.indexerRow(&res.Model[i]))
		}
		slices.SortFunc(out.Indexers, func(a, b indexerRow) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })

		return nil, out, nil
	})

	type deleteIn struct {
		Indexer string `json:"indexer" jsonschema:"the indexer to delete, by name or id"`
	}
	type deleteOut struct {
		Deleted string `json:"deleted"`
		Note    string `json:"note"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "indexer_delete",
		Description: "Delete an indexer from Prowlarr, with its history and statistics. Applications that sync fully drop it on the next sync; disabling it instead keeps it and its history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deleteIn) (*mcp.CallToolResult, deleteOut, error) {
		i, err := r.resolveIndexer(ctx, in.Indexer)
		if err != nil {
			return nil, deleteOut{}, err
		}
		if _, err := pc.DeleteIndexerById(ctx, i.Id); err != nil {
			return nil, deleteOut{}, err
		}

		return nil, deleteOut{Deleted: i.Name, Note: "applications set to full sync drop it on the next sync (app_sync runs one now)"}, nil
	})
}

// nilIfZero sends 0 as "unset", which is how Prowlarr clears a limit.
func nilIfZero(n int) any {
	if n == 0 {
		return nil
	}

	return n
}
