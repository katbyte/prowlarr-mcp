package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The audits of what keeps a setup safe rather than of the indexers: that
// someone is told when something breaks, and that there is a way back.

// registerServerAudits adds the notification and backup audits.
func registerServerAudits(r *registry, t *auditTable) {
	type pageOnly struct{ auditPage }
	type testIn struct {
		Test bool `json:"test,omitempty" jsonschema:"also send a test through each notification that is sent for something, which reaches whoever it reaches"`
		auditPage
	}
	addAudit(r, t, &mcp.Tool{
		Name: "audit_notifications",
		Description: "Find what is wrong with the notifications, which are how anyone hears that something broke: none at all sent for a health issue (Prowlarr's own checks go unheard), one sent for nothing (switched off, in effect), one sent for health issues with the warnings left out (most of what Prowlarr reports is a warning), and with test=true one that fails its test. " +
			"notification_edit and notification_add fix them; notification_test says why one fails.",
	}, "no notification of a health issue, notifications sent for nothing or without warnings, or failing their test", func(ctx context.Context, s *snapshot, in testIn) (auditOut, error) {
		out := auditOut{}
		notes, err := s.notifications(ctx)
		if err != nil {
			return auditOut{}, err
		}
		heard := false
		for n := range notes {
			note := &notes[n]
			out.Scanned++
			on := sentFor(note)
			if len(on) == 0 {
				out.report(finding{
					Subject: note.Name, Kind: "notification", Problem: "sent_for_nothing",
					Detail: "it is sent for nothing, so it is switched off in effect", Fix: "notification_edit on=health_issue,health_restored (and grab, update as wanted), or notification_delete",
				})

				continue
			}
			if slices.Contains(on, "health_issue") {
				heard = true
				if !boolv(note.IncludeHealthWarnings) {
					out.report(finding{
						Subject: note.Name, Kind: "notification", Problem: "health_warnings_off",
						Detail: "it is sent for health issues but not for warnings, and most of what Prowlarr's checks report - a failing indexer, an application it cannot reach, an expiring VIP - is a warning", Fix: "notification_edit health_warnings=true",
					})
				}
			}
			if in.Test {
				row, err := testNotification(ctx, s.r.client, note)
				if err != nil {
					return auditOut{}, err
				}
				if !row.OK {
					out.report(finding{
						Subject: note.Name, Kind: "notification", Problem: "test_failed",
						Detail: "its test fails: " + strings.Join(row.Problems, "; "), Fix: "notification_edit its settings, or notification_delete",
					})
				}
			}
		}
		if !heard {
			out.report(finding{
				Subject: "notifications", Kind: "notification", Problem: "no_health_notification",
				Detail: fmt.Sprintf("none of the %d notifications is sent for a health issue, so nothing Prowlarr's own checks find - a failing indexer, an application it cannot reach, an expiring VIP - is told to anyone", len(notes)),
				Fix:    "notification_add kind=Discord (or Webhook, Email, ...) on=health_issue,health_restored, or notification_edit on=health_issue on one there is",
			})
		}

		return out, nil
	})

	addAudit(r, t, &mcp.Tool{
		Name: "audit_backups",
		Description: "Find what is wrong with the backups, which are the way back from a bad change: none at all, or the newest older than the scheduled backup should have left it (the scheduled backup is not running), with how old it is and how often Prowlarr is set to take one. " +
			"backup_create takes one now; task_run Backup runs the scheduled one; server_settings_edit section=host backupInterval sets how often.",
	}, "no backup, or the newest older than the schedule should allow", func(ctx context.Context, s *snapshot, _ pageOnly) (auditOut, error) {
		out := auditOut{Scanned: 1}
		backups, err := s.r.backups(ctx)
		if err != nil {
			return auditOut{}, err
		}
		host, err := s.r.client.GetConfigHost(ctx)
		if err != nil {
			return auditOut{}, err
		}
		interval := max(host.Model.BackupInterval, 1)
		if len(backups) == 0 {
			out.report(finding{
				Subject: "backups", Kind: "backup", Problem: "no_backup",
				Detail: fmt.Sprintf("Prowlarr holds no backup at all, though it is set to take one every %d days", interval),
				Fix:    "backup_create, and task_run Backup to see the scheduled one run",
			})

			return out, nil
		}
		newest := backups[0]
		taken, dated := parsedTime(newest.Time)
		if !dated {
			out.report(finding{
				Subject: "backups", Kind: "backup", Problem: "backup_time_unreadable",
				Detail: fmt.Sprintf("the newest backup, %s, is dated %q, which is not a time", newest.Name, newest.Time), Fix: "backup_create",
			})

			return out, nil
		}
		// a scheduled backup is due every interval days; one later than
		// that by a day has been missed
		age := time.Since(taken)
		if allowed := time.Duration(interval+1) * 24 * time.Hour; age > allowed {
			out.report(finding{
				Subject: "backups", Kind: "backup", Problem: "backup_stale",
				Detail: fmt.Sprintf("the newest backup, %s (%s), is %d days old, and Prowlarr is set to take one every %d days: the scheduled backup is not running", newest.Name, newest.Type, int(age.Hours()/24), interval),
				Fix:    "backup_create now; task_run Backup, and server_log for why the scheduled one did not run",
			})
		}

		return out, nil
	})
}

// notifications reads the notifications the first time an audit asks.
func (s *snapshot) notifications(ctx context.Context) ([]prowlarr.NotificationResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notes == nil {
		res, err := s.r.client.GetNotification(ctx)
		if err != nil {
			return nil, err
		}
		s.notes = res.Model
		if s.notes == nil {
			s.notes = []prowlarr.NotificationResource{}
		}
	}

	return s.notes, nil
}

// parsedTime reads a time as Prowlarr writes one, and says whether it could.
func parsedTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)

	return t, err == nil
}
