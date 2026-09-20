package tools

import (
	"context"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Notifications are listed and tested, not set up: which chat service to
// tell about a grab is a person's plumbing, not a judgment call.

func registerNotificationTools(r *registry) {
	pc := r.client

	type notificationRow struct {
		Name     string         `json:"name"`
		Kind     string         `json:"kind"     jsonschema:"e.g. Discord, Email, Pushover, Webhook"`
		On       []string       `json:"on"       jsonschema:"what it is sent for: grab, health_issue, health_restored, application_update"`
		Tags     []string       `json:"tags"`
		Settings map[string]any `json:"settings" jsonschema:"without credentials"`
	}
	type listOut struct {
		Notifications []notificationRow `json:"notifications"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "notification_list",
		Description: "The notifications Prowlarr sends - to Discord, email, Pushover, a webhook and the rest - and what each is sent for: grabs, health issues and their recovery, application updates.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetNotification(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		tags, err := r.tagLabels(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{}
		for _, n := range res.Model {
			row := notificationRow{Name: n.Name, Kind: n.Implementation, Tags: labels(n.Tags, tags)}
			row.Settings, _ = settings(n.Fields)
			for _, on := range []struct {
				set  *bool
				name string
			}{{n.OnGrab, "grab"}, {n.OnHealthIssue, "health_issue"}, {n.OnHealthRestored, "health_restored"}, {n.OnApplicationUpdate, "application_update"}} {
				if boolv(on.set) {
					row.On = append(row.On, on.name)
				}
			}
			out.Notifications = append(out.Notifications, row)
		}

		return nil, out, nil
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

		return nil, testOut{Results: []testRow{row}}, err
	})
}
