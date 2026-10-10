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

// A command is one run of something Prowlarr does in the background: a sync
// to the applications, a health check, a backup. A scheduled task queues one
// when it is due, and so does anyone who asks for it.

func registerCommandTools(r *registry) {
	pc := r.client

	type commandRow struct {
		commandOut
		Task    string `json:"name"              jsonschema:"the command's own name, which task_run takes for the ones that are scheduled tasks, e.g. CheckHealth"`
		Trigger string `json:"trigger,omitempty" jsonschema:"scheduled, manual or unspecified: what queued it"`
		Queued  string `json:"queued,omitempty"`
	}
	type listIn struct {
		Status string `json:"status,omitempty" jsonschema:"only these: queued, started, completed, failed, aborted or cancelled; running is queued and started"`
		pageIn
	}
	type listOut struct {
		pageOut
		Commands []commandRow `json:"commands" jsonschema:"newest first"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "command_list",
		Description: "What Prowlarr is doing in the background and has lately done: every command queued, running and recently finished - syncs to the applications, health checks, backups, definition updates - with what queued it, how long it took and what it said. task_list is the schedule these come from, and task_run queues one now.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
		res, err := pc.GetCommand(ctx)
		if err != nil {
			return nil, listOut{}, err
		}
		want := strings.ToLower(strings.TrimSpace(in.Status))
		var rows []commandRow
		for i := range res.Model {
			c := &res.Model[i]
			status := strings.ToLower(string(c.Status))
			switch {
			case want == "":
			case want == "running" && !commandDone(c.Status):
			case want == status:
			default:
				continue
			}
			rows = append(rows, commandRow{commandOut: projectCommand(c), Task: c.Name, Trigger: string(c.Trigger), Queued: c.Queued})
		}
		if want != "" && want != "running" && !slices.Contains([]string{"queued", "started", "completed", "failed", "aborted", "cancelled", "orphaned"}, want) {
			return nil, listOut{}, fmt.Errorf("status must be queued, started, running, completed, failed, aborted or cancelled, got %q", in.Status)
		}
		slices.SortStableFunc(rows, func(a, b commandRow) int { return strings.Compare(b.Queued, a.Queued) })
		out := listOut{}
		out.Commands, out.pageOut = paged(rows, in.Limit, in.Offset, defaultLimit)

		return nil, out, nil
	})

	type cancelOut struct {
		Cancelled commandOut `json:"cancelled" jsonschema:"the command taken off the queue"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "command_cancel",
		Description: "Take a command off Prowlarr's queue before it starts, by the id command_list gives it. One that has started cannot be stopped, and one that has finished has nothing to cancel: both are refused, saying which.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ID int `json:"id" jsonschema:"the command's id, from command_list"`
	},
	) (*mcp.CallToolResult, cancelOut, error) {
		got, err := pc.GetCommandById(ctx, in.ID)
		if err != nil {
			return nil, cancelOut{}, fmt.Errorf("no command %d: %w", in.ID, err)
		}
		c := got.Model
		switch {
		case commandDone(c.Status):
			return nil, cancelOut{}, fmt.Errorf("command %d (%s) has already %s: there is nothing to cancel", c.Id, projectCommand(c).Name, c.Status)
		case c.Status == prowlarr.CommandStatusStarted:
			return nil, cancelOut{}, fmt.Errorf("command %d (%s) has started, and Prowlarr only cancels one still queued", c.Id, projectCommand(c).Name)
		}
		if _, err := pc.DeleteCommandById(ctx, c.Id); err != nil {
			return nil, cancelOut{}, fmt.Errorf("cancelling command %d (%s), which may have started in the meantime: %w", c.Id, projectCommand(c).Name, err)
		}
		out := projectCommand(c)
		out.Status = string(prowlarr.CommandStatusCancelled)

		return nil, cancelOut{Cancelled: out}, nil
	})

	type restartOut struct {
		Restarted bool   `json:"restarted"            jsonschema:"Prowlarr went down and is answering again"`
		StartTime string `json:"start_time,omitempty" jsonschema:"when the Prowlarr answering now started"`
		Waited    int    `json:"waited_seconds"       jsonschema:"how long it took to answer again"`
		Note      string `json:"note,omitempty"       jsonschema:"what happened instead, when it did not come back in time"`
		Version   string `json:"version,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "server_restart",
		Description: "Restart Prowlarr and wait for it to answer again: what a restored backup and some settings need before they take effect. Searches, syncs and tests under way are cut off, and the applications get no answer from it for the few seconds it is down.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Wait int `json:"wait,omitempty" jsonschema:"seconds to wait for it to answer again, default 60"`
	},
	) (*mcp.CallToolResult, restartOut, error) {
		before, err := pc.GetSystemStatus(ctx)
		if err != nil {
			return nil, restartOut{}, err
		}

		// nothing else may be half way through a write when it goes down
		unlock := r.locks.All()
		defer unlock()

		if _, err := pc.PostSystemRestart(ctx); err != nil {
			return nil, restartOut{}, err
		}

		// it answers for a moment after it has been told, so the start time
		// changing is what says it went down and came back
		began := time.Now()
		deadline := began.Add(waitFor(in.Wait))
		out := restartOut{}
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, out, ctx.Err()
			case <-time.After(commandPoll):
			}
			now, err := pc.GetSystemStatus(ctx)
			if err != nil || now.Model.StartTime == before.Model.StartTime {
				continue
			}
			out.Restarted, out.StartTime, out.Version = true, now.Model.StartTime, now.Model.Version
			break
		}
		out.Waited = int(time.Since(began).Seconds())
		if !out.Restarted {
			out.Note = "Prowlarr was told to restart and had not answered as a new start within the wait: server_info says when it is back"
		}

		return nil, out, nil
	})
}
