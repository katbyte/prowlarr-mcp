package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// discord is a Discord notification as Prowlarr's schema and its list answer
// one: it can be sent for a grab and for health, and not for an update.
func discord(id int, name string, on ...string) map[string]any {
	n := map[string]any{
		"id": id, "name": name, "implementation": "Discord", "tags": []any{},
		"supportsOnGrab": true, "supportsOnHealthIssue": true, "supportsOnHealthRestored": true, "supportsOnApplicationUpdate": false,
		"onGrab": false, "onHealthIssue": false, "onHealthRestored": false, "onApplicationUpdate": false,
		"fields": []any{map[string]any{"name": "webHookUrl", "value": "", "type": "textbox", "privacy": "apiKey"}, map[string]any{"name": "username", "value": "", "type": "textbox"}},
	}
	for _, event := range on {
		n[map[string]string{"grab": "onGrab", "health_issue": "onHealthIssue", "health_restored": "onHealthRestored"}[event]] = true
	}

	return n
}

// A notification is added from its kind's schema with its settings by name
// and what it is sent for; an event its kind cannot be sent for, and a kind
// there is not, are refused before anything is sent.
func TestNotificationAdd(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/notification/schema", []any{discord(0, "")})
	f.answer("POST /api/v1/notification", discord(6, "Alerts", "health_issue", "health_restored"))
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "notification_add", map[string]any{"kind": "discord", "name": "Alerts", "on": []string{"health_issue", "health_restored"}, "settings": map[string]any{"webHookUrl": "https://discord.invalid/hook"}, "health_warnings": true})
	on := listOf(out["on"])
	if out["name"] != "Alerts" || out["kind"] != "Discord" || len(on) != 2 {
		t.Errorf("notification_add = %v", out)
	}
	var sent prowlarr.NotificationResource
	if err := json.Unmarshal([]byte(f.sent("POST /api/v1/notification")[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Name != "Alerts" || !boolv(sent.OnHealthIssue) || !boolv(sent.OnHealthRestored) || boolv(sent.OnGrab) || !boolv(sent.IncludeHealthWarnings) || fieldString(sent.Fields, "webHookUrl") != "https://discord.invalid/hook" {
		t.Errorf("sent %+v", sent)
	}
	// the credential it was given is not in the answer
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "discord.invalid") {
		t.Errorf("the webhook is in the answer: %s", raw)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		says string
	}{
		"a kind there is not":              {map[string]any{"kind": "Pigeon", "on": []string{"grab"}}, "no notification kind"},
		"nothing to send it for":           {map[string]any{"kind": "Discord", "on": []string{}}, "say what to send it for"},
		"an event the kind cannot send":    {map[string]any{"kind": "Discord", "on": []string{"application_update"}}, "cannot be sent for application_update"},
		"an event there is not":            {map[string]any{"kind": "Discord", "on": []string{"downloads"}}, "no event"},
		"a setting the kind does not have": {map[string]any{"kind": "Discord", "on": []string{"grab"}, "settings": map[string]any{"url": "x"}}, "webHookUrl"},
	} {
		if msg := callError(t, cs, "notification_add", tc.args); !strings.Contains(msg, tc.says) {
			t.Errorf("%s = %q, want it to say %q", name, msg, tc.says)
		}
	}
	if n := len(f.sent("POST /api/v1/notification")); n != 1 {
		t.Errorf("a refused notification was sent anyway (%d creates)", n)
	}
}

// A forced add creates the notification sent for nothing, which Prowlarr
// does not test, then says what it is sent for with a forced update.
func TestNotificationAddForced(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/notification/schema", []any{discord(0, "")})
	f.answer("POST /api/v1/notification", discord(6, "Discord"))
	f.answer("PUT /api/v1/notification/6", discord(6, "Discord", "grab"))
	cs := session(t, f, Options{})

	mustCall(t, cs, "notification_add", map[string]any{"kind": "Discord", "on": []string{"grab"}, "force": true})
	var created, updated prowlarr.NotificationResource
	if err := json.Unmarshal([]byte(f.sent("POST /api/v1/notification")[0]), &created); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(f.sent("PUT /api/v1/notification/6")[0]), &updated); err != nil {
		t.Fatal(err)
	}
	if boolv(created.OnGrab) || !boolv(updated.OnGrab) || updated.Id != 6 {
		t.Errorf("created on grab %v, updated on grab %v with id %d: want it created off and switched on", boolv(created.OnGrab), boolv(updated.OnGrab), updated.Id)
	}
	if q := f.requested("PUT /api/v1/notification/6"); !strings.Contains(q[0], "forceSave=true") {
		t.Errorf("the update was not forced: %v", q)
	}
}

// An edit sends the whole notification back with what was asked changed:
// events added and removed one by one, or replaced outright.
func TestNotificationEdit(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/notification", []any{discord(6, "Alerts", "grab", "health_issue")})
	f.answer("GET /api/v1/notification/6", discord(6, "Alerts", "grab", "health_issue"))
	f.answer("PUT /api/v1/notification/6", discord(6, "Alerts", "health_issue", "health_restored"))
	cs := session(t, f, Options{})

	sentAfter := func(args map[string]any) prowlarr.NotificationResource {
		t.Helper()

		mustCall(t, cs, "notification_edit", args)
		all := f.sent("PUT /api/v1/notification/6")
		var n prowlarr.NotificationResource
		if err := json.Unmarshal([]byte(all[len(all)-1]), &n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	n := sentAfter(map[string]any{"notification": "alerts", "add_on": []string{"health_restored"}, "remove_on": []string{"grab"}, "settings": map[string]any{"username": "Prowlarr"}})
	if boolv(n.OnGrab) || !boolv(n.OnHealthIssue) || !boolv(n.OnHealthRestored) || fieldString(n.Fields, "username") != "Prowlarr" || n.Name != "Alerts" {
		t.Errorf("after adding and removing events, sent %+v", n)
	}
	if n = sentAfter(map[string]any{"notification": "6", "on": []string{"grab"}}); !boolv(n.OnGrab) || boolv(n.OnHealthIssue) {
		t.Errorf("after replacing the events, sent on grab %v, on health issue %v", boolv(n.OnGrab), boolv(n.OnHealthIssue))
	}

	if msg := callError(t, cs, "notification_edit", map[string]any{"notification": "Alerts"}); !strings.Contains(msg, "nothing to change") {
		t.Errorf("an edit of nothing = %q", msg)
	}
	if msg := callError(t, cs, "notification_edit", map[string]any{"notification": "Alerts", "add_on": []string{"application_update"}}); !strings.Contains(msg, "cannot be sent for") {
		t.Errorf("an event the kind cannot send = %q", msg)
	}
}
