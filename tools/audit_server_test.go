package tools

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// Nobody hears of a health issue when no notification is sent for one; one
// sent for nothing is off in effect, one without the warnings misses most of
// what Prowlarr reports, and with test=true one that cannot be sent is named.
func TestAuditNotificationsCanned(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/notification", []any{
		discord(1, "Quiet"),
		discord(2, "Grabs", "grab"),
	})
	cs := session(t, f, Options{})
	if got, want := problems(t, mustCall(t, cs, "audit_notifications", nil)), []string{"Quiet/sent_for_nothing", "notifications/no_health_notification"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}

	// one sent for health issues, without the warnings, and failing its
	// test: nothing sends a test unless asked
	f.answer("GET /api/v1/notification", []any{discord(3, "Health", "health_issue")})
	f.answerStatus("POST /api/v1/notification/test", http.StatusInternalServerError, map[string]any{"message": "One or more errors occurred. (Connection refused)"})
	if got, want := problems(t, mustCall(t, cs, "audit_notifications", nil)), []string{"Health/health_warnings_off"}; !slices.Equal(got, want) || len(f.requested("POST /api/v1/notification/test")) != 0 {
		t.Errorf("findings = %v, want %v, after %d tests", got, want, len(f.requested("POST /api/v1/notification/test")))
	}
	out := mustCall(t, cs, "audit_notifications", map[string]any{"test": true})
	if got, want := problems(t, out), []string{"Health/health_warnings_off", "Health/test_failed"}; !slices.Equal(got, want) {
		t.Errorf("with test = %v, want %v", got, want)
	}
	if detail := detailOf(t, out, "Health", "test_failed"); !slices.Contains([]string{detail}, detail) || detail == "" {
		t.Errorf("detail = %q", detail)
	}
}

// No backup at all, or a newest one older than the schedule allows, is the
// way back missing.
func TestAuditBackupsCanned(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/config/host", hostSettings())
	f.answer("GET /api/v1/system/backup", []any{})
	cs := session(t, f, Options{})
	if got, want := problems(t, mustCall(t, cs, "audit_backups", nil)), []string{"backups/no_backup"}; !slices.Equal(got, want) {
		t.Errorf("with none = %v, want %v", got, want)
	}

	// the host settings canned take one every 7 days: one from 3 days ago
	// is in time, one from 10 days ago was missed
	stamp := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339) }
	f.answer("GET /api/v1/system/backup", []any{
		map[string]any{"id": 1, "name": "old.zip", "type": "scheduled", "time": stamp(20 * 24 * time.Hour), "size": 10},
		map[string]any{"id": 2, "name": "new.zip", "type": "scheduled", "time": stamp(3 * 24 * time.Hour), "size": 10},
	})
	if got := problems(t, mustCall(t, cs, "audit_backups", nil)); len(got) != 0 {
		t.Errorf("with a backup in time = %v, want none", got)
	}
	f.answer("GET /api/v1/system/backup", []any{map[string]any{"id": 1, "name": "old.zip", "type": "scheduled", "time": stamp(10 * 24 * time.Hour), "size": 10}})
	out := mustCall(t, cs, "audit_backups", nil)
	if got, want := problems(t, out), []string{"backups/backup_stale"}; !slices.Equal(got, want) {
		t.Errorf("with a stale backup = %v, want %v", got, want)
	}
	if detail := detailOf(t, out, "backups", "backup_stale"); detail == "" || !slices.Contains([]string{detail}, detail) {
		t.Errorf("detail = %q", detail)
	}
}
