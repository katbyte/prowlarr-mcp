package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	mcpregistry "github.com/katbyte/go-kt/mcp/registry"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A backup is a zip of Prowlarr's database and config.xml. Prowlarr writes
// one on a schedule and before an update; a restore puts one back over
// everything changed since, and takes a restart to finish.

type backupRow struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type" jsonschema:"scheduled, manual or update"`
	Time string `json:"time"`
	Size int64  `json:"size" jsonschema:"bytes"`
}

func projectBackup(b *prowlarr.BackupResource) backupRow {
	return backupRow{ID: b.Id, Name: b.Name, Type: string(b.Type), Time: shownTime(b.Time), Size: b.Size}
}

// backups reads the backups, newest first.
func (r *registry) backups(ctx context.Context) ([]prowlarr.BackupResource, error) {
	res, err := r.client.GetSystemBackup(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(res.Answer, func(a, b prowlarr.BackupResource) int { return b.Time.Compare(a.Time.Time) })

	return res.Answer, nil
}

// findBackup picks a backup by its name or id, and otherwise answers an error
// naming the ones there are.
func findBackup(list []prowlarr.BackupResource, ref string) (*prowlarr.BackupResource, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("name the backup")
	}
	names := make([]string, 0, len(list))
	for i := range list {
		if strings.EqualFold(list[i].Name, ref) || strconv.Itoa(list[i].Id) == ref {
			return &list[i], nil
		}
		names = append(names, list[i].Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no backup %q: Prowlarr has none", ref)
	}

	return nil, fmt.Errorf("no backup %q (have: %s)", ref, strings.Join(names, ", "))
}

func registerBackupTools(r *registry) {
	pc := r.client

	type backupsOut struct {
		pageOut
		Backups []backupRow `json:"backups" jsonschema:"newest first"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_list",
		Description: "Prowlarr's backups of its database and settings, newest first, with when each was taken and why (scheduled, manual, before an update). backup_create takes another, and backup_restore puts one back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pageIn) (*mcp.CallToolResult, backupsOut, error) {
		all, err := r.backups(ctx)
		if err != nil {
			return nil, backupsOut{}, err
		}
		page, where := paged(all, in.Limit, in.Offset, defaultLimit)
		out := backupsOut{pageOut: where}
		for i := range page {
			out.Backups = append(out.Backups, projectBackup(&page[i]))
		}

		return nil, out, nil
	})

	type createOut struct {
		Backup  *backupRow `json:"backup,omitempty" jsonschema:"the backup written; absent when the wait ended before it was"`
		Command commandOut `json:"command"          jsonschema:"the run that wrote it"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_create",
		Description: "Write a backup of Prowlarr's database and settings now, as its scheduled backup does, and answer with it: worth doing before a change that is hard to take back. Nothing else is touched, and a backup asked for this way is kept until it is deleted.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Wait int `json:"wait,omitempty" jsonschema:"seconds to wait for it to finish, default 60; -1 queues it and returns at once"`
	},
	) (*mcp.CallToolResult, createOut, error) {
		cmd, err := r.runCommand(ctx, "Backup", nil, mcpregistry.WaitFor(in.Wait, commandWait))
		out := createOut{Command: cmd}
		if err != nil || cmd.Status != string(prowlarr.CommandStatusCompleted) {
			return nil, out, err
		}
		// the one it wrote is the newest there is. Prowlarr names a backup
		// for the second it was taken in, so a second one inside that second
		// is written over the first, and is still the newest
		all, err := r.backups(ctx)
		if err != nil {
			return nil, out, err
		}
		if len(all) == 0 {
			return nil, out, fmt.Errorf("the backup ran (%s) but no backup is listed", cmd.Message)
		}
		row := projectBackup(&all[0])
		out.Backup = &row

		return nil, out, nil
	})

	type restoreOut struct {
		Restored     string   `json:"restored,omitempty"      jsonschema:"the backup restored"`
		WouldRestore string   `json:"would_restore,omitempty" jsonschema:"without confirm: the backup a confirmed call restores; nothing has changed"`
		Overwrites   []string `json:"overwrites"              jsonschema:"what is written over, and cannot be had back unless a backup of it was taken first"`
		Note         string   `json:"note"                    jsonschema:"what has to happen next"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_restore",
		Description: "Put a backup back: Prowlarr's database and settings become what they were when it was taken, and everything changed since is written over - indexers, applications, proxies, download clients, notifications, tags, settings, and the history recorded since. Prowlarr has to be restarted for the restored database to be used; server_restart does that. Without confirm nothing changes and the answer says what would be written over; backup_create first keeps a way back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Backup  string `json:"backup"            jsonschema:"the backup to restore, by its name in backup_list or its id"`
		Confirm bool   `json:"confirm,omitempty" jsonschema:"true to restore; without it nothing changes and the answer says what a confirmed call would write over"`
	},
	) (*mcp.CallToolResult, restoreOut, error) {
		all, err := r.backups(ctx)
		if err != nil {
			return nil, restoreOut{}, err
		}
		b, err := findBackup(all, in.Backup)
		if err != nil {
			return nil, restoreOut{}, err
		}
		out := restoreOut{Overwrites: []string{
			"every indexer, application, sync profile, proxy, download client, notification, tag and setting as they are now, replaced with what they were at " + shownTime(b.Time),
			"the history and statistics recorded since " + shownTime(b.Time),
			"Prowlarr's API key and address, if they have changed since: prowlarr-mcp then needs the ones in the backup",
		}}
		if !in.Confirm {
			out.WouldRestore = b.Name
			out.Note = "nothing has changed; backup_create first keeps what is there now"

			return nil, out, nil
		}

		// nothing else may write while the database is swapped out from under it
		unlock := r.locks.All()
		defer unlock()

		if _, err := pc.PostSystemBackupRestoreById(ctx, b.Id); err != nil {
			return nil, restoreOut{}, err
		}
		out.Restored = b.Name
		out.Note = "Prowlarr uses the restored database once it has restarted: server_restart"

		return nil, out, nil
	})

	add(r, deleteTool, &mcp.Tool{
		Name:        "backup_delete",
		Description: "Delete one of Prowlarr's backups from its disk. Without confirm nothing changes and the answer says what would go.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Backup string `json:"backup" jsonschema:"the backup to delete, by its name in backup_list or its id"`
		confirmIn
	},
	) (*mcp.CallToolResult, deletedOut, error) {
		all, err := r.backups(ctx)
		if err != nil {
			return nil, deletedOut{}, err
		}
		b, err := findBackup(all, in.Backup)
		if err != nil {
			return nil, deletedOut{}, err
		}
		loses := []string{fmt.Sprintf("the file itself: %d bytes, taken %s (%s), and the way back to Prowlarr as it was then", b.Size, shownTime(b.Time), b.Type)}
		out, err := deleting(in.Confirm, b.Name, loses, "", func() error {
			_, delErr := pc.DeleteSystemBackupById(ctx, b.Id)

			return delErr
		})

		return nil, out, err
	})
}
