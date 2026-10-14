# The audits

Each audit looks for one kind of problem and answers a worklist. Every finding names the tool that fixes it. All of them only read; none changes Prowlarr.

An audit that reads history or statistics takes a window in days. Each takes `limit` and `offset` and answers `total_findings`.

## audit_all

Every audit in one call, counts only, so one call says where a setup needs work - start here.

**Fix with:** the audit it points to.

## audit_failing

Indexers failing now: the ones Prowlarr has stopped using after failures (how long they have been failing, and until when they are held back), and enabled ones whose last query or grab failed.

**Fix with:** `indexer_test` says why; `indexer_edit` fixes the address, credentials or proxy tags, or retires it with `enable=false`.

## audit_unreliable

Indexers that fail too often over a window: the share of queries, RSS syncs, logins and grabs that failed, worst first, split by kind - failing logins mean credentials or a cookie, failing grabs a download limit or a dead link, failing queries a site that is down or behind Cloudflare.

**Fix with:** `indexer_test`, then `indexer_edit`; a site behind Cloudflare needs a proxy (`audit_proxies`).

## audit_slow

Indexers whose queries average longer than a threshold (5 seconds by default), slowest first, and whose grabs take twice that: every search waits for them.

**Fix with:** `indexer_edit enable=false` for one not worth the wait, or `indexer_edit redirect=true` where grabs are the slow part.

## audit_unused

Enabled indexers that earn nothing: never queried at all (no application receives them, or none searches them), or queried and never grabbed from.

**Fix with:** `indexer_edit enable=false`, `indexer_delete`, or a sync profile without automatic search (`profile_create`).

## audit_limits

Indexers at or near a query or grab limit, and how much of the current window they have already used.

**Fix with:** `indexer_edit query_limit` or `grab_limit` to what the site allows, or a sync profile without RSS to spend fewer.

## audit_unsynced

What the syncing leaves out: enabled indexers no application receives, each application's reason (it lacks their tag, syncs none of their categories, needs a kind of search they do not offer, or its sync is off), applications receiving nothing at all, and indexers whose profile turns off RSS and both searches.

**Fix with:** `indexer_edit add_tags` or `app_edit`, then `app_sync`.

## audit_apps

Applications that are misconfigured: failing, sync switched off, told to reach Prowlarr at `localhost` while running elsewhere (every indexer they receive then points at themselves), added twice, or syncing no categories.

**Fix with:** `app_test` says why; `app_edit` fixes the address, key, sync level or categories; `app_delete` drops a duplicate.

## audit_proxies

Indexers behind Cloudflare with no FlareSolverr proxy sharing a tag (every query fails), proxies used by nothing, and proxies failing.

**Fix with:** `indexer_edit add_tags` with the proxy's tag, `proxy_edit`, or `proxy_delete`.

## audit_download_clients

Indexers pinned to a download client that is gone or disabled, a usenet blackhole that can never take a usenet grab (Prowlarr always redirects those), and clients failing.

**Fix with:** `indexer_edit download_client` to pin another, or `downloadclient_edit`; `downloadclient_test` says why one fails.

## audit_duplicates

The same tracker added twice: two indexers from one definition, or two generic Newznab or Torznab indexers pointed at the same address.

**Fix with:** `indexer_delete` on the one to drop.

## audit_seeding

Private and semi-private torrent indexers with no seed goal: the applications tell their download client nothing, so it may stop seeding before the tracker's minimum - which private trackers punish as hit-and-run.

**Fix with:** `indexer_bulk_edit` with `seed_ratio` and `seed_time`.

## audit_vip

Paid memberships that have run out, or run out within a number of days, from the VIP expiry recorded on the indexer: after it many sites cut the API the applications rely on.

**Fix with:** renew it, then `indexer_edit vip_expiration`; or `indexer_edit enable=false`.

## audit_definitions

Indexers their definition has left behind: the definition is gone from the catalogue or marked obsolete, the address is one the site has moved away from or one the definition does not list at all (a mirror, or a typo), or the definition carries a warning.

**Fix with:** `indexer_catalog` and `indexer_add` for a replacement, then `indexer_delete`; or `indexer_edit base_url`.

## audit_tags

Tags carried by nothing, and tags carried by an application that no indexer shares - which leaves that application with nothing through it.

**Fix with:** `tag_delete`, or `indexer_edit add_tags` to put the tag to use.

## audit_profiles

Sync profiles that do nothing: RSS, automatic and interactive search all off, and profiles no indexer uses.

**Fix with:** `profile_edit` to turn a search on, `indexer_bulk_edit sync_profile` to move its indexers, or `profile_delete`.

## audit_health

Prowlarr's own health checks as a worklist, errors first, each with its wiki link and the audit or tool that deals with it.

**Fix with:** the audit or tool each finding names.

## audit_notifications

Nobody is told when something breaks: no notification sent for a health issue, one sent for nothing, one sent for health issues without the warnings (which most of Prowlarr's checks are), and with `test=true` one that fails its test.

**Fix with:** `notification_add` or `notification_edit` with `on=health_issue`.

## audit_backups

No way back: no backup at all, or the newest older than the schedule should allow, which means the scheduled backup is not running.

**Fix with:** `backup_create` now, and `task_run Backup` to see the scheduled one run.

## The principle

**Detection is code, correction is judgment.** The server runs cheap, fixed checks over the whole setup and produces worklists; the AI reasons only about what they find. Every answer is trimmed to what a decision needs, never the raw record. A credential is never shown, whatever a tool returns: an indexer's key or cookie, an application's key and a webhook's address are named as set, and an address is shown without the values after its `?`.
