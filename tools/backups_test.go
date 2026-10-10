package tools

import (
	"strings"
	"testing"
)

// A restore changes nothing without confirm, says what it would write over,
// and with confirm restores the backup named and says a restart is needed.
func TestBackupRestoreAsksFirst(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/system/backup", []any{
		map[string]any{"id": 9, "name": "old.zip", "type": "scheduled", "time": "2026-09-01T00:00:00Z", "size": 100},
		map[string]any{"id": 10, "name": "new.zip", "type": "manual", "time": "2026-10-01T00:00:00Z", "size": 200},
	})
	f.answerOK("POST /api/v1/system/backup/restore/9", map[string]any{"restartRequired": true})
	cs := session(t, f, Options{})

	out := mustCall(t, cs, "backup_restore", map[string]any{"backup": "old.zip"})
	overwrites := listOf(out["overwrites"])
	if out["would_restore"] != "old.zip" || out["restored"] != nil || len(overwrites) == 0 || len(f.requested("POST /api/v1/system/backup/restore/9")) != 0 {
		t.Fatalf("without confirm = %v", out)
	}

	out = mustCall(t, cs, "backup_restore", map[string]any{"backup": "9", "confirm": true})
	if out["restored"] != "old.zip" || !strings.Contains(str(t, out["note"], "note"), "server_restart") || len(f.requested("POST /api/v1/system/backup/restore/9")) != 1 {
		t.Errorf("with confirm = %v", out)
	}
	if msg := callError(t, cs, "backup_restore", map[string]any{"backup": "missing.zip", "confirm": true}); !strings.Contains(msg, "new.zip, old.zip") {
		t.Errorf("a backup that is not there = %q, want the ones there are, newest first", msg)
	}

	// the list is newest first
	list := mustCall(t, cs, "backup_list", nil)
	rows := listOf(list["backups"])
	if len(rows) != 2 || objectOf(rows[0])["name"] != "new.zip" {
		t.Errorf("backup_list = %v", list)
	}
}
