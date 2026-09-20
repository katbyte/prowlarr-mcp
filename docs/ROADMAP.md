# Tool roadmap

Design rules, in priority order:

1. **Wrap judgment, not plumbing.** A tool exists only where an AI has a decision to
   make. The web interface's own routes, the file system browser, saved filters and
   the localisation tables stay unwrapped.
2. **Trim every response.** Tools return the fields a decision needs, never raw
   resources (an `IndexerResource` carries every setting of every definition;
   `indexer_list` returns nine fields a row). Credentials are named, never shown.
3. **Composite over chatty.** If a task always takes N calls (find the definition →
   read its schema → fill the settings → create → enable), it is one tool, not N.
4. **Names, not just ids.** Every tool that takes an indexer, application, profile,
   proxy, download client or tag resolves a name, and an unknown one lists what
   exists.
5. **Resource-first names** (`indexer_*`, `app_*`, `audit_*`, `server_*`) so tools
   group by what they act on.
6. **Reads are cheap, writes are explicit, destructive is opt-in.** Every tool carries
   MCP annotations; anything that changes Prowlarr says so in its description;
   anything that deletes a provider is disabled unless the operator sets
   `--enable-delete`.
7. **An audit names its fix.** Every finding carries the tool that deals with it, so a
   worklist is actionable without reading the code.

## Done

| Area | Tools | Answers |
|---|---|---|
| know the setup | `server_info`, `server_health`, `indexer_list`, `indexer_get`, `indexer_stats`, `history_list`, `app_list`, `app_get`, `profile_list`, `proxy_list`, `downloadclient_list`, `notification_list`, `tag_list`, `category_list`, `backup_list` | "what is wired up, and is it working" |
| curation | `audit_all` + 16 audits, `indexer_test`, `indexer_edit`, `indexer_bulk_edit`, `app_edit`, `app_sync`, `app_test`, `profile_edit`, `proxy_edit`, `proxy_test`, `tag_delete` | "what is wrong, and fix it" |
| setup | `indexer_catalog`, `indexer_add`, `app_add`, `profile_create`, `proxy_add`, `downloadclient_add`, `tag_create` | "add a tracker, and get it into Sonarr" |
| grab | `release_search`, `release_grab`, `downloadclient_list`, `downloadclient_edit`, `downloadclient_test` | "find this and send it to the client" |
| admin | `server_logs`, `server_log`, `server_updates`, `task_list`, `task_run`, `notification_test`, `indexer_delete`, `app_delete`, `proxy_delete`, `downloadclient_delete`, `profile_delete` | "keep it healthy" |

## Candidates

| Tool | Endpoints | Answers |
|---|---|---|
| `indexer_status_clear` | none | "stop holding this indexer back": Prowlarr exposes no way to clear a failure, so it would take a restart or a database write - not worth it until it does |
| `notification_add` / `notification_edit` | `/api/v1/notification` | wire up a webhook or a Discord message for health failures; the reads are there, the writes are a schema-driven form like the other providers |
| `backup_restore` | `/api/v1/system/backup/restore/{id}` | "put it back the way it was" - a whole-server restore is a decision a person should make at the console, not through a chat client |
| `app_categories` | `/api/v1/indexer/categories` + each application's settings | "which categories should this application sync" as a recommendation rather than a listing |

## Guarded / deliberately excluded

- `indexer_delete`, `app_delete`, `proxy_delete`, `downloadclient_delete` and
  `profile_delete`: only registered when `--enable-delete`
  (`PROWLARR_ENABLE_DELETE`) is set.
- Not wrapping **as tools**, ever: the settings sections (host, UI, download
  client and development config - changing a server's port or its API key from a
  chat client is not judgment, it is a foot-gun), restart and shutdown, the backup
  upload and restore, saved filters, the file system browser, the localisation
  tables, and the Newznab and Torznab feeds themselves (they are for applications,
  not for a model). `lib/prowlarr` covers all of it - it is a complete client -
  but none of it is judgment an AI should be making.
