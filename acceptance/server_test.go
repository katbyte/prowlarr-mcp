//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

func TestServerInfo(t *testing.T) {
	out := call(t, "server_info", nil)
	if !strings.HasPrefix(str(out["version"]), "2.") || str(out["prowlarr_mcp_version"]) == "" || out["docker"] != true {
		t.Errorf("server_info = %v", out)
	}
	if n := num(t, out["indexers"], "indexers"); n != len(genericIndexers)+3 {
		t.Errorf("%d indexers, want %d", n, len(genericIndexers)+3)
	}
	if num(t, out["applications"], "applications") != 3 || num(t, out["proxies"], "proxies") != 2 {
		t.Errorf("applications %v, proxies %v", out["applications"], out["proxies"])
	}
	byProtocol := object(t, out["indexers_by_protocol"], "indexers_by_protocol")
	if byProtocol["usenet"] != float64(2) {
		t.Errorf("by protocol = %v", byProtocol)
	}
	if len(rows(t, out["health"], "health")) == 0 {
		t.Error("no health checks, but Prowlarr warns about its allowed hosts at least")
	}
}

func TestServerHealth(t *testing.T) {
	checks := rows(t, call(t, "server_health", map[string]any{"check": true})["checks"], "checks")
	var sources []string
	for _, c := range checks {
		sources = append(sources, str(c["source"]))
		if str(c["source"]) == "AllowedHostsCheck" && str(c["fix"]) == "" {
			t.Errorf("no fix named for %v", c)
		}
	}
	if len(checks) == 0 {
		t.Fatal("no health checks")
	}
	// errors first
	seenWarning := false
	for _, c := range checks {
		switch str(c["type"]) {
		case "warning":
			seenWarning = true
		case "error":
			if seenWarning {
				t.Errorf("an error after a warning: %v", sources)
			}
		}
	}
}

func TestTasks(t *testing.T) {
	var names []string
	for _, task := range rows(t, call(t, "task_list", nil)["tasks"], "tasks") {
		names = append(names, str(task["name"]))
		if num(t, task["interval_minutes"], "interval_minutes") <= 0 {
			t.Errorf("task = %v", task)
		}
	}
	for _, want := range []string{"ApplicationIndexerSync", "CheckHealth", "Backup"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("%s missing from %v", want, names)
		}
	}

	out := call(t, "task_run", map[string]any{"name": "backup"})
	if str(out["status"]) != "completed" || str(out["command"]) != "Backup" {
		t.Errorf("task_run backup = %v", out)
	}
	if msg := callErr(t, "task_run", map[string]any{"name": "Defragment"}); !strings.Contains(msg, "CheckHealth") {
		t.Errorf("an unknown task = %q", msg)
	}
	// queued without waiting
	if out := call(t, "task_run", map[string]any{"name": "CheckHealth", "wait": -1}); str(out["status"]) == "" {
		t.Errorf("queued = %v", out)
	}
}

func TestBackups(t *testing.T) {
	call(t, "task_run", map[string]any{"name": "Backup"})
	backups := rows(t, call(t, "backup_list", nil)["backups"], "backups")
	if len(backups) == 0 || str(backups[0]["type"]) != "manual" || num(t, backups[0]["size"], "size") == 0 {
		t.Errorf("backups = %v", backups)
	}
}

func TestServerLogs(t *testing.T) {
	files := rows(t, call(t, "server_logs", nil)["files"], "files")
	if len(files) == 0 || str(files[0]["kind"]) != "app" || !strings.HasPrefix(str(files[0]["name"]), "prowlarr") {
		t.Fatalf("log files = %v", files)
	}

	// the recorded warnings and errors: offline, Prowlarr cannot reach its
	// definitions service, and says so
	entries := rows(t, call(t, "server_log", map[string]any{"level": "warn", "limit": 20})["entries"], "entries")
	if len(entries) == 0 {
		t.Error("no warnings recorded")
	}
	for _, e := range entries {
		if l := strings.ToLower(str(e["level"])); l != "warn" && l != "error" && l != "fatal" {
			t.Errorf("a %s entry among warnings", l)
		}
	}
	// one file, narrowed to an indexer
	lines := rows(t, call(t, "server_log", map[string]any{"file": str(files[0]["name"]), "contains": idxTorrent, "limit": 10})["entries"], "entries")
	if len(lines) == 0 {
		t.Errorf("no lines mentioning %s", idxTorrent)
	}
	for _, l := range lines {
		if !strings.Contains(str(l["message"]), idxTorrent) {
			t.Errorf("line %q does not mention %s", l["message"], idxTorrent)
		}
	}
	if msg := callErr(t, "server_log", map[string]any{"file": "nope.txt"}); !strings.Contains(msg, "prowlarr.txt") {
		t.Errorf("an unknown file = %q", msg)
	}
	if msg := callErr(t, "server_log", map[string]any{"level": "loud"}); !strings.Contains(msg, "warn") {
		t.Errorf("an unknown level = %q", msg)
	}
}

func TestServerUpdates(t *testing.T) {
	out := call(t, "server_updates", nil)
	// offline, the list cannot be had, and it says why rather than failing
	if !strings.HasPrefix(str(out["current"]), "2.") || !strings.Contains(str(out["unavailable"]), "update service") {
		t.Errorf("server_updates = %v", out)
	}
}
