# The tools

Tools are named resource-first (`indexer_*`, `app_*`, `audit_*`, `server_*`...) so they group by what they act on. Every tool carries MCP annotations (read-only or destructive, and whether a repeat changes anything more) and tools that change Prowlarr's state say so in their descriptions. Wherever a tool takes an indexer, application, profile, proxy, download client, notification, saved filter, backup or tag it accepts a name as well as an id, and an unknown name comes back as an error listing what exists. Anything that reads history or statistics takes a window in days.

**Every list is read the same way.** A tool that answers a list takes `limit` (100 unless given, 1000 at most) and `offset`, and answers `total`, `offset` and, until the last page, `next_offset` to pass back as the next `offset`. A list cut short always says how much there is. An audit counts its findings as `total_findings`.

**A delete asks first.** A tool that deletes takes `confirm`. Without it nothing changes, and the answer says what would go (`would_delete`) and what goes with it for good (`loses`): an indexer's history and statistics, the indexers pinned to a download client. `backup_restore` asks the same way, and so does `server_settings_edit` for a setting that decides how Prowlarr is reached or who may sign in.

**Text from outside is shown clean.** A release's name is whatever a public indexer said it was, and a model reads what it is given. A release's name, a history entry, what a site or an application answered a test with, a definition's description and a line of the log are shown with the characters that do not display taken out (line breaks become spaces) and cut when very long: 300 characters for a name, 2,000 for running text. What a later call hands back, such as a release's `guid`, is left exactly as it came. Prowlarr quotes the whole address of a request that failed, an indexer's API key among its parameters, so an address in what Prowlarr, a site or an application said went wrong is shown without who it signs in as, the value of any parameter, or what follows its `#`: in a failed test, a health message, a line of the log and a tool's own error. A client that connects is told this, and the two rules above, in the server's instructions.

**Calls made at once take turns.** A client's calls in one turn run at once, and an edit sends a whole record back; two edits of one indexer, application, profile, proxy, download client, notification, filter or settings section wait for each other rather than each undoing the other.

| Resource | Tools |
|---|---|
| server | `server_info`, `server_health` (what each check means and what fixes it), `server_logs`, `server_log`, `server_updates`, `server_settings` (Prowlarr's own settings by section, credentials never shown), `server_settings_edit` (reads the section back, and says which settings Prowlarr took and did not keep), `server_restart` (waits for it to answer again) |
| backups | `backup_list`, `backup_create`, `backup_restore` (puts one back over everything changed since; a restart finishes it), `backup_delete` |
| tasks | `task_list`, `task_run` (waits for it, or queues it with `wait=-1`), `command_list` (what is queued, running and lately finished), `command_cancel` (one still queued) |
| indexers | `indexer_list`, `indexer_get` (settings, categories, searches, which applications receive it and why not, failures, 30 days of statistics, last ten events), `indexer_catalog` (the definitions Prowlarr ships, searchable), `indexer_add`, `indexer_edit`, `indexer_bulk_edit` (priority, profile, tags, seed goals across many), `indexer_test`, `indexer_delete`, `indexer_stats`, `category_list` |
| audits | the 19 audits in [audits.md](audits.md) |
| releases | `release_search` (as an application would, by words and kind, narrowed to categories, indexers or protocol), `release_grab` (sends one, or several from one indexer at once, to a download client) |
| applications | `app_list`, `app_get` (what it receives, what is held back, what it skips), `app_add`, `app_edit`, `app_test`, `app_sync`, `app_delete` |
| profiles | `profile_list`, `profile_create`, `profile_edit`, `profile_delete` |
| proxies | `proxy_list`, `proxy_add`, `proxy_edit`, `proxy_test`, `proxy_delete` |
| download clients | `downloadclient_list`, `downloadclient_add`, `downloadclient_edit`, `downloadclient_test`, `downloadclient_delete` |
| notifications | `notification_list`, `notification_add` (Discord, email, a webhook and the rest, with what it is sent for), `notification_edit`, `notification_test`, `notification_delete` |
| saved filters | `filter_list`, `filter_create`, `filter_edit`, `filter_delete` (the filters of the web interface's indexers, statistics, history and search pages) |
| tags | `tag_list` (with what carries each one), `tag_create`, `tag_edit` (rename), `tag_delete` |
| history | `history_list` (queries, RSS syncs, logins and grabs, with elapsed time and result counts; keys in URLs redacted) |

The eight tools that delete (`indexer_delete`, `app_delete`, `profile_delete`, `proxy_delete`, `downloadclient_delete`, `notification_delete`, `filter_delete` and `backup_delete`) are only registered when `--enable-delete` / `PROWLARR_ENABLE_DELETE` is set, and each still asks for `confirm`. `--read-only` registers the 48 read tools and nothing else, so a write tool is absent from `tools/list` rather than refused when called.

## Choosing which tools load

**The default is `core`: six read-only tools, about 1,300 tokens.** The whole surface is around 15,000 tokens of tool definitions before anyone asks a question, which is a poor way to spend a client's context by default. `--toolsets` / `PROWLARR_TOOLSETS` loads the groups a session actually needs, and `core` comes along with whatever else is asked for, because nothing else can find an indexer or read the history.

**Auditing a setup needs `PROWLARR_TOOLSETS=curation`** - every audit, and everything that fixes what they find. `PROWLARR_TOOLSETS=all` restores every tool.

| toolset | tools | with core | ~tokens |
|---|---|---|---|
| `core` *(default)* | 6 | 6 | 1,300 |
| `grab` | 4 | 10 | 1,900 |
| `setup` | 9 | 15 | 3,700 |
| `admin` | 19 (27 with delete) | 25 | 4,600 |
| `curation` | 36 | 42 | 8,500 |
| `all` | 74 (82 with delete) | 74 | 15,000 |

Tokens are what the model sees: each tool's name, description and input schema, measured over a real `tools/list` at four bytes a token. Every tool also carries an output schema, another 20,100 tokens across `all`, but clients keep that to themselves to validate results rather than sending it to the model.

`--toolsets` also takes a resource family - `indexer`, `app`, `audit`, `release`, `profile`, `proxy`, `downloadclient`, `notification`, `filter`, `tag`, `history`, `category`, `server`, `task`, `command`, `backup` - which is every tool with that prefix:

```sh
PROWLARR_TOOLSETS=all                 # every tool
PROWLARR_TOOLSETS=curation            # the audits plus everything that fixes what they find
PROWLARR_TOOLSETS=setup               # adding indexers, applications, profiles and proxies
PROWLARR_TOOLSETS=audit               # read-only detection, nothing that writes
PROWLARR_TOOLSETS=core,indexer,app    # core plus two whole families
```

`prowlarr-mcp tools` prints what the current flags would register, grouped by toolset, and needs no server:

```sh
prowlarr-mcp tools                    # the default set
prowlarr-mcp tools --toolsets all     # every tool
prowlarr-mcp tools --read-only -q     # names only
```

## One tool more, or a few less

`--allow-tools` asks for tools by name, as `--toolsets` asks for them by set, and a session gets what either names: "these sets, and this tool as well". On its own, with no toolset beside it, it is only the tools it names. `--deny-tools` takes tools out of whatever was asked for, which is how a toolset is narrowed. Both take comma-separated tool names, globs with a leading or trailing `*`, and `--allow-tools` the `essential` preset (`indexer_list`, `release_search`, `audit_all`, `indexer_test`, `history_list`). Neither gets a tool past `--read-only` or the delete gate.

```sh
PROWLARR_TOOLSETS=curation PROWLARR_ALLOW_TOOLS=release_grab    # the curation set, and the grab as well
PROWLARR_ALLOW_TOOLS=essential                                  # the five essential tools and nothing else
PROWLARR_ALLOW_TOOLS=indexer_*,app_list,audit_*                 # only these
PROWLARR_TOOLSETS=curation PROWLARR_DENY_TOOLS=audit_*          # the curation set without its audits
PROWLARR_DENY_TOOLS=*_delete,release_grab
```

Before 0.2 an allow list beside toolsets narrowed them, so a tool the sets did not hold was silently left out.

A pattern that matches no tool aborts startup and names it, so a typo cannot silently hide a tool.

## A typical audit session

1. `audit_all` says where the setup needs work, in counts.
2. `audit_failing` and `audit_unreliable` name the indexers that are broken now; `indexer_test` says why, and `indexer_edit` fixes credentials, cookies or the address.
3. `audit_unsynced` says what the applications never receive, and why: `indexer_edit` or `app_edit` puts the tag, category or profile right, then `app_sync` pushes it.
4. `audit_definitions` finds the indexers their definition has outgrown; `indexer_catalog` and `indexer_add` replace them.
5. `audit_vip` and `audit_limits` say what is about to stop working.
6. `audit_seeding` finds the private trackers with no seed goal; `indexer_bulk_edit` sets one on all of them at once.
7. `audit_unused` and `audit_slow` say what to drop: every search waits for them.
