# Tool design

Design rules, in priority order:

1. **Wrap judgment, and say what a write costs.** A tool exists where an AI has a decision to make. The parts of the API that are plumbing for other programs stay unwrapped (see the end); the parts a person runs Prowlarr with are all here, and the ones that can cost something say so and ask first.
2. **Trim every response.** Tools return the fields a decision needs, never raw resources (an `IndexerResource` carries every setting of every definition; `indexer_list` returns nine fields a row). Credentials are named, never shown.
3. **Composite over chatty.** If a task always takes N calls (find the definition → read its schema → fill the settings → create → enable), it is one tool, not N.
4. **Names, not just ids.** Every tool that takes an indexer, application, profile, proxy, download client, notification, saved filter, backup or tag resolves a name, and an unknown one lists what exists.
5. **Resource-first names** (`indexer_*`, `app_*`, `audit_*`, `server_*`) so tools group by what they act on.
6. **Reads are cheap, writes are explicit, destructive is opt-in.** Every tool carries MCP annotations, each write tool's decided on its own; anything that changes Prowlarr says so in its description; anything that deletes is disabled unless the operator sets `--enable-delete`.
7. **One way to say each thing.** Members of a list are added and removed with `add_x` and `remove_x` lists (`add_tags`, `add_on`); a single thing is set by giving its value. Every list takes `limit` and `offset` and answers `total` and `next_offset`, with one default (100) and one cap (1000).
8. **`confirm` before what cannot be put back.** A tool that deletes, restores a backup, or changes how Prowlarr is reached changes nothing without `confirm`, and says what it would do: `would_delete` with what it `loses`, `would_restore` with what it `overwrites`, `would_change`.
9. **An audit names its fix.** Every finding carries the tool that deals with it, so a worklist is actionable without reading the code.
10. **What strangers wrote is data.** A release's name, a site's answer to a test and a line of the log are shown with what does not display taken out and cut to a length (go-kt's `outside.Text`), and an address in them without anything that may be a credential (`outside.BlankAddresses`). A value a later call takes, a release's `guid` or a definition's name, goes back as it came, or it would no longer find what it names.
11. **Calls made at once take turns.** An edit locks the record it has resolved before it reads what it will send back, so two edits of one thing in one turn do not undo each other.

## The tools

| Area | Tools | Answers |
|---|---|---|
| know the setup | `server_info`, `server_health`, `indexer_list`, `indexer_get`, `indexer_stats`, `history_list`, `app_list`, `app_get`, `profile_list`, `proxy_list`, `downloadclient_list`, `notification_list`, `tag_list`, `category_list`, `backup_list`, `command_list`, `filter_list`, `server_settings` | "what is wired up, and is it working" |
| curation | `audit_all` + 18 audits, `indexer_test`, `indexer_edit`, `indexer_bulk_edit`, `app_edit`, `app_sync`, `app_test`, `profile_edit`, `proxy_edit`, `proxy_test`, `tag_edit`, `tag_delete` | "what is wrong, and fix it" |
| setup | `indexer_catalog`, `indexer_add`, `app_add`, `profile_create`, `proxy_add`, `downloadclient_add`, `notification_add`, `tag_create` | "add a tracker, get it into Sonarr, and be told when it breaks" |
| grab | `release_search`, `release_grab`, `downloadclient_list`, `downloadclient_edit`, `downloadclient_test` | "find this and send it to the client" |
| admin | `server_logs`, `server_log`, `server_updates`, `server_settings_edit`, `server_restart`, `task_list`, `task_run`, `command_cancel`, `backup_create`, `backup_restore`, `notification_edit`, `notification_test`, `filter_create`, `filter_edit`, and the eight deletes | "keep it healthy, and keep a way back" |

## Guarded

- The eight tools that delete (`indexer_delete`, `app_delete`, `profile_delete`, `proxy_delete`, `downloadclient_delete`, `notification_delete`, `filter_delete`, `backup_delete`) are only registered when `--enable-delete` (`PROWLARR_ENABLE_DELETE`) is set, and each asks for `confirm`.
- `backup_restore` asks for `confirm` and says what it writes over.
- `server_settings_edit` asks for `confirm` before it changes the port, the address, the URL base, SSL, the API key, or how and as whom anyone signs in: a wrong value there cuts prowlarr-mcp off from Prowlarr, and nothing here can put it back.

## Not wrapped, and why

`sdk/prowlarr` covers all of these, since it is a complete client; none is a tool.

- **Shutting Prowlarr down.** `server_restart` brings it back; nothing here could start one that was shut down. (The client suite does test it, because the test container's supervisor starts Prowlarr again.)
- **Restoring from an uploaded backup.** The file would come from the machine prowlarr-mcp runs on, which is rarely where the person asking keeps it. `backup_restore` restores one Prowlarr already holds. The client can send one, as a multipart form.
- **Clearing an indexer's failures.** Prowlarr's API has no way to stop holding an indexer back; it lets go when the hold runs out, or when the indexer is edited.
- **The lookups a settings form makes** (`.../action/{name}`), **the file system browser, the localisation tables and the route list**: plumbing for the web interface's own forms.
- **The Newznab and Torznab feeds themselves**: they are for the applications. `release_search` asks the same indexers the same way.
- **The bulk edit and delete routes for applications and download clients**: a setup has a handful of each, and `app_edit` and `downloadclient_edit` change them one at a time. `indexer_bulk_edit` and `indexer_delete` with `indexers` exist because there are dozens of indexers, and an audit turns up several at once.
