# prowlarr-mcp - a Prowlarr MCP server, CLI and Go SDK

[![GitHub release](https://img.shields.io/github/v/release/katbyte/prowlarr-mcp?color=blueviolet)](https://github.com/katbyte/prowlarr-mcp/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/prowlarr-mcp?color=00ADD8)](https://github.com/katbyte/prowlarr-mcp/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/prowlarr-mcp?color=blue)](https://github.com/katbyte/prowlarr-mcp/blob/main/LICENSE)
![build](https://github.com/katbyte/prowlarr-mcp/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/prowlarr-mcp/actions/workflows/pr-tests.yaml/badge.svg)
![lint](https://github.com/katbyte/prowlarr-mcp/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/prowlarr-mcp/badges/coverage.json)](https://github.com/katbyte/prowlarr-mcp/actions/workflows/coverage.yaml)

An [MCP](https://modelcontextprotocol.io) server, command-line tool and Go client that checks a [Prowlarr](https://prowlarr.com) setup for what has quietly gone wrong, and fixes it, from Claude Code or any other MCP client.

Prowlarr rarely fails loudly. An indexer gets disabled, a membership lapses, a tracker never reaches Sonarr, and searches just find less.

**Detection is code, correction is judgment.** The server runs fixed checks over the whole setup and returns worklists. The AI reasons only about what they find. A credential is never shown.

## Quick start

```bash
go install github.com/katbyte/prowlarr-mcp@latest
claude mcp add prowlarr -e PROWLARR_SERVER=http://nas:9696 -e PROWLARR_TOKEN=... -e PROWLARR_TOOLSETS=curation -- prowlarr-mcp serve
```

The API key is in Prowlarr under Settings → General → Security. Then ask Claude to run `audit_all`.

`prowlarr-mcp info` checks the connection on its own. Tested against Prowlarr 2.6.5.

## The audits

There are 19. Each finds one kind of problem and names the tool that fixes it. The full rules for each are in [docs/audits.md](docs/audits.md).

| Audit | What it finds, and the fix |
|---|---|
| [`audit_all`](docs/audits.md#audit_all) | Every audit in one call, counts only. Start here. |
| [`audit_failing`](docs/audits.md#audit_failing) | Indexers failing right now. `indexer_test` says why; `indexer_edit` fixes them. |
| [`audit_unreliable`](docs/audits.md#audit_unreliable) | Indexers that fail too often. Fix with `indexer_test` and `indexer_edit`. |
| [`audit_slow`](docs/audits.md#audit_slow) | Indexers that slow every search down. Disable with `indexer_edit`. |
| [`audit_unused`](docs/audits.md#audit_unused) | Indexers never searched or never grabbed from. Disable with `indexer_edit` or remove with `indexer_delete`. |
| [`audit_limits`](docs/audits.md#audit_limits) | Indexers close to a query or grab limit. Raise it with `indexer_edit`. |
| [`audit_unsynced`](docs/audits.md#audit_unsynced) | Indexers no application receives, and why. Fix the tag with `indexer_edit`, then `app_sync`. |
| [`audit_apps`](docs/audits.md#audit_apps) | Applications failing or set up wrongly. Fix with `app_edit`. |
| [`audit_proxies`](docs/audits.md#audit_proxies) | Cloudflare sites with no solver, and unused or failing proxies. Fix with `indexer_edit` or `proxy_edit`. |
| [`audit_download_clients`](docs/audits.md#audit_download_clients) | Indexers tied to a missing or failing download client. Fix with `indexer_edit` or `downloadclient_edit`. |
| [`audit_duplicates`](docs/audits.md#audit_duplicates) | The same tracker added twice. Remove one with `indexer_delete`. |
| [`audit_seeding`](docs/audits.md#audit_seeding) | Private trackers with no seed ratio or time. Set them with `indexer_bulk_edit`. |
| [`audit_vip`](docs/audits.md#audit_vip) | Paid memberships expired or about to. Renew, then `indexer_edit`. |
| [`audit_definitions`](docs/audits.md#audit_definitions) | Indexers on a retired definition or address. Replace with `indexer_catalog` and `indexer_add`. |
| [`audit_tags`](docs/audits.md#audit_tags) | Tags that do nothing. Remove with `tag_delete`. |
| [`audit_profiles`](docs/audits.md#audit_profiles) | Sync profiles that do nothing. Fix with `profile_edit` or remove with `profile_delete`. |
| [`audit_health`](docs/audits.md#audit_health) | Prowlarr's own health checks, errors first, each naming its fix. |
| [`audit_notifications`](docs/audits.md#audit_notifications) | Nobody is told when something breaks. Fix with `notification_add` or `notification_edit`. |
| [`audit_backups`](docs/audits.md#audit_backups) | No backup, or an overdue one. Make one with `backup_create`. |

## The tools

There are 74 tools, 82 with the ones that delete. They cover indexers, applications, sync profiles, proxies, download clients, notifications, tags, history, backups, tasks and Prowlarr's own settings.

- **Names work everywhere.** A tool takes a thing's name or its id.
- **Lists page the same way,** with `limit` and `offset`.
- **A delete asks first.** Without `confirm` nothing changes, and the answer says what would be lost.
- **The default is small.** Six read-only tools load unless `PROWLARR_TOOLSETS` asks for more. `curation` loads every audit and what fixes its findings; `all` loads everything.

The full list, the toolsets and how to allow or deny single tools are in [docs/tools.md](docs/tools.md).

## Configuration

Two settings are needed: `PROWLARR_SERVER` and `PROWLARR_TOKEN`. Set them as flags, environment variables, or in `~/.prowlarr-mcp`:

```env
SERVER=http://nas:9696
TOKEN=...
TOOLSETS=curation
```

Every setting, serving over HTTP, Docker and running two Prowlarrs are in [docs/configuration.md](docs/configuration.md).

## The Go client

`sdk/prowlarr` is a typed Go client for the whole Prowlarr API. It works on its own, with no MCP involved.

```go
c, err := prowlarr.New("http://nas:9696", os.Getenv("PROWLARR_TOKEN"))
res, err := c.GetSearch(ctx, prowlarr.GetSearchOptions{Query: "dune", Type: "search", Limit: new(50)})
```

More on it, and on building and testing, is in [docs/development.md](docs/development.md). How it is generated from Prowlarr's API description is in [docs/README.md](docs/README.md).

## How this is built

I set the design, the scope and the audit rules, and decide what stays. The rest is held to account by tests and checks:

- Every tool, and every operation of the Go client, must have a live test against a real Prowlarr in Docker. CI fails otherwise.
- CI also runs the unit tests, the linter (golangci-lint), CodeQL, two vulnerability scans (govulncheck on the code, Trivy on the built binary), and a check that vendored code and generated code are current.
- Shell scripts, workflows, spelling and markdown each have their own check.

## More

- [docs/ROADMAP.md](docs/ROADMAP.md): the design rules behind the tools.
- [CONTRIBUTING.md](CONTRIBUTING.md): how releases are cut.
- [CHANGELOG.md](CHANGELOG.md): what changed in each release.
