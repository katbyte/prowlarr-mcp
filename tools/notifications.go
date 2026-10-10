package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A notification is something Prowlarr tells someone: a grab, a health issue
// and its recovery, an update. Each kind (Discord, email, a webhook...) has
// its own settings and supports its own events.

type notificationRow struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	Kind           string         `json:"kind"            jsonschema:"e.g. Discord, Email, Pushover, Webhook"`
	On             []string       `json:"on"              jsonschema:"what it is sent for: grab, health_issue, health_restored, application_update"`
	HealthWarnings bool           `json:"health_warnings" jsonschema:"health warnings are sent as well as errors"`
	ManualGrabs    bool           `json:"manual_grabs"    jsonschema:"a grab made from Prowlarr's own search is sent as well as the applications' grabs"`
	Tags           []string       `json:"tags"`
	Settings       map[string]any `json:"settings"        jsonschema:"without credentials"`
}

// notificationEvent is one thing a notification can be sent for: its switch
// on a notification, and whether the notification's kind can be sent for it.
type notificationEvent struct {
	name      string
	on        func(n *prowlarr.NotificationResource) **bool
	supported func(n *prowlarr.NotificationResource) *bool
}

var notificationEvents = []notificationEvent{
	{"grab", func(n *prowlarr.NotificationResource) **bool { return &n.OnGrab }, func(n *prowlarr.NotificationResource) *bool { return n.SupportsOnGrab }},
	{"health_issue", func(n *prowlarr.NotificationResource) **bool { return &n.OnHealthIssue }, func(n *prowlarr.NotificationResource) *bool { return n.SupportsOnHealthIssue }},
	{"health_restored", func(n *prowlarr.NotificationResource) **bool { return &n.OnHealthRestored }, func(n *prowlarr.NotificationResource) *bool { return n.SupportsOnHealthRestored }},
	{"application_update", func(n *prowlarr.NotificationResource) **bool { return &n.OnApplicationUpdate }, func(n *prowlarr.NotificationResource) *bool { return n.SupportsOnApplicationUpdate }},
}

// sentFor is what a notification is sent for, by name.
func sentFor(n *prowlarr.NotificationResource) []string {
	var out []string
	for _, e := range notificationEvents {
		if boolv(*e.on(n)) {
			out = append(out, e.name)
		}
	}

	return out
}

// sendFor switches events on or off on a notification, refusing one its kind
// cannot be sent for and one that is no event at all.
func sendFor(n *prowlarr.NotificationResource, names []string, on bool) error {
	for _, name := range names {
		i := slices.IndexFunc(notificationEvents, func(e notificationEvent) bool { return strings.EqualFold(e.name, strings.TrimSpace(name)) })
		if i < 0 {
			return fmt.Errorf("no event %q: a notification is sent for grab, health_issue, health_restored or application_update", name)
		}
		e := notificationEvents[i]
		if on && !boolv(e.supported(n)) {
			return fmt.Errorf("%s cannot be sent for %s", n.Implementation, e.name)
		}
		*e.on(n) = new(on)
	}

	return nil
}

// sendError explains a notification Prowlarr would not save: it sends a test
// through one that is sent for something before saving it, and a test that
// cannot be sent is a 500 with why, where the other providers answer a 400.
func sendError(what string, err error) error {
	if status := client.StatusCode(err); status == http.StatusBadRequest || status >= http.StatusInternalServerError {
		return fmt.Errorf("%w (Prowlarr sends a test through %s before saving it; fix what the test says, or pass force=true to save it anyway)", err, what)
	}

	return err
}

func projectNotification(n *prowlarr.NotificationResource, tags map[int]string) notificationRow {
	row := notificationRow{ID: n.Id, Name: n.Name, Kind: n.Implementation, On: sentFor(n), HealthWarnings: boolv(n.IncludeHealthWarnings), ManualGrabs: boolv(n.IncludeManualGrabs), Tags: labels(n.Tags, tags)}
	row.Settings, _ = settings(n.Fields)

	return row
}

func registerNotificationTools(r *registry) {
	pc := r.client

	type listOut struct {
		pageOut
		Notifications []notificationRow `json:"notifications"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "notification_list",
		Description: "The notifications Prowlarr sends - to Discord, email, Pushover, a webhook and the rest - and what each is sent for: grabs, health issues and their recovery, application updates.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetNotification(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		page, where := paged(res.Model, in.Limit, in.Offset, defaultLimit)
		out := listOut{pageOut: where}
		for i := range page {
			out.Notifications = append(out.Notifications, projectNotification(&page[i], tags))
		}

		return nil, out, nil
	})

	type addIn struct {
		Kind           string         `json:"kind"                      jsonschema:"what it is sent through, e.g. Discord, Email, Pushover, Webhook, Telegram, Gotify, Ntfy, Slack, Apprise"`
		Name           string         `json:"name,omitempty"            jsonschema:"default the kind"`
		Settings       map[string]any `json:"settings,omitempty"        jsonschema:"its settings by name, e.g. webHookUrl for Discord, url for Webhook, server, from and to for Email; a name the kind does not have is refused with the ones it does"`
		On             []string       `json:"on"                        jsonschema:"what to send it for: grab, health_issue, health_restored, application_update"`
		HealthWarnings *bool          `json:"health_warnings,omitempty" jsonschema:"send health warnings as well as errors"`
		ManualGrabs    *bool          `json:"manual_grabs,omitempty"    jsonschema:"with on grab: also send a grab made from Prowlarr's own search, not only the applications' grabs"`
		Tags           []string       `json:"tags,omitempty"            jsonschema:"created when new"`
		Force          bool           `json:"force,omitempty"           jsonschema:"save it even if Prowlarr's test of it fails"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_add",
		Description: "Add a notification: where Prowlarr sends word of a grab, a health issue and its recovery, or an update - Discord, email, Pushover, a webhook and the rest - with its settings by name and what it is sent for. Prowlarr sends a test through it first and refuses one that fails, unless force is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, notificationRow, error) {
		schema, err := pc.GetNotificationSchema(ctx)
		if err != nil {
			return nil, notificationRow{}, err
		}
		var n *prowlarr.NotificationResource
		kinds := make([]string, 0, len(schema.Model))
		for i := range schema.Model {
			if strings.EqualFold(schema.Model[i].Implementation, in.Kind) {
				n = &schema.Model[i]
			}
			kinds = append(kinds, schema.Model[i].Implementation)
		}
		if n == nil {
			slices.Sort(kinds)
			return nil, notificationRow{}, fmt.Errorf("no notification kind %q (have: %s)", in.Kind, strings.Join(kinds, ", "))
		}
		if len(in.On) == 0 {
			return nil, notificationRow{}, errors.New("say what to send it for with on: grab, health_issue, health_restored or application_update")
		}

		n.Name = n.Implementation
		if in.Name != "" {
			n.Name = in.Name
		}
		n.Presets = nil
		if in.HealthWarnings != nil {
			n.IncludeHealthWarnings = in.HealthWarnings
		}
		if in.ManualGrabs != nil {
			n.IncludeManualGrabs = in.ManualGrabs
		}
		if err := setFields(n.Fields, in.Settings); err != nil {
			return nil, notificationRow{}, fmt.Errorf("%s: %w", n.Name, err)
		}
		if n.Tags, err = r.makeTags(ctx, in.Tags); err != nil {
			return nil, notificationRow{}, err
		}
		if err := sendFor(n, in.On, true); err != nil {
			return nil, notificationRow{}, err
		}

		// a notification is on when it is sent for something: a forced add
		// creates it sent for nothing, which is not tested, and says what it
		// is sent for with the update
		made, err := forcedAdd(in.Force,
			func(on bool) { _ = sendFor(n, in.On, on) },
			func() (*prowlarr.NotificationResource, error) {
				res, addErr := pc.PostNotification(ctx, *n, prowlarr.PostNotificationOperationOptions{ForceSave: new(in.Force)})
				return res.Model, addErr
			},
			func(created *prowlarr.NotificationResource) (*prowlarr.NotificationResource, error) {
				n.Id = created.Id
				res, saveErr := pc.PutNotificationById(ctx, created.Id, *n, prowlarr.PutNotificationByIdOperationOptions{ForceSave: new(true)})
				return res.Model, saveErr
			})
		if err != nil {
			return nil, notificationRow{}, sendError("the notification", err)
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, notificationRow{}, err
		}

		return nil, projectNotification(made, tags), nil
	})

	type editIn struct {
		Notification   string         `json:"notification"              jsonschema:"the notification to change, by name or id"`
		Name           string         `json:"name,omitempty"            jsonschema:"a new name"`
		Settings       map[string]any `json:"settings,omitempty"        jsonschema:"settings by name, as notification_list shows them"`
		On             []string       `json:"on,omitempty"              jsonschema:"replace what it is sent for with these: grab, health_issue, health_restored, application_update"`
		AddOn          []string       `json:"add_on,omitempty"          jsonschema:"events to send it for as well"`
		RemoveOn       []string       `json:"remove_on,omitempty"       jsonschema:"events to stop sending it for"`
		HealthWarnings *bool          `json:"health_warnings,omitempty" jsonschema:"send health warnings as well as errors"`
		ManualGrabs    *bool          `json:"manual_grabs,omitempty"    jsonschema:"also send a grab made from Prowlarr's own search, not only the applications' grabs"`
		Tags           []string       `json:"tags,omitempty"            jsonschema:"replace its tags with these, created when new"`
		AddTags        []string       `json:"add_tags,omitempty"        jsonschema:"tags to add, created when new"`
		RemoveTags     []string       `json:"remove_tags,omitempty"`
		Force          bool           `json:"force,omitempty"           jsonschema:"save it even if Prowlarr's test of it fails"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_edit",
		Description: "Change a notification: its name, its settings by name, what it is sent for, whether health warnings are sent, or its tags. Prowlarr sends a test through it before saving unless force is set; notification_test sends one at any time.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, notificationRow, error) {
		all, err := pc.GetNotification(ctx)
		if err != nil {
			return nil, notificationRow{}, err
		}
		found, err := find(all.Model, in.Notification, "notification")
		if err != nil {
			return nil, notificationRow{}, err
		}

		// what is sent back is the whole notification, so it is read once no
		// other edit of it can run
		unlock := editing[prowlarr.NotificationResource](r, found.Id, len(in.Tags)+len(in.AddTags) > 0)
		defer unlock()

		read, err := pc.GetNotificationById(ctx, found.Id)
		if err != nil {
			return nil, notificationRow{}, err
		}
		n := read.Model

		changed := in.Name != "" || len(in.Settings) > 0 || in.On != nil || len(in.AddOn)+len(in.RemoveOn) > 0 || in.HealthWarnings != nil || in.ManualGrabs != nil || in.Tags != nil || len(in.AddTags)+len(in.RemoveTags) > 0
		if !changed {
			return nil, notificationRow{}, errors.New("nothing to change: name a setting to edit")
		}
		if in.Name != "" {
			n.Name = in.Name
		}
		if in.HealthWarnings != nil {
			n.IncludeHealthWarnings = in.HealthWarnings
		}
		if in.ManualGrabs != nil {
			n.IncludeManualGrabs = in.ManualGrabs
		}
		if err := setFields(n.Fields, in.Settings); err != nil {
			return nil, notificationRow{}, fmt.Errorf("%s: %w", n.Name, err)
		}
		if in.On != nil {
			if err := sendFor(n, sentFor(n), false); err != nil {
				return nil, notificationRow{}, err
			}
			if err := sendFor(n, in.On, true); err != nil {
				return nil, notificationRow{}, err
			}
		}
		if err := sendFor(n, in.AddOn, true); err != nil {
			return nil, notificationRow{}, err
		}
		if err := sendFor(n, in.RemoveOn, false); err != nil {
			return nil, notificationRow{}, err
		}
		if in.Tags != nil || in.AddTags != nil || in.RemoveTags != nil {
			var set, addIDs, removeIDs []int
			if set, err = r.resolveTags(ctx, in.Tags, true); err != nil {
				return nil, notificationRow{}, err
			}
			if addIDs, err = r.resolveTags(ctx, in.AddTags, true); err != nil {
				return nil, notificationRow{}, err
			}
			if removeIDs, err = r.resolveTags(ctx, in.RemoveTags, false); err != nil {
				return nil, notificationRow{}, err
			}
			n.Tags = editTags(n.Tags, set, addIDs, removeIDs, in.Tags != nil)
		}

		res, err := pc.PutNotificationById(ctx, n.Id, *n, prowlarr.PutNotificationByIdOperationOptions{ForceSave: new(in.Force)})
		if err != nil {
			return nil, notificationRow{}, sendError(n.Name, err)
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, notificationRow{}, err
		}

		return nil, projectNotification(res.Model, tags), nil
	})

	type testOut struct {
		Results []testRow `json:"results"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "notification_test",
		Description: "Send a test notification, one by name or every one, and say what is wrong with any that fail.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Notification string `json:"notification,omitempty" jsonschema:"one to test, by name or id; default every one"`
	},
	) (*mcp.CallToolResult, testOut, error) {
		res, err := pc.GetNotification(ctx)
		if err != nil {
			return nil, testOut{}, err
		}
		names := map[int]string{}
		for _, n := range res.Model {
			names[n.Id] = n.Name
		}
		if in.Notification == "" {
			var results []prowlarr.ProviderTestAllResult
			all, testErr := pc.PostNotificationTestAll(ctx)
			if results, err = testAllAnswer(all.HttpResponse, all.Model, testErr); err != nil {
				return nil, testOut{}, err
			}

			return nil, testOut{Results: testResults(results, names)}, nil
		}
		n, err := find(res.Model, in.Notification, "notification")
		if err != nil {
			return nil, testOut{}, err
		}
		_, err = pc.PostNotificationTest(ctx, *n, prowlarr.PostNotificationTestOperationOptions{ForceTest: new(true)})
		row, err := testOne(n.Name, err)
		// a notification that cannot be sent answers a 500 with why, where
		// the other providers answer a 400: it is the test that failed, not
		// the asking
		if client.StatusCode(err) >= http.StatusInternalServerError {
			row.Problems = outsideLines(client.Messages(err))
			if len(row.Problems) == 0 {
				row.Problems = []string{outsideProse(err.Error())}
			}
			err = nil
		}

		return nil, testOut{Results: []testRow{row}}, err
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "notification_delete",
		Description: "Delete a notification, so Prowlarr no longer sends word through it. Without confirm nothing changes and the answer says what would go.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Notification string `json:"notification" jsonschema:"the notification to delete, by name or id"`
		confirmIn
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		all, err := pc.GetNotification(ctx)
		if err != nil {
			return nil, deletedOut{}, err
		}
		n, err := find(all.Model, in.Notification, "notification")
		if err != nil {
			return nil, deletedOut{}, err
		}
		loses := []string{"its settings and credentials, as Prowlarr holds them"}
		if on := sentFor(n); len(on) > 0 {
			loses = append(loses, "word of "+strings.Join(on, ", ")+" through it")
		}
		out, err := deleting(in.Confirm, n.Name, loses, "", func() error {
			_, delErr := pc.DeleteNotificationById(ctx, n.Id)

			return delErr
		})

		return nil, out, err
	})
}
