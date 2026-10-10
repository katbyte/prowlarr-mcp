package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// hostSettings is the host section as Prowlarr answers it, credentials and
// all.
func hostSettings() map[string]any {
	return map[string]any{
		"id": 1, "port": 9696, "bindAddress": "*", "urlBase": "", "apiKey": "0123456789abcdef", "password": "hashed", "passwordConfirmation": "",
		"logLevel": "info", "backupInterval": 7, "backupRetention": 28, "historyCleanupDays": 30, "proxyPassword": "", "instanceName": "Prowlarr", "analyticsEnabled": false,
	}
}

// The settings are shown by section and name with no credential's value in
// them, only that it is set.
func TestServerSettingsShowNoCredentials(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/config/host", hostSettings())
	f.answer("GET /api/v1/config/ui", map[string]any{"id": 1, "theme": "auto", "firstDayOfWeek": 0})
	f.answer("GET /api/v1/config/development", map[string]any{"id": 1, "logRotate": 50, "consoleLogLevel": ""})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "server_settings", nil)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "0123456789abcdef") || strings.Contains(string(raw), "hashed") {
		t.Fatalf("a credential's value is in the answer: %s", raw)
	}
	sections := listOf(out["sections"])
	if len(sections) != 3 {
		t.Fatalf("%d sections, want host, ui and development: %s", len(sections), raw)
	}
	host := objectOf(sections[0])
	settings := objectOf(host["settings"])
	creds := listOf(host["credentials"])
	if host["section"] != "host" || settings["logLevel"] != "info" || settings["backupInterval"] != float64(7) || settings["id"] != nil {
		t.Errorf("the host section = %v", host)
	}
	// the ones set are named; the empty ones are not
	if len(creds) != 2 || creds[0] != "apiKey" || creds[1] != "password" {
		t.Errorf("credentials set = %v, want apiKey and password", creds)
	}

	one := mustCall(t, cs, "server_settings", map[string]any{"section": "UI"})
	if got := listOf(one["sections"]); len(got) != 1 {
		t.Errorf("section=UI answered %d sections", len(got))
	}
	if msg := callError(t, cs, "server_settings", map[string]any{"section": "indexers"}); !strings.Contains(msg, "host, ui, development") {
		t.Errorf("an unknown section = %q", msg)
	}
}

// A setting is changed by name and the whole section is sent back with only
// it changed; one the section does not have, a value of the wrong kind and a
// value it already has are each refused before anything is sent.
func TestServerSettingsEditSendsTheWholeSection(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/config/host", hostSettings())
	f.answer("PUT /api/v1/config/host/1", hostSettings())
	cs := session(t, f, Options{})

	// the canned server answers the section as it was whatever it is sent,
	// which is a server that took the change and did not keep it: said as
	// such, read back, rather than reported as changed
	out := mustCall(t, cs, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"loglevel": "debug", "backupRetention": 14}})
	changed := listOf(out["changed"])
	dropped := listOf(out["not_kept"])
	if len(dropped) != 2 || len(changed) != 0 || out["would_change"] != nil {
		t.Fatalf("an edit the server did not keep = %v", out)
	}
	sent := f.sent("PUT /api/v1/config/host/1")
	if len(sent) != 1 {
		t.Fatalf("%d updates sent", len(sent))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(sent[0]), &got); err != nil {
		t.Fatal(err)
	}
	// the two changed, and what was not named sent back as it was read: the
	// key and the password included, or Prowlarr would save them empty
	if got["logLevel"] != "debug" || got["backupRetention"] != float64(14) || got["backupInterval"] != float64(7) || got["apiKey"] != "0123456789abcdef" || got["password"] != "hashed" || got["port"] != float64(9696) {
		t.Errorf("sent %s", sent[0])
	}

	for name, tc := range map[string]struct {
		settings map[string]any
		says     string
	}{
		"a setting the section does not have": {map[string]any{"theme": "dark"}, "has no setting"},
		"a value of the wrong kind":           {map[string]any{"backupInterval": "weekly"}, "wrong kind"},
		"a value it already has":              {map[string]any{"logLevel": "info"}, "already has that value"},
		"nothing at all":                      {map[string]any{}, "nothing to change"},
	} {
		if msg := callError(t, cs, "server_settings_edit", map[string]any{"section": "host", "settings": tc.settings}); !strings.Contains(msg, tc.says) {
			t.Errorf("%s = %q, want it to say %q", name, msg, tc.says)
		}
	}
	if n := len(f.sent("PUT /api/v1/config/host/1")); n != 1 {
		t.Errorf("a refused edit was sent anyway (%d updates)", n)
	}
}

// A setting that decides how Prowlarr is reached or who may sign in is not
// changed without confirm, and a credential's value is in no answer.
func TestServerSettingsEditAsksBeforeCuttingItselfOff(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/config/host", hostSettings())
	f.answer("PUT /api/v1/config/host/1", hostSettings())
	cs := session(t, f, Options{})

	args := map[string]any{"section": "host", "settings": map[string]any{"port": 9797, "apiKey": "ffffffffffffffff", "logLevel": "debug"}}
	out := mustCall(t, cs, "server_settings_edit", args)
	would := listOf(out["would_change"])
	if len(would) != 3 || len(f.sent("PUT /api/v1/config/host/1")) != 0 {
		t.Fatalf("without confirm = %v, with %d updates sent", out, len(f.sent("PUT /api/v1/config/host/1")))
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "ffffffffffffffff") || strings.Contains(string(raw), "0123456789abcdef") {
		t.Errorf("a key is in the answer: %s", raw)
	}

	// with confirm it is sent. The key cannot be compared with what is read
	// back, so it counts as changed; the rest the canned server did not keep
	args["confirm"] = true
	out = mustCall(t, cs, "server_settings_edit", args)
	changed := listOf(out["changed"])
	dropped := listOf(out["not_kept"])
	if len(changed) != 1 || len(dropped) != 2 || !strings.Contains(str(t, out["note"], "note"), "restarted") {
		t.Errorf("with confirm = %v", out)
	}
	sent := f.sent("PUT /api/v1/config/host/1")
	if len(sent) != 1 || !strings.Contains(sent[0], `"port":9797`) || !strings.Contains(sent[0], `"apiKey":"ffffffffffffffff"`) {
		t.Errorf("sent %v", sent)
	}
}
