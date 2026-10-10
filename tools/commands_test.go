package tools

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// A command still queued is taken off the queue; one that has started or
// finished is refused, saying which, and nothing is sent.
func TestCommandCancelOnlyWhatIsQueued(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/command/3", map[string]any{"id": 3, "name": "Backup", "commandName": "Backup", "status": "queued"})
	f.answer("GET /api/v1/command/4", map[string]any{"id": 4, "name": "CheckHealth", "commandName": "Check Health", "status": "started"})
	f.answer("GET /api/v1/command/5", map[string]any{"id": 5, "name": "CheckHealth", "commandName": "Check Health", "status": "completed"})
	for _, id := range []string{"3", "4", "5"} {
		f.answerOK("DELETE /api/v1/command/"+id, map[string]any{})
	}
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "command_cancel", map[string]any{"id": 3})
	cancelled := objectOf(out["cancelled"])
	if cancelled["command"] != "Backup" || cancelled["status"] != "cancelled" || len(f.requested("DELETE /api/v1/command/3")) != 1 {
		t.Errorf("cancelling a queued command = %v", out)
	}
	if msg := callError(t, cs, "command_cancel", map[string]any{"id": 4}); !strings.Contains(msg, "has started") {
		t.Errorf("cancelling a started command = %q", msg)
	}
	if msg := callError(t, cs, "command_cancel", map[string]any{"id": 5}); !strings.Contains(msg, "already completed") {
		t.Errorf("cancelling a finished command = %q", msg)
	}
	if n := len(f.requested("DELETE /api/v1/command/4")) + len(f.requested("DELETE /api/v1/command/5")); n != 0 {
		t.Errorf("%d cancels were sent for commands that cannot be cancelled", n)
	}
}

// The commands are listed newest first, and narrowed to the ones running.
func TestCommandList(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/command", []any{
		map[string]any{"id": 1, "commandName": "Check Health", "status": "completed", "trigger": "scheduled", "queued": "2026-10-01T00:00:00Z"},
		map[string]any{"id": 2, "commandName": "Backup", "status": "started", "trigger": "manual", "queued": "2026-10-01T00:05:00Z"},
		map[string]any{"id": 3, "commandName": "Application Indexer Sync", "status": "queued", "trigger": "manual", "queued": "2026-10-01T00:06:00Z"},
	})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "command_list", nil)
	rows := listOf(out["commands"])
	if len(rows) != 3 || objectOf(rows[0])["command"] != "Application Indexer Sync" || objectOf(rows[0])["trigger"] != "manual" {
		t.Errorf("command_list = %v", out)
	}
	if out = mustCall(t, cs, "command_list", map[string]any{"status": "running"}); num(t, out["total"], "total") != 2 {
		t.Errorf("status=running = %v, want the queued and the started one", out)
	}
	if out = mustCall(t, cs, "command_list", map[string]any{"status": "completed"}); num(t, out["total"], "total") != 1 {
		t.Errorf("status=completed = %v", out)
	}
	if msg := callError(t, cs, "command_list", map[string]any{"status": "done"}); !strings.Contains(msg, "status must be") {
		t.Errorf("an unknown status = %q", msg)
	}
}

// A restart is told to Prowlarr, and is over when a Prowlarr that started
// later answers: one that has not answered as a new start by the end of the
// wait is said to be still on its way, not restarted.
//
//nolint:paralleltest // shortens the shared poll interval
func TestServerRestartWaitsForANewStart(t *testing.T) {
	was := commandPoll
	commandPoll = 5 * time.Millisecond
	t.Cleanup(func() { commandPoll = was })

	f := newFakeServer(t)
	f.answerOK("POST /api/v1/system/restart", map[string]any{"restarting": true})
	cs := session(t, f, Options{})

	// it comes back after a moment, as a later start
	var once sync.Once
	go func() {
		for len(f.requested("POST /api/v1/system/restart")) == 0 {
			time.Sleep(time.Millisecond)
		}
		once.Do(func() {
			f.answer("GET /api/v1/system/status", map[string]any{"appName": "Prowlarr", "version": "2.6.5.5623", "startTime": "2026-10-10T00:00:10Z"})
		})
	}()
	f.answer("GET /api/v1/system/status", map[string]any{"appName": "Prowlarr", "version": "2.6.5.5623", "startTime": "2026-10-10T00:00:00Z"})

	out := mustCall(t, cs, "server_restart", map[string]any{"wait": 10})
	if !flag(t, out["restarted"], "restarted") || out["start_time"] != "2026-10-10T00:00:10Z" {
		t.Errorf("a restart that came back = %v", out)
	}

	// and one that does not come back within the wait says so
	f.answer("GET /api/v1/system/status", map[string]any{"appName": "Prowlarr", "version": "2.6.5.5623", "startTime": "2026-10-10T00:00:10Z"})
	out = mustCall(t, cs, "server_restart", map[string]any{"wait": 1})
	if flag(t, out["restarted"], "restarted") || !strings.Contains(str(t, out["note"], "note"), "had not answered as a new start") {
		t.Errorf("a restart that did not come back = %v", out)
	}
}
