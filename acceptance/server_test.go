//go:build integration

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	acc "github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/go-kt/test/services/arrserver"
	"github.com/katbyte/prowlarr-mcp/lib/testenv"
)

func TestServerInfo(t *testing.T) {
	out := suite.Call(t, "server_info", nil)
	if !strings.HasPrefix(acc.Str(out["version"]), "2.") || acc.Str(out["prowlarr_mcp_version"]) == "" || out["docker"] != true {
		t.Errorf("server_info = %v", out)
	}
	if n := acc.Num(t, out["indexers"], "indexers"); n != len(genericIndexers)+3 {
		t.Errorf("%d indexers, want %d", n, len(genericIndexers)+3)
	}
	if acc.Num(t, out["applications"], "applications") != 3 || acc.Num(t, out["proxies"], "proxies") != 2 {
		t.Errorf("applications %v, proxies %v", out["applications"], out["proxies"])
	}
	byProtocol := acc.Object(t, out["indexers_by_protocol"], "indexers_by_protocol")
	if byProtocol["usenet"] != float64(2) {
		t.Errorf("by protocol = %v", byProtocol)
	}
	if len(acc.Rows(t, out["health"], "health")) == 0 {
		t.Error("no health checks, but Prowlarr warns about its allowed hosts at least")
	}
}

func TestServerHealth(t *testing.T) {
	checks := acc.Rows(t, suite.Call(t, "server_health", map[string]any{"check": true})["checks"], "checks")
	var sources []string
	for _, c := range checks {
		sources = append(sources, acc.Str(c["source"]))
		if acc.Str(c["source"]) == "AllowedHostsCheck" && acc.Str(c["fix"]) == "" {
			t.Errorf("no fix named for %v", c)
		}
	}
	if len(checks) == 0 {
		t.Fatal("no health checks")
	}
	// errors first
	seenWarning := false
	for _, c := range checks {
		switch acc.Str(c["type"]) {
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
	for _, task := range acc.Rows(t, suite.Call(t, "task_list", nil)["tasks"], "tasks") {
		names = append(names, acc.Str(task["name"]))
		if acc.Num(t, task["interval_minutes"], "interval_minutes") <= 0 {
			t.Errorf("task = %v", task)
		}
	}
	for _, want := range []string{"ApplicationIndexerSync", "CheckHealth", "Backup"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("%s missing from %v", want, names)
		}
	}

	out := suite.Call(t, "task_run", map[string]any{"name": "backup"})
	if acc.Str(out["status"]) != "completed" || acc.Str(out["command"]) != "Backup" {
		t.Errorf("task_run backup = %v", out)
	}
	if msg := suite.CallErr(t, "task_run", map[string]any{"name": "Defragment"}); !strings.Contains(msg, "CheckHealth") {
		t.Errorf("an unknown task = %q", msg)
	}
	// queued without waiting
	if out := suite.Call(t, "task_run", map[string]any{"name": "CheckHealth", "wait": -1}); acc.Str(out["status"]) == "" {
		t.Errorf("queued = %v", out)
	}
}

// A backup is written, listed newest first, and deleted only once that is
// confirmed.
func TestBackups(t *testing.T) {
	made := suite.Call(t, "backup_create", nil)
	backup := acc.Object(t, made["backup"], "backup")
	name := acc.Str(backup["name"])
	if name == "" || acc.Str(backup["type"]) != "manual" || acc.Num(t, backup["size"], "size") == 0 || acc.Str(acc.Object(t, made["command"], "command")["status"]) != "completed" {
		t.Fatalf("backup_create = %v", made)
	}
	suite.DeleteLaterIfThere(t, "backup_delete", map[string]any{"backup": name, "confirm": true}, "no backup")

	listed := acc.Rows(t, suite.Call(t, "backup_list", nil)["backups"], "backups")
	if len(listed) == 0 || acc.Str(listed[0]["name"]) != name {
		t.Errorf("the newest backup listed is %v, want %s", listed, name)
	}
	// the scheduled task writes one the same way
	suite.Call(t, "task_run", map[string]any{"name": "Backup"})

	would := suite.Call(t, "backup_delete", map[string]any{"backup": name})
	if acc.Str(would["would_delete"]) != name || len(acc.Strs(t, would["loses"], "loses")) == 0 {
		t.Errorf("without confirm = %v", would)
	}
	stillThere := func() bool {
		for _, b := range acc.Rows(t, suite.Call(t, "backup_list", nil)["backups"], "backups") {
			if acc.Str(b["name"]) == name {
				return true
			}
		}

		return false
	}
	if !stillThere() {
		t.Fatal("the backup went without confirm")
	}
	if gone := suite.Call(t, "backup_delete", map[string]any{"backup": name, "confirm": true}); acc.Str(gone["deleted"]) != name || stillThere() {
		t.Errorf("with confirm = %v, still listed %v", gone, stillThere())
	}
	if msg := suite.CallErr(t, "backup_restore", map[string]any{"backup": name, "confirm": true}); !strings.Contains(msg, "no backup") {
		t.Errorf("restoring one that is gone = %q", msg)
	}
}

// What Prowlarr does in the background is listed, and one of several things
// queued at once is taken off the queue before it starts.
func TestCommands(t *testing.T) {
	all := suite.Call(t, "command_list", nil)
	rows := acc.Rows(t, all["commands"], "commands")
	if len(rows) == 0 || acc.Num(t, all["total"], "total") < len(rows) {
		t.Fatalf("command_list = %v", all)
	}
	// the health checks the suite ran as it started are among them, finished
	done := acc.Rows(t, suite.Call(t, "command_list", map[string]any{"status": "completed"})["commands"], "commands")
	i := slices.IndexFunc(done, func(c map[string]any) bool { return acc.Str(c["name"]) == "CheckHealth" })
	if i < 0 || acc.Str(done[i]["status"]) != "completed" || acc.Str(done[i]["queued"]) == "" {
		t.Fatalf("the finished commands lack the health check: %v", done)
	}
	if msg := suite.CallErr(t, "command_cancel", map[string]any{"id": acc.Num(t, done[i]["command_id"], "command_id")}); !strings.Contains(msg, "already completed") {
		t.Errorf("cancelling a finished command = %q", msg)
	}
	if msg := suite.CallErr(t, "command_list", map[string]any{"status": "done"}); !strings.Contains(msg, "status must be") {
		t.Errorf("an unknown status = %q", msg)
	}

	// every scheduled task queued at once is more than Prowlarr runs at a
	// time, so some wait their turn, and one that is waiting can be cancelled.
	// Which ones wait is Prowlarr's to say, so it is tried until one is caught
	var tasks []string
	for _, task := range acc.Rows(t, suite.Call(t, "task_list", nil)["tasks"], "tasks") {
		tasks = append(tasks, acc.Str(task["name"]))
	}
	idle := func() bool {
		return acc.Num(t, suite.Call(t, "command_list", map[string]any{"status": "running"})["total"], "total") == 0
	}
	t.Cleanup(func() { acc.EventuallyWithin(2*time.Minute, idle) })

	// a task is over in well under a second, so the queue is read straight
	// after each is queued, and the whole thing tried again when none was
	// caught waiting
	cancelled := false
	for attempt := 0; attempt < 10 && !cancelled; attempt++ {
		var queued []map[string]any
		for _, task := range tasks {
			suite.Call(t, "task_run", map[string]any{"name": task, "wait": -1})
			if queued = acc.Rows(t, suite.Call(t, "command_list", map[string]any{"status": "queued"})["commands"], "commands"); len(queued) > 0 {
				break
			}
		}
		for _, c := range queued {
			out, err := suite.Invoke("command_cancel", map[string]any{"id": acc.Num(t, c["command_id"], "command_id")})
			if err != nil {
				continue // it started between the listing and the cancel
			}
			if got := acc.Object(t, out["cancelled"], "cancelled"); acc.Str(got["status"]) != "cancelled" || acc.Str(got["command"]) != acc.Str(c["command"]) || acc.Str(c["name"]) == "" {
				t.Errorf("cancelled = %v, want %v", got, c)
			}
			cancelled = true

			break
		}
		if !acc.EventuallyWithin(2*time.Minute, idle) {
			t.Fatal("the commands queued never finished")
		}
	}
	if !cancelled {
		t.Error("no command was ever caught waiting in the queue, in ten tries of queueing every task at once")
	}
}

// Prowlarr's own settings are read by section with no credential in them, and
// changed by name; one that decides how Prowlarr is reached is not changed
// without confirm.
func TestServerSettings(t *testing.T) {
	out := suite.Call(t, "server_settings", nil)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testenv.Token()) {
		t.Fatalf("Prowlarr's API key is in the answer: %s", raw)
	}
	sections := map[string]map[string]any{}
	for _, sec := range acc.Rows(t, out["sections"], "sections") {
		sections[acc.Str(sec["section"])] = sec
	}
	host := sections["host"]
	if len(sections) != 3 || host == nil || sections["ui"] == nil || sections["development"] == nil {
		t.Fatalf("sections = %v", out)
	}
	settings := acc.Object(t, host["settings"], "settings")
	if settings["port"] != float64(9696) || acc.Str(settings["logLevel"]) != "debug" || !slices.Contains(acc.Strs(t, host["credentials"], "credentials"), "apiKey") {
		t.Errorf("the host section = %v", host)
	}

	// Prowlarr refuses to save the host section while it answers to any host
	// name and lets local addresses in unasked, which is how this one starts:
	// the first change has to say which hosts it answers to. That is the fix
	// for its allowed hosts health warning, and it takes confirm, since a
	// wrong list cuts off whoever reaches it by another name
	keep := acc.Num(t, settings["backupRetention"], "backupRetention")
	if acc.Str(settings["allowedHosts"]) == "" {
		if msg := suite.CallErr(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"backupRetention": keep + 1}}); !strings.Contains(msg, "Allowed Hosts") {
			t.Errorf("a change while no allowed hosts are set = %q, want Prowlarr's own refusal", msg)
		}
		hosts := strings.Join([]string{"127.0.0.1", "localhost", testenv.Host(), testenv.Container()}, ",")
		would := suite.Call(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"allowedHosts": hosts}})
		if len(acc.Rows(t, would["would_change"], "would_change")) != 1 || len(acc.Rows(t, would["changed"], "changed")) != 0 {
			t.Errorf("naming the allowed hosts without confirm = %v", would)
		}
		named := suite.Call(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"allowedHosts": hosts}, "confirm": true})
		if acc.Object(t, named["settings"], "settings")["allowedHosts"] != hosts {
			t.Fatalf("naming the allowed hosts = %v", named)
		}
		// and the warning it was the fix for is gone from the next run of checks
		for _, c := range acc.Rows(t, suite.Call(t, "server_health", map[string]any{"check": true})["checks"], "checks") {
			if acc.Str(c["source"]) == "AllowedHostsCheck" {
				t.Errorf("the allowed hosts warning is still there after naming them: %v", c)
			}
		}
	}

	// a setting changed by name, in whatever case, and read back
	suite.PutBack(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"backupRetention": keep}})
	edited := suite.Call(t, "server_settings_edit", map[string]any{"section": "Host", "settings": map[string]any{"BackupRetention": keep + 3}})
	changed := acc.Rows(t, edited["changed"], "changed")
	if len(changed) != 1 || acc.Str(changed[0]["setting"]) != "backupRetention" || changed[0]["to"] != float64(keep+3) || acc.Object(t, edited["settings"], "settings")["backupRetention"] != float64(keep+3) {
		t.Errorf("edited = %v", edited)
	}
	one := acc.Rows(t, suite.Call(t, "server_settings", map[string]any{"section": "host"})["sections"], "sections")
	if len(one) != 1 || acc.Object(t, one[0]["settings"], "settings")["backupRetention"] != float64(keep+3) {
		t.Errorf("read back = %v", one)
	}
	if msg := suite.CallErr(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"backupRetention": keep + 3}}); !strings.Contains(msg, "already has that value") {
		t.Errorf("the same change again = %q", msg)
	}

	// the web interface's settings, the same way
	theme := acc.Str(acc.Object(t, sections["ui"]["settings"], "settings")["theme"])
	suite.PutBack(t, "server_settings_edit", map[string]any{"section": "ui", "settings": map[string]any{"theme": theme}})
	other := "dark"
	if theme == other {
		other = "light"
	}
	if ui := suite.Call(t, "server_settings_edit", map[string]any{"section": "ui", "settings": map[string]any{"theme": other}}); acc.Object(t, ui["settings"], "settings")["theme"] != other {
		t.Errorf("the theme = %v", ui)
	}

	// how Prowlarr is reached is not changed without confirm, and is not
	// changed here with it: the suite would lose the server
	would := suite.Call(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"port": 9797}})
	if len(acc.Rows(t, would["would_change"], "would_change")) != 1 || len(acc.Rows(t, would["changed"], "changed")) != 0 || acc.Object(t, would["settings"], "settings")["port"] != float64(9696) {
		t.Errorf("a change of port without confirm = %v", would)
	}
	// one that takes confirm, and that Prowlarr takes and does not keep: a
	// name to sign in with means nothing to it while nothing asks anyone to
	// sign in. The tool reads the section back, so it says so rather than
	// reporting a change that did not happen
	named := suite.Call(t, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"username": "someone"}, "confirm": true})
	dropped := acc.Rows(t, named["not_kept"], "not_kept")
	if len(dropped) != 1 || acc.Str(dropped[0]["setting"]) != "username" || len(acc.Rows(t, named["changed"], "changed")) != 0 || acc.Object(t, named["settings"], "settings")["username"] != "" {
		t.Errorf("a username given to a Prowlarr nobody signs in to = %v", named)
	}

	for says, args := range map[string]map[string]any{
		"no settings section": {"section": "indexers", "settings": map[string]any{"a": 1}},
		"has no setting":      {"section": "ui", "settings": map[string]any{"logLevel": "debug"}},
		"wrong kind":          {"section": "host", "settings": map[string]any{"backupInterval": "weekly"}},
	} {
		if msg := suite.CallErr(t, "server_settings_edit", args); !strings.Contains(msg, says) {
			t.Errorf("server_settings_edit %v = %q, want it to say %q", args, msg, says)
		}
	}
}

func TestServerLogs(t *testing.T) {
	files := acc.Rows(t, suite.Call(t, "server_logs", nil)["files"], "files")
	if len(files) == 0 || acc.Str(files[0]["kind"]) != "app" || !strings.HasPrefix(acc.Str(files[0]["name"]), "prowlarr") {
		t.Fatalf("log files = %v", files)
	}

	// the recorded warnings and errors: offline, Prowlarr cannot reach its
	// definitions service, and says so
	entries := acc.Rows(t, suite.Call(t, "server_log", map[string]any{"level": "warn", "limit": 20})["entries"], "entries")
	if len(entries) == 0 {
		t.Error("no warnings recorded")
	}
	for _, e := range entries {
		if l := strings.ToLower(acc.Str(e["level"])); l != "warn" && l != "error" && l != "fatal" {
			t.Errorf("a %s entry among warnings", l)
		}
	}
	// one file, narrowed to an indexer. Prowlarr starts a new file when one
	// is full, so the newest may not have got to this indexer yet: the
	// newest that has is the one read
	var lines []map[string]any
	for _, file := range files {
		if lines = acc.Rows(t, suite.Call(t, "server_log", map[string]any{"file": acc.Str(file["name"]), "contains": idxTorrent, "limit": 10})["entries"], "entries"); len(lines) > 0 {
			break
		}
	}
	if len(lines) == 0 {
		t.Errorf("no lines mentioning %s in any of %d log files", idxTorrent, len(files))
	}
	for _, l := range lines {
		if !strings.Contains(acc.Str(l["message"]), idxTorrent) {
			t.Errorf("line %q does not mention %s", l["message"], idxTorrent)
		}
	}
	if msg := suite.CallErr(t, "server_log", map[string]any{"file": "nope.txt"}); !strings.Contains(msg, "prowlarr.txt") {
		t.Errorf("an unknown file = %q", msg)
	}
	if msg := suite.CallErr(t, "server_log", map[string]any{"level": "loud"}); !strings.Contains(msg, "warn") {
		t.Errorf("an unknown level = %q", msg)
	}
}

func TestServerUpdates(t *testing.T) {
	// the update server knows of no release until the suite gives it some
	out := suite.Call(t, "server_updates", nil)
	current := acc.Str(out["current"])
	if !strings.HasPrefix(current, "2.") || len(acc.Rows(t, out["updates"], "updates")) != 0 || acc.IsBool(out["update_available"], true) {
		t.Fatalf("server_updates with no releases = %v", out)
	}

	// the one installed and one before it: nothing to update to
	const older, newer = "2.0.0.1", "99.0.0.1"
	t.Cleanup(func() { home.Offer() })
	home.Offer(arrserver.Update{Version: older, Fixed: []string{"an old fault"}}, arrserver.Update{Version: current})
	out = suite.Call(t, "server_updates", nil)
	rows := acc.Rows(t, out["updates"], "updates")
	if len(rows) != 2 || acc.Str(rows[0]["version"]) != current || !acc.IsBool(rows[0]["installed"], true) || acc.Str(rows[1]["version"]) != older || acc.IsBool(out["update_available"], true) {
		t.Errorf("server_updates with nothing newer = %v", out)
	}
	if fixed := acc.Strs(t, acc.OrEmptyList(rows[1]["fixed"]), "fixed"); len(fixed) != 1 || fixed[0] != "an old fault" {
		t.Errorf("the older release's fixes = %v", fixed)
	}

	// and one after it: this install is behind, and says what it is missing
	home.Offer(arrserver.Update{Version: newer, New: []string{"a new thing"}}, arrserver.Update{Version: current})
	out = suite.Call(t, "server_updates", nil)
	rows = acc.Rows(t, out["updates"], "updates")
	if len(rows) != 2 || acc.Str(rows[0]["version"]) != newer || !acc.IsBool(rows[0]["latest"], true) || acc.IsBool(rows[0]["installed"], true) || !acc.IsBool(out["update_available"], true) {
		t.Errorf("server_updates with a newer release = %v", out)
	}
	if added := acc.Strs(t, acc.OrEmptyList(rows[0]["new"]), "new"); len(added) != 1 || added[0] != "a new thing" {
		t.Errorf("the newer release's changes = %v", added)
	}

	// with the update server away the list cannot be had, and it says why
	// rather than failing
	home.SetDown(true)
	t.Cleanup(func() { home.SetDown(false) })
	out = suite.Call(t, "server_updates", nil)
	if acc.Str(out["current"]) != current || !strings.Contains(acc.Str(out["unavailable"]), "update service") {
		t.Errorf("server_updates with the update server down = %v", out)
	}
}

// A backup is put back over what was changed after it, and Prowlarr restarted
// to use it: the tag made since is gone, and everything made before is as it
// was. It is the last test in the suite, since it takes Prowlarr down.
func TestBackupRestoreAndRestart(t *testing.T) {
	indexersBefore := acc.Num(t, suite.Call(t, "indexer_list", nil)["total"], "total")
	made := suite.Call(t, "backup_create", nil)
	name := acc.Str(acc.Object(t, made["backup"], "backup")["name"])
	if name == "" {
		t.Fatalf("backup_create = %v", made)
	}
	suite.DeleteLaterIfThere(t, "backup_delete", map[string]any{"backup": name, "confirm": true}, "no backup")

	const since = "made-after-the-backup"
	suite.Call(t, "tag_create", map[string]any{"label": since})
	suite.DeleteLaterIfThere(t, "tag_delete", map[string]any{"label": since}, "no tag")
	has := func() bool {
		for _, tag := range acc.Rows(t, suite.Call(t, "tag_list", nil)["tags"], "tags") {
			if acc.Str(tag["label"]) == since {
				return true
			}
		}

		return false
	}

	// without confirm it says what it would write over, and writes nothing
	would := suite.Call(t, "backup_restore", map[string]any{"backup": name})
	if acc.Str(would["would_restore"]) != name || len(acc.Strs(t, would["overwrites"], "overwrites")) == 0 || !has() {
		t.Fatalf("without confirm = %v, tag there %v", would, has())
	}

	restored := suite.Call(t, "backup_restore", map[string]any{"backup": name, "confirm": true})
	if acc.Str(restored["restored"]) != name || !strings.Contains(acc.Str(restored["note"]), "server_restart") {
		t.Fatalf("restored = %v", restored)
	}
	back := suite.Call(t, "server_restart", map[string]any{"wait": 120})
	if !acc.IsBool(back["restarted"], true) || acc.Str(back["start_time"]) == "" {
		t.Fatalf("server_restart = %v", back)
	}

	// the restored database is the one in use: what was made since is gone,
	// and what was there is there
	if has() {
		t.Errorf("the tag made after the backup is still there after the restore")
	}
	if got := acc.Num(t, suite.Call(t, "indexer_list", nil)["total"], "total"); got != indexersBefore {
		t.Errorf("%d indexers after the restore, %d before the backup", got, indexersBefore)
	}
	// Prowlarr checks its health as it starts, with the suite's proxy there to answer it this time
	if !acc.EventuallyWithin(patience, func() bool { return acc.Num(t, suite.Call(t, "server_health", nil)["total"], "total") > 0 }) {
		t.Error("the restarted Prowlarr reports no health checks")
	}
}
