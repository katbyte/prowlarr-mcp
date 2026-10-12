//go:build acceptance

package tools_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
)

// hook is where a webhook notification is sent: a server on the host that
// keeps what arrives, so a test sees that Prowlarr really sent word and what
// it said.
type hook struct {
	url string

	mu     sync.Mutex
	events []string
	bodies []map[string]any
}

// newHook listens where the container can reach it, until the test ends.
func newHook(t *testing.T) *hook {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("the listener's address is a %T, not a TCP address", ln.Addr())
	}

	h := &hook{url: "http://" + net.JoinHostPort(testenv.Host(), strconv.Itoa(addr.Port)) + "/hook"}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:gosec // a test server on the host for the length of one test
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		h.mu.Lock()
		h.events = append(h.events, acctest.Str(body["eventType"]))
		h.bodies = append(h.bodies, body)
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	return h
}

// got is the kinds of event that have arrived, in order.
func (h *hook) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.events)
}

// A notification is added, changed, tested and deleted, and each time
// Prowlarr sends word it arrives: the test it sends before saving, the one
// notification_test asks for, and a grab made from Prowlarr's own search once
// it is told to send those.
func TestNotifications(t *testing.T) {
	// none are set up; the listing and the test of every one still answer
	if got := acctest.Rows(t, suite.Call(t, "notification_list", nil)["notifications"], "notifications"); len(got) != 0 {
		t.Fatalf("notifications = %v", got)
	}
	if got := acctest.Rows(t, suite.Call(t, "notification_test", nil)["results"], "results"); len(got) != 0 {
		t.Errorf("testing none = %v", got)
	}
	if msg := suite.CallErr(t, "notification_test", map[string]any{"notification": "Discord"}); !strings.Contains(msg, "has none") {
		t.Errorf("testing one that is not there = %q", msg)
	}

	h := newHook(t)
	const name, renamed = "Suite Hook", "Suite Hook Two"
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": renamed, "confirm": true}, "no notification")
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": name, "confirm": true}, "no notification")

	// Prowlarr sends a test through it before it saves it
	made := suite.Call(t, "notification_add", map[string]any{"kind": "webhook", "name": name, "on": []string{"health_issue"}, "settings": map[string]any{"url": h.url}})
	if acctest.Str(made["kind"]) != "Webhook" || !slices.Equal(acctest.Strs(t, made["on"], "on"), []string{"health_issue"}) || acctest.IsBool(made["manual_grabs"], true) {
		t.Errorf("added = %v", made)
	}
	if got := h.got(); !slices.Equal(got, []string{"Test"}) {
		t.Errorf("adding it sent %v, want the one test", got)
	}
	listed := acctest.Rows(t, suite.Call(t, "notification_list", nil)["notifications"], "notifications")
	if len(listed) != 1 || acctest.Str(listed[0]["name"]) != name || acctest.Object(t, listed[0]["settings"], "settings")["url"] != h.url {
		t.Errorf("listed = %v", listed)
	}

	// tested by name, and with every other one
	for _, args := range []map[string]any{{"notification": name}, nil} {
		res := acctest.Rows(t, suite.Call(t, "notification_test", args)["results"], "results")
		if len(res) != 1 || !acctest.IsBool(res[0]["ok"], true) {
			t.Errorf("notification_test %v = %v", args, res)
		}
	}
	if got := h.got(); len(got) != 3 || got[2] != "Test" {
		t.Errorf("after two tests the hook has %v", got)
	}

	// renamed, and sent for grabs as well, Prowlarr's own included
	edited := suite.Call(t, "notification_edit", map[string]any{"notification": name, "name": renamed, "add_on": []string{"grab"}, "manual_grabs": true})
	if acctest.Str(edited["name"]) != renamed || !slices.Equal(acctest.Strs(t, edited["on"], "on"), []string{"grab", "health_issue"}) || !acctest.IsBool(edited["manual_grabs"], true) {
		t.Errorf("edited = %v", edited)
	}
	before := len(h.got())
	grabTorrent(t, "arrival")
	if !acctest.EventuallyWithin(patience, func() bool { return slices.Contains(h.got()[before:], "Grab") }) {
		t.Errorf("a grab sent no word: the hook has %v", h.got())
	}
	// and no longer, once it is told not to
	edited = suite.Call(t, "notification_edit", map[string]any{"notification": renamed, "on": []string{"health_issue"}})
	if !slices.Equal(acctest.Strs(t, edited["on"], "on"), []string{"health_issue"}) {
		t.Errorf("after replacing what it is sent for = %v", edited)
	}

	// what cannot be sent, or does not exist, is refused
	for says, args := range map[string]map[string]any{
		"no notification kind": {"kind": "Pigeon", "on": []string{"grab"}},
		"no event":             {"kind": "Webhook", "on": []string{"downloads"}, "settings": map[string]any{"url": h.url}},
		"url":                  {"kind": "Webhook", "on": []string{"grab"}, "settings": map[string]any{"address": h.url}},
	} {
		if msg := suite.CallErr(t, "notification_add", args); !strings.Contains(msg, says) {
			t.Errorf("notification_add %v = %q, want it to say %q", args, msg, says)
		}
	}
	// one Prowlarr cannot reach is refused, and saved all the same when forced
	nowhere := map[string]any{"kind": "Webhook", "name": "Nowhere", "on": []string{"health_issue"}, "settings": map[string]any{"url": "http://" + testenv.Host() + ":1/hook"}}
	if msg := suite.CallErr(t, "notification_add", nowhere); !strings.Contains(msg, "force=true") {
		t.Errorf("a hook that cannot be reached = %q", msg)
	}
	nowhere["force"] = true
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": "Nowhere", "confirm": true}, "no notification")
	if forced := suite.Call(t, "notification_add", nowhere); !slices.Equal(acctest.Strs(t, forced["on"], "on"), []string{"health_issue"}) {
		t.Errorf("forced = %v", forced)
	}
	if res := acctest.Rows(t, suite.Call(t, "notification_test", map[string]any{"notification": "Nowhere"})["results"], "results"); len(res) != 1 || acctest.IsBool(res[0]["ok"], true) {
		t.Errorf("testing one that cannot be reached = %v", res)
	}
	// Prowlarr fails the test of every one outright when one cannot be sent,
	// without saying which: the tool still answers for each, by name
	every := acctest.Rows(t, suite.Call(t, "notification_test", nil)["results"], "results")
	if len(every) != 2 || acctest.Str(every[0]["name"]) != "Nowhere" || acctest.IsBool(every[0]["ok"], true) || acctest.Str(every[1]["name"]) != renamed || !acctest.IsBool(every[1]["ok"], true) {
		t.Errorf("testing every one, one of which cannot be reached = %v", every)
	}

	// deleted only once it is confirmed
	would := suite.Call(t, "notification_delete", map[string]any{"notification": "Nowhere"})
	if acctest.Str(would["would_delete"]) != "Nowhere" || len(acctest.Rows(t, suite.Call(t, "notification_list", nil)["notifications"], "notifications")) != 2 {
		t.Errorf("without confirm = %v", would)
	}
	for _, n := range []string{"Nowhere", renamed} {
		if gone := suite.Call(t, "notification_delete", map[string]any{"notification": n, "confirm": true}); acctest.Str(gone["deleted"]) != n {
			t.Errorf("deleting %s = %v", n, gone)
		}
	}
	if left := acctest.Rows(t, suite.Call(t, "notification_list", nil)["notifications"], "notifications"); len(left) != 0 {
		t.Errorf("after deleting both = %v", left)
	}
}

// A webhook's address is what posts to it, and Prowlarr marks neither
// Discord's nor a plain webhook's as a credential. Discord's is named as set
// and never shown; a plain webhook's is shown, which is how it is told from
// another, without the value of what follows its "?". Both are given to the
// suite to watch for, so no later answer may show them either.
func TestANotificationsCredentialIsNamedNotShown(t *testing.T) {
	h := newHook(t)
	const chat, hooked = "Suite Chat", "Suite Token Hook"
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": chat, "confirm": true}, "no notification")
	suite.DeleteLaterIfThere(t, "notification_delete", map[string]any{"notification": hooked, "confirm": true}, "no notification")

	// forced, since nothing answers at the address
	suite.Call(t, "notification_add", map[string]any{"kind": "Discord", "name": chat, "on": []string{"health_issue"}, "force": true, "settings": map[string]any{"webHookUrl": "https://discord.invalid/api/webhooks/1/" + suite.Secret("a-discord-webhooks-token"), "author": "the suite"}})
	suite.Call(t, "notification_add", map[string]any{"kind": "Webhook", "name": hooked, "on": []string{"health_issue"}, "settings": map[string]any{"url": h.url + "?token=" + suite.Secret("a-plain-webhooks-token")}})
	if got := h.got(); !slices.Equal(got, []string{"Test"}) {
		t.Errorf("adding the hook sent %v, want the one test, so the token it was given is the one Prowlarr uses", got)
	}

	rows := map[string]map[string]any{}
	for _, row := range acctest.Rows(t, suite.Call(t, "notification_list", nil)["notifications"], "notifications") {
		rows[acctest.Str(row["name"])] = row
	}
	if len(rows) != 2 {
		t.Fatalf("notifications = %v", rows)
	}

	shown := acctest.Object(t, rows[chat]["settings"], "settings")
	if set := acctest.Strs(t, rows[chat]["secrets_set"], "secrets_set"); !slices.Equal(set, []string{"webHookUrl"}) || shown["webHookUrl"] != nil || shown["author"] != "the suite" {
		t.Errorf("Discord names %v as set and shows %v", set, shown)
	}
	if got := acctest.Object(t, rows[hooked]["settings"], "settings")["url"]; got != h.url+"?token=REDACTED" {
		t.Errorf("the hook's address is shown as %v", got)
	}
	// it is still sent to the address it was given, token and all
	if res := acctest.Rows(t, suite.Call(t, "notification_test", map[string]any{"notification": hooked})["results"], "results"); len(res) != 1 || !acctest.IsBool(res[0]["ok"], true) {
		t.Errorf("testing the hook = %v", res)
	}
	// and an edit of something else keeps the address Prowlarr holds; forced
	// for Discord's, which Prowlarr would test first and nothing answers
	suite.Call(t, "notification_edit", map[string]any{"notification": chat, "force": true, "settings": map[string]any{"author": "the suite, again"}})
	suite.Call(t, "notification_edit", map[string]any{"notification": hooked, "health_warnings": true})
	before := len(h.got())
	if res := acctest.Rows(t, suite.Call(t, "notification_test", map[string]any{"notification": hooked})["results"], "results"); len(res) != 1 || !acctest.IsBool(res[0]["ok"], true) || len(h.got()) <= before {
		t.Errorf("testing the hook after an edit = %v, and the hook has %v", res, h.got())
	}

	for _, n := range []string{chat, hooked} {
		if gone := suite.Call(t, "notification_delete", map[string]any{"notification": n, "confirm": true}); acctest.Str(gone["deleted"]) != n {
			t.Errorf("deleting %s = %v", n, gone)
		}
	}
}
