# Changelog

## 0.1.0 (2026-09-19)

First release: an MCP server, CLI and Go SDK for Prowlarr.

- **17 audits.** Indexers failing now, failing too often, too slow, or earning
  nothing; indexers no application receives, and the reason each application
  passes them over; applications, proxies and download clients that are
  misconfigured; trackers added twice; private trackers with no seed goal;
  memberships and request limits running out; definitions the site has left
  behind; tags and sync profiles that do nothing; and Prowlarr's own health
  checks as a worklist. `audit_all` runs every one over a single read and
  reports the counts.
- **59 tools** (64 with `--enable-delete`), in toolsets: browse, search and
  add indexers from the catalogue, edit one or many, test them, search and
  grab, read history and per-indexer statistics, wire up applications, sync
  profiles, proxies, download clients and tags, run tasks and read logs. The
  default `core` set costs about a thousand tokens of context.
- **`lib/prowlarr`**, a generated Go client covering all 122 operations of the
  Prowlarr API, with typed options and bodies, the status codes each operation
  really answers, and `Complete` pagers for the history and the log. Generated
  by `internal/pandorest` from Prowlarr's own OpenAPI document through eight
  named workarounds for the document's bugs.
- Tested against a real Prowlarr in Docker with no internet at all: fake
  Newznab and Torznab sites (real feeds, real `.nzb` files, valid torrents,
  able to fail, stall or refuse a key) and fake Sonarr, Radarr, Lidarr and
  Readarr instances that record what Prowlarr syncs into them. The suite fails
  if a registered tool has no test.
