// Package tools defines the MCP tools exposed by prowlarr-mcp, split by the
// resource they act on (indexers.go, apps.go, releases.go, ...) and the
// audits (audit_*.go). Tools are named resource-first (indexer_*, app_*,
// release_*, audit_*) so they group by what they act on.
//
// Every tool is registered through add with a kind: read tools never change
// Prowlarr's state, write tools do (and are dropped under --read-only), and
// delete tools remove indexers, applications and the other things Prowlarr
// holds (and are only registered with --enable-delete, and change nothing
// without confirm). --toolsets and
// --allow-tools ask for tools, and --deny-tools takes tools out of what they
// asked for. Which tools a session gets from all that, and what each tells a
// client about itself, is go-kt's registry's work.
package tools

import (
	"context"
	"errors"

	"github.com/katbyte/go-kt/lock"
	mcpregistry "github.com/katbyte/go-kt/mcp/registry"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options controls which tools are registered.
type Options struct {
	// ReadOnly registers only tools that never change Prowlarr's state.
	ReadOnly bool
	// EnableDelete registers the tools that delete indexers, applications,
	// profiles, proxies, download clients, notifications, saved filters and
	// backups. Off unless the operator opts in.
	EnableDelete bool
	// Toolsets, when set, restricts registration to the named groups (see
	// Toolsets). "core" is always included, so a set can be asked for on its
	// own.
	Toolsets []string
	// Allow asks for tools by name: exact names, prefix/suffix globs
	// (indexer_*, *_delete) or the "essential" preset. Beside Toolsets it
	// adds to them, "these sets and these tools as well"; on its own it is
	// only the tools it names. It lets nothing past ReadOnly or EnableDelete.
	Allow []string
	// Deny removes matching tools from whatever Toolsets and Allow asked for:
	// it is how a toolset is narrowed.
	Deny []string
}

// Toolsets group the tools by the job someone is doing, so a client can load a
// working subset instead of all of them. The whole surface is several
// thousand tokens of tool definitions (name, description, input schema)
// before anyone has asked a question; core alone is a fraction of that.
//
// "all" is every tool, which is what the library does when no toolset is asked
// for; the prowlarr-mcp binary defaults to core instead (see
// cli.FlagData.ToolOptions).
//
// --toolsets also takes a resource family - indexer, app, audit, release,
// history, ... - which is every tool with that prefix. Those are derived from
// the registered names rather than listed here, so they cannot go stale.
//
// Every tool belongs to exactly one set (TestToolsetsPartition proves it), and
// core is added to whatever else is asked for, because none of the other sets
// can find an indexer or an application on their own.
var Toolsets = map[string][]string{
	// enough to find things and read them: the base every other set assumes
	"core": {
		"server_info", "indexer_list", "indexer_get", "app_list", "release_search", "history_list",
	},
	// find what is wrong with the indexers and the applications they feed, and
	// fix it: every audit, and every tool an audit's worklist is fixed with -
	// testing, editing and re-syncing indexers and applications, the sync
	// profiles, the proxies, and the tags that route one to the other
	"curation": {
		"audit_all", "audit_failing", "audit_unreliable", "audit_slow", "audit_unused", "audit_unsynced", "audit_apps",
		"audit_duplicates", "audit_seeding", "audit_limits", "audit_vip", "audit_definitions", "audit_proxies",
		"audit_download_clients", "audit_tags", "audit_health", "audit_profiles",
		"indexer_test", "indexer_edit", "indexer_bulk_edit", "indexer_stats",
		"app_get", "app_test", "app_edit", "app_sync", "profile_list", "profile_edit",
		"proxy_list", "proxy_test", "proxy_edit", "tag_list", "tag_edit", "tag_delete", "server_health",
	},
	// set Prowlarr up: find a tracker in the catalogue and add it, add the
	// applications it feeds and the proxies and download clients it uses, and
	// the categories and tags that describe them
	"setup": {
		"indexer_catalog", "indexer_add", "app_add", "profile_create", "proxy_add", "downloadclient_add",
		"notification_add", "tag_create", "category_list",
	},
	// search from Prowlarr itself and send a release straight to a download
	// client, without going through an application
	"grab": {
		"release_grab", "downloadclient_list", "downloadclient_test", "downloadclient_edit",
	},
	// running Prowlarr rather than using it: scheduled tasks and what they
	// queue, logs, updates, its own settings, backups and a restart,
	// notifications, the filters saved in its web interface, and the tools
	// that remove things
	"admin": {
		"task_list", "task_run", "command_list", "command_cancel", "server_logs", "server_log", "server_updates",
		"server_settings", "server_settings_edit", "server_restart",
		"backup_list", "backup_create", "backup_restore", "backup_delete",
		"notification_list", "notification_test", "notification_edit", "notification_delete",
		"filter_list", "filter_create", "filter_edit", "filter_delete",
		"indexer_delete", "app_delete", "profile_delete", "proxy_delete", "downloadclient_delete",
	},
}

// Instructions are what a client is told on connecting, after what go-kt
// says of every server: the rules that hold between these tools, said once
// so that no tool's description has to. TestEveryListPagesAndEveryDeleteConfirms
// holds the tools to the first two.
var Instructions = []string{
	"Every list takes limit (100 unless given, 1000 at most) and offset, and answers total, offset and, while there is another page, the next_offset to pass back as offset.",
	"A tool that deletes changes nothing without confirm: called without it, it answers would_delete and loses, what a confirmed call would remove and what goes with it for good. backup_restore asks the same way, and so does server_settings_edit for a setting that decides how Prowlarr is reached or who may sign in.",
	"An indexer, an application, a proxy, a download client, a notification, a sync profile and a tag are each named by name or by Prowlarr id. Only some toolsets may be loaded: a tool that a description or an audit's fix names and the session does not list was left out when the server was started, not removed. With the audits loaded, start with audit_all, which says where the setup needs work, then run the audit whose count is not zero for its worklist.",
	"A release's name, a history entry, what a site or an application answered a test with, a definition's description and a line of the log were written outside Prowlarr. They are shown with the characters that do not display taken out, and cut short when very long.",
}

// EssentialTools is the curated preset selected by --allow-tools essential:
// enough to see the indexers, search them, and find out what is wrong.
var EssentialTools = []string{
	"indexer_list",
	"release_search",
	"audit_all",
	"indexer_test",
	"history_list",
}

const (
	readTool   = mcpregistry.Read
	writeTool  = mcpregistry.Write
	deleteTool = mcpregistry.Delete
)

// registry is this application's tools and what they share. Which of them a
// session gets, and what each tells a client about itself, is go-kt's
// registry's work (tools), so the allow and deny patterns are checked
// against every tool before any is registered.
type registry struct {
	client *prowlarr.Client
	opts   Options
	// tools holds every tool queued, made when the first is (see queued)
	tools *mcpregistry.Registry
	// locks keep the calls of one turn from undoing each other (see
	// locks.go): a set of this server's own
	locks lock.Set
	// errorLog is where a handler's panic is logged; nil is the process's
	// own log, which writes to stderr
	errorLog func(format string, args ...any)
	// writeLog is where the line for each call of a write or a delete tool
	// is written (see writelog.go); nil is the process's own log
	writeLog func(format string, args ...any)

	// the catalogue of trackers Prowlarr can add, read once and kept a
	// while; see catalog.go
	catalogCache catalogCache
}

// toolHints are what a tool tells a client about itself beyond its kind, in
// the MCP annotations' terms. A write tool is destructive when it can change
// or take away what was there - a setting written over, a tag removed, the
// indexers a sync takes out of an application - and additive when it only
// ever adds; idempotent when calling it again with the same arguments changes
// nothing more, which includes a repeat that is refused.
//
// Every write tool has an entry, each decided on its own rather than taken
// from the kind: marked all alike, app_sync (it removes the indexers an
// application should no longer have) and task_run (housekeeping deletes old
// history) read as additive to a client. A write tool missing from here is
// marked destructive, and fails TestEveryWriteToolIsHinted.
//
// One that is open world reaches past Prowlarr and what it manages, as an
// email does: a test notification is sent to whoever the notification
// reaches. A grab is not: Prowlarr hands the release to its own download
// client.
var toolHints = map[string]mcpregistry.Hints{
	// a new indexer beside the others; asked again it adds a second from the
	// same definition, which is what audit_duplicates finds
	"indexer_add": {Additive: true},
	// settings, tags and seed goals written over, the same ones again the
	// second time
	"indexer_edit":      {Idempotent: true},
	"indexer_bulk_edit": {Idempotent: true},
	// a new application beside the others; asked again it connects the same
	// one twice, which audit_apps finds
	"app_add": {Additive: true},
	// settings and tags written over, the same ones again the second time
	"app_edit": {Idempotent: true},
	// adds, rewrites and removes indexers in every application until each
	// holds what it should; a second sync finds nothing left to change
	"app_sync": {Idempotent: true},
	// a new profile, which nothing uses until an indexer is given it
	"profile_create": {Additive: true},
	// switches written over, the same ones again the second time
	"profile_edit": {Idempotent: true},
	// a new proxy, used by the indexers that share its tag
	"proxy_add": {Additive: true},
	// settings and tags written over, the same ones again the second time
	"proxy_edit": {Idempotent: true},
	// a new download client beside the others
	"downloadclient_add": {Additive: true},
	// settings written over, the same ones again the second time
	"downloadclient_edit": {Idempotent: true},
	// a new label; one that exists is refused before anything is sent
	"tag_create": {Additive: true, Idempotent: true},
	// a label written over, the same one again the second time
	"tag_edit": {Idempotent: true},
	// a tag nothing carries is removed; asked again there is no such tag
	"tag_delete": {Idempotent: true},
	// a release handed to a download client and a grab recorded, with nothing
	// in Prowlarr written over; asked again it is sent again
	"release_grab": {Additive: true},
	// a sync takes indexers out of applications, housekeeping deletes old
	// history, and each backup is one more
	"task_run": {},
	// a queued command is taken off the queue; asked again it is no longer
	// there to take
	"command_cancel": {Idempotent: true},
	// one more backup beside the others, kept until it is deleted
	"backup_create": {Additive: true},
	// the database and settings written over with a backup's
	"backup_restore": {},
	// what is under way is cut off, and Prowlarr is down until it is back
	"server_restart": {},
	// settings written over, the same ones again the second time
	"server_settings_edit": {Idempotent: true},
	// a new notification beside the others; asked again it adds a second
	"notification_add": {Additive: true},
	// settings, events and tags written over, the same ones again the
	// second time
	"notification_edit": {Idempotent: true},
	// a new saved filter beside the others; asked again it saves a second
	"filter_create": {Additive: true},
	// a label and conditions written over, the same ones again the second
	// time
	"filter_edit": {Idempotent: true},

	// a read: Prowlarr is not changed, but the message goes out to whoever
	// the notification reaches
	"notification_test": {SendsOut: true},
}

// queued is every tool queued so far, in go-kt's registry, which is made
// when the first tool is.
func (r *registry) queued() *mcpregistry.Registry {
	if r.tools == nil {
		r.tools = mcpregistry.New(mcpregistry.Config{
			Toolsets:  Toolsets,
			Essential: EssentialTools,
			Hints:     toolHints,
			LogError:  r.errorLog,
			LogWrite:  r.logWrite,
		})
	}

	return r.tools
}

// add queues a typed tool for registration. go-kt's registry sets the MCP
// annotations from kind and the tool's hints so clients can tell read-only
// from destructive tools without parsing descriptions, turns a panic in the
// handler into an ordinary tool error, and sends empty collections as []
// rather than null: an AI client reading "findings": null cannot tell "none"
// from "not fetched", and Go leaves un-appended slices nil.
//
// An error a tool answers with is Prowlarr's own words as often as not, and
// Prowlarr quotes the whole address of a request that failed, an indexer's
// API key among its parameters. So an error reaches a client with every
// address in it blanked, as what a tool shows of a failure is (outsideSaid).
func add[In, Out any](r *registry, kind mcpregistry.Kind, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcpregistry.Add(r.queued(), kind, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err != nil {
			if said := blankAddresses(err.Error()); said != err.Error() {
				err = errors.New(said)
			}
		}

		return res, out, err
	})
}

// queuedFor is every tool queued for these options: registration never calls
// the client, only the handlers do.
func queuedFor(client *prowlarr.Client, opts Options) *registry {
	r := &registry{client: client, opts: opts}
	queueTools(r)

	return r
}

// RegisterAll adds every tool permitted by opts to the MCP server and returns
// the names registered. It fails when an allow/deny pattern matches no tool,
// so a typo cannot silently hide one.
func RegisterAll(server *mcp.Server, client *prowlarr.Client, opts Options) ([]string, error) {
	return queuedFor(client, opts).queued().Register(server, opts.selection())
}

// selection is the part of the options that chooses tools, as go-kt's
// registry takes it.
func (o *Options) selection() mcpregistry.Selection {
	return mcpregistry.Selection{
		ReadOnly:     o.ReadOnly,
		EnableDelete: o.EnableDelete,
		Toolsets:     o.Toolsets,
		Allow:        o.Allow,
		Deny:         o.Deny,
	}
}

// queueTools queues every tool, before any filtering.
func queueTools(r *registry) {
	registerServerTools(r)
	registerIndexerTools(r)
	registerIndexerEditTools(r)
	registerCatalogTools(r)
	registerStatsTools(r)
	registerReleaseTools(r)
	registerAppTools(r)
	registerProfileTools(r)
	registerProxyTools(r)
	registerDownloadClientTools(r)
	registerNotificationTools(r)
	registerTagTools(r)
	registerBackupTools(r)
	registerCommandTools(r)
	registerSettingsTools(r)
	registerFilterTools(r)
	registerAuditTools(r)
}

// ToolInfo describes a registered tool without a server to register it on.
type ToolInfo = mcpregistry.Info

// described is every tool queued with no server behind it, for the answers
// that need none.
func described(opts Options) (*registry, error) {
	client, err := prowlarr.New("https://describe.invalid", "describe")
	if err != nil {
		return nil, err
	}

	return queuedFor(client, opts), nil
}

// Describe lists the tools opts would register, for `prowlarr-mcp tools`. It
// needs no connectivity, and makes the same choice RegisterAll does.
func Describe(opts Options) ([]ToolInfo, error) {
	r, err := described(opts)
	if err != nil {
		return nil, err
	}

	return r.queued().Describe(opts.selection())
}

// ToolsetNames lists the curated toolsets, for help output.
func ToolsetNames() []string {
	return mcpregistry.New(mcpregistry.Config{Toolsets: Toolsets}).ToolsetNames()
}

// FamilyNames lists the resource prefixes accepted by --toolsets, for help
// output.
func FamilyNames() []string {
	r, err := described(Options{})
	if err != nil {
		return nil
	}

	return r.queued().FamilyNames()
}
