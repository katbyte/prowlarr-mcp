# prowlarr-mcp - a Prowlarr MCP server, CLI and Go SDK

[![GitHub release](https://img.shields.io/github/v/release/katbyte/prowlarr-mcp?color=blueviolet)](https://github.com/katbyte/prowlarr-mcp/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/prowlarr-mcp?color=00ADD8)](https://github.com/katbyte/prowlarr-mcp/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/prowlarr-mcp?color=blue)](https://github.com/katbyte/prowlarr-mcp/blob/main/LICENSE)
![build](https://github.com/katbyte/prowlarr-mcp/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/prowlarr-mcp/actions/workflows/pr-integration.yaml/badge.svg)
![lint](https://github.com/katbyte/prowlarr-mcp/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/prowlarr-mcp/badges/coverage.json)](https://github.com/katbyte/prowlarr-mcp/actions/workflows/coverage.yaml)

An [MCP](https://modelcontextprotocol.io) server, CLI and Go SDK that **audit a
[Prowlarr](https://prowlarr.com) setup for the things that actually go wrong, and fix what
they find** - from Claude Code, Claude Desktop, or any other MCP client.

Prowlarr is the piece nobody looks at until a search comes back empty: it holds the
indexers, keeps their definitions current, and pushes them into Sonarr, Radarr and the rest.
When it quietly stops - a tracker disabled after failures, an indexer no application ever
receives, a definition the site has outgrown, a VIP membership that lapsed - nothing breaks
loudly. Searches just find less, and the only sign is a download that never appears.

This server wraps Prowlarr's API, but the reason it exists is the layer above: **17 audits**,
each a sweep for one specific thing that goes wrong in a real setup, returning a worklist
rather than a dump, and naming the tool that fixes it.

### The audits

| audit | what it catches |
|---|---|
| `audit_all` | every audit in one call, counts only, so one call says where a setup needs work - start here |
| `audit_failing` | indexers failing now: the ones Prowlarr has stopped using after failures (how long they have been failing, and until when they are held back), and enabled ones whose last query or grab failed |
| `audit_unreliable` | indexers that fail too often over a window: the share of queries, RSS syncs, logins and grabs that failed, worst first, split by kind - failing logins mean credentials or a cookie, failing grabs a download limit or a dead link, failing queries a site that is down or behind Cloudflare |
| `audit_slow` | indexers whose queries average longer than a threshold (5 seconds by default), slowest first, and whose grabs take twice that: every search waits for them |
| `audit_unused` | enabled indexers that earn nothing: never queried at all (no application receives them, or none searches them), or queried and never grabbed from |
| `audit_limits` | indexers at or near a query or grab limit, and how much of the current window they have already used |
| `audit_unsynced` | what the syncing leaves out: enabled indexers no application receives, each application's reason (it lacks their tag, syncs none of their categories, needs a kind of search they do not offer, or its sync is off), applications receiving nothing at all, and indexers whose profile turns off RSS and both searches |
| `audit_apps` | applications that are misconfigured: failing, sync switched off, told to reach Prowlarr at `localhost` while running elsewhere (every indexer they receive then points at themselves), added twice, or syncing no categories |
| `audit_proxies` | indexers behind Cloudflare with no FlareSolverr proxy sharing a tag (every query fails), proxies used by nothing, and proxies failing |
| `audit_download_clients` | indexers pinned to a download client that is gone or disabled, a usenet blackhole that can never take a usenet grab (Prowlarr always redirects those), and clients failing |
| `audit_duplicates` | the same tracker added twice: two indexers from one definition, or two generic Newznab or Torznab indexers pointed at the same address |
| `audit_seeding` | private and semi-private torrent indexers with no seed goal: the applications tell their download client nothing, so it may stop seeding before the tracker's minimum - which private trackers punish as hit-and-run |
| `audit_vip` | paid memberships that have run out, or run out within a number of days, from the VIP expiry recorded on the indexer: after it many sites cut the API the applications rely on |
| `audit_definitions` | indexers their definition has left behind: the definition is gone from the catalogue or marked obsolete, the address is one the site has moved away from or one the definition does not list at all (a mirror, or a typo), or the definition carries a warning |
| `audit_tags` | tags carried by nothing, and tags carried by an application that no indexer shares - which leaves that application with nothing through it |
| `audit_profiles` | sync profiles that do nothing: RSS, automatic and interactive search all off, and profiles no indexer uses |
| `audit_health` | Prowlarr's own health checks as a worklist, errors first, each with its wiki link and the audit or tool that deals with it |

The design principle: **detection is code, correction is judgment.** The server runs cheap
deterministic checks over the whole setup and produces worklists; the AI reasons only about
the anomalies. Every response is a trimmed projection of what a decision needs, never the raw
API object - and an indexer's credentials are named, never shown, whatever a tool returns.

### What else is in the box

- **59 tools, in toolsets.** Browse and search the indexers, add one from the catalogue of
  definitions Prowlarr ships, edit one or many, test them, search and grab through them, read
  the history and the per-indexer statistics, wire up applications, sync profiles, proxies,
  download clients and tags, run tasks and read logs. Each sits in a toolset a session can
  load on its own, so a client spends about a thousand tokens of context by default rather
  than ten thousand.
- **A Go SDK.** `lib/prowlarr` is a complete typed client for the Prowlarr API - all 122
  operations, generated from Prowlarr's own OpenAPI document, standard library only, no
  knowledge of MCP. Useful on its own, whether or not you care about AI.
- **Tested against a real Prowlarr.** Every tool runs against Prowlarr 2.6 in Docker, with
  fake Newznab and Torznab sites and fake Sonarr, Radarr and Lidarr instances that can be
  made to fail, answer slowly or refuse their key. The suite fails if a registered tool has
  no test, and no test needs a network.

## Installation

```bash
go install github.com/katbyte/prowlarr-mcp@latest
```

Tested against Prowlarr 2.6.5, whose API document the SDK is generated from.

## Configuration

All options can be passed as command-line flags, environment variables, or via a configuration file.

| Variable | Flag | Description |
|---|---|---|
| `PROWLARR_SERVER` | `--server`, `-s` | Prowlarr URL, e.g. `http://nas:9696` |
| `PROWLARR_TOKEN` | `--token`, `-t` | API key (Settings → General → Security → API Key) |
| `PROWLARR_READ_ONLY` | `--read-only` | register only tools that never change Prowlarr's state |
| `PROWLARR_ENABLE_DELETE` | `--enable-delete` | register `indexer_delete`, `app_delete`, `proxy_delete`, `downloadclient_delete` and `profile_delete` |
| `PROWLARR_TOOLSETS` | `--toolsets` | groups of tools to register, default `core`: `all`, `core`, `curation`, `setup`, `grab`, `admin`, or a resource family like `indexer` (`core` is always included) |
| `PROWLARR_ALLOW_TOOLS` | `--allow-tools` | only register these tools (names, `indexer_*` globs, or `essential`) |
| `PROWLARR_DENY_TOOLS` | `--deny-tools` | never register these tools (names or globs such as `*_delete`) |
| `PROWLARR_LOG` | | log level (`WARN` default; `DEBUG`, `TRACE`, ...) |
| `PROWLARR_LISTEN` | `--listen` | serve MCP over HTTP on this address (e.g. `:8080`) instead of stdio |
| `PROWLARR_AUTH_TOKEN` | `--auth-token` | bearer token required on the HTTP endpoint (required with `--listen`) |
| `PROWLARR_ALLOW_NO_AUTH` | `--allow-no-auth` | serve HTTP with no bearer token at all: anyone who can reach the port can use every tool |

### Configuration File

You can place a `.prowlarr-mcp` file in your home directory `~/.prowlarr-mcp` (for global settings)
or in your current directory `./.prowlarr-mcp` (for per-project settings). Keys match the long flag
names using the `env` format:

```env
SERVER=http://nas:9696
TOKEN=...
TOOLSETS=curation
```

## Usage

Quick connectivity check:

```bash
prowlarr-mcp info
```

### Register with Claude Code

`.mcp.json`:

```json
{
  "mcpServers": {
    "prowlarr": {
      "command": "prowlarr-mcp",
      "args": ["serve"],
      "env": {
        "PROWLARR_SERVER": "http://nas:9696",
        "PROWLARR_TOKEN": "...",
        "PROWLARR_TOOLSETS": "curation"
      }
    }
  }
}
```

Or from the shell:

```bash
claude mcp add prowlarr -e PROWLARR_SERVER=http://nas:9696 -e PROWLARR_TOKEN=... -- prowlarr-mcp serve
```

### Run as a service (HTTP transport)

`serve --listen :8080` serves the MCP Streamable HTTP transport at `/mcp` (plus `GET /healthz`)
instead of stdio. `PROWLARR_AUTH_TOKEN` is required: clients must send `Authorization: Bearer
<token>`, and the server refuses to start without one unless `PROWLARR_ALLOW_NO_AUTH=true` says
that anyone who can reach the port may use every tool. Register it from any machine:

```bash
claude mcp add --transport http prowlarr http://nas:8080/mcp \
  --header "Authorization: Bearer $PROWLARR_AUTH_TOKEN"
```

### Docker

Releases publish a multi-arch (amd64, arm64) image to `ghcr.io/katbyte/prowlarr-mcp`, tagged
`vX.Y.Z`, `vX.Y` and `latest`. `docker-compose.yml` is the default always-on deployment: it runs
that image and reads secrets from a gitignored `.env` (copy `.env.example`). Adjust
`PROWLARR_SERVER` and `TZ` in the compose file, then:

```bash
cp .env.example .env      # fill in PROWLARR_TOKEN and PROWLARR_AUTH_TOKEN
docker compose up -d
```

`make docker` builds the same image from source, tagged `prowlarr-mcp`, with version info from
git. The image is alpine-based (so `docker exec -it prowlarr-mcp sh` works), runs as a non-root
user and has a healthcheck against `/healthz`. The binary is the entrypoint, so `docker run --rm
ghcr.io/katbyte/prowlarr-mcp info` works as a connectivity check with the `PROWLARR_*` variables
passed via `-e`.

## MCP Tools

Tools are named resource-first (`indexer_*`, `app_*`, `audit_*`, `server_*`...) so they group
by what they act on. Every tool carries MCP annotations (read-only or destructive) and tools
that change Prowlarr's state say so in their descriptions. Wherever a tool takes an indexer,
application, profile, proxy, download client or tag it accepts a name as well as an id, and an
unknown name comes back as an error listing what exists. Anything that reads history or
statistics takes a window in days.

| Resource | Tools |
|---|---|
| server | `server_info`, `server_health` (what each check means and what fixes it), `server_logs`, `server_log`, `server_updates`, `backup_list` |
| tasks | `task_list`, `task_run` (waits for it, or queues it with `wait=-1`) |
| indexers | `indexer_list`, `indexer_get` (settings, categories, searches, which applications receive it and why not, failures, 30 days of statistics, last ten events), `indexer_catalog` (the definitions Prowlarr ships, searchable), `indexer_add`, `indexer_edit`, `indexer_bulk_edit` (priority, profile, tags, seed goals across many), `indexer_test`, `indexer_delete`, `indexer_stats`, `category_list` |
| audits | the 17 audits in [the table above](#the-audits) |
| releases | `release_search` (as an application would, by words and kind, narrowed to categories, indexers or protocol), `release_grab` (sends it to a download client) |
| applications | `app_list`, `app_get` (what it receives, what is held back, what it skips), `app_add`, `app_edit`, `app_test`, `app_sync`, `app_delete` |
| profiles | `profile_list`, `profile_create`, `profile_edit`, `profile_delete` |
| proxies | `proxy_list`, `proxy_add`, `proxy_edit`, `proxy_test`, `proxy_delete` |
| download clients | `downloadclient_list`, `downloadclient_add`, `downloadclient_edit`, `downloadclient_test`, `downloadclient_delete` |
| notifications | `notification_list`, `notification_test` |
| tags | `tag_list` (with what carries each one), `tag_create`, `tag_delete` |
| history | `history_list` (queries, RSS syncs, logins and grabs, with elapsed time and result counts; keys in URLs redacted) |

`indexer_delete`, `app_delete`, `proxy_delete`, `downloadclient_delete` and `profile_delete`
are only registered when `--enable-delete` / `PROWLARR_ENABLE_DELETE` is set. `--read-only`
registers the 43 read tools and nothing else, so a write tool is absent from `tools/list`
rather than refused when called.

### Choosing which tools load

**The default is `core`: six read-only tools, about 1,000 tokens.** The whole surface is
around 9,800 tokens of tool definitions before anyone asks a question, which is a poor way to
spend a client's context by default. `--toolsets` / `PROWLARR_TOOLSETS` loads the groups a
session actually needs, and `core` comes along with whatever else is asked for, because
nothing else can find an indexer or read the history.

**Auditing a setup needs `PROWLARR_TOOLSETS=curation`** - every audit, and everything that
fixes what they find. `PROWLARR_TOOLSETS=all` restores every tool.

| toolset | tools | with core | ~tokens |
|---|---|---|---|
| `core` *(default)* | 6 | 6 | 1,000 |
| `grab` | 4 | 10 | 1,600 |
| `admin` | 8 (13 with delete) | 14 | 1,700 |
| `setup` | 8 | 14 | 3,000 |
| `curation` | 33 | 39 | 6,700 |
| `all` | 59 | 59 | 9,800 |

Tokens are what the model sees: each tool's name, description and input schema, measured over
a real `tools/list` at four bytes a token. Every tool also carries an output schema, another
14,000 tokens across `all`, but clients keep that to themselves to validate results rather
than sending it to the model.

`--toolsets` also takes a resource family - `indexer`, `app`, `audit`, `release`, `profile`,
`proxy`, `downloadclient`, `notification`, `tag`, `history`, `category`, `server`, `task`,
`backup` - which is every tool with that prefix:

```sh
PROWLARR_TOOLSETS=all                 # every tool
PROWLARR_TOOLSETS=curation            # the audits plus everything that fixes what they find
PROWLARR_TOOLSETS=setup               # adding indexers, applications, profiles and proxies
PROWLARR_TOOLSETS=audit               # read-only detection, nothing that writes
PROWLARR_TOOLSETS=core,indexer,app    # core plus two whole families
```

`prowlarr-mcp tools` prints what the current flags would register, grouped by toolset, and
needs no server:

```sh
prowlarr-mcp tools                    # the default set
prowlarr-mcp tools --toolsets all     # every tool
prowlarr-mcp tools --read-only -q     # names only
```

### Narrowing further

`--allow-tools` and `--deny-tools` narrow whatever the toolsets left, and take comma-separated
tool names, globs with a leading or trailing `*`, or the `essential` preset (`indexer_list`,
`release_search`, `audit_all`, `indexer_test`, `history_list`):

```sh
PROWLARR_ALLOW_TOOLS=essential
PROWLARR_ALLOW_TOOLS=indexer_*,app_list,audit_*
PROWLARR_DENY_TOOLS=*_delete,release_grab
```

A pattern that matches no tool aborts startup and names it, so a typo cannot silently hide a
tool.

### A typical audit session

1. `audit_all` says where the setup needs work, in counts.
2. `audit_failing` and `audit_unreliable` name the indexers that are broken now;
   `indexer_test` says why, and `indexer_edit` fixes credentials, cookies or the address.
3. `audit_unsynced` says what the applications never receive, and why: `indexer_edit` or
   `app_edit` puts the tag, category or profile right, then `app_sync` pushes it.
4. `audit_definitions` finds the indexers their definition has outgrown; `indexer_catalog`
   and `indexer_add` replace them.
5. `audit_vip` and `audit_limits` say what is about to stop working.
6. `audit_seeding` finds the private trackers with no seed goal; `indexer_bulk_edit` sets one
   on all of them at once.
7. `audit_unused` and `audit_slow` say what to drop: every search waits for them.

## Using the client on its own

`lib/prowlarr` is a complete Go client for the Prowlarr API that depends on nothing but the
standard library, the base client in `lib/client` and `go-kt/version`, and knows nothing of
MCP. If you only want to talk to Prowlarr from Go, take the package and ignore the rest:

```go
import "github.com/katbyte/prowlarr-mcp/lib/prowlarr"

c, err := prowlarr.New("http://nas:9696", os.Getenv("PROWLARR_TOKEN"))
res, err := c.GetSearch(ctx, prowlarr.GetSearchOperationOptions{
    Query: "dune", Type: "search", Categories: []int{2000}, Limit: 50,
})
for _, r := range res.Model { ... }

all, err := c.GetHistoryComplete(ctx, prowlarr.GetHistoryOperationOptions{PageSize: 250}) // every page
```

It is generated from Prowlarr's own OpenAPI document (`docs/`, see
[docs/README.md](docs/README.md)) by `internal/pandorest`, a generator kept in this
repository and modelled on [hashicorp/pandora](https://github.com/hashicorp/pandora): an
importer normalises the spec into checked-in definitions (`api-definitions/`, one file per
tag) through named workarounds for the spec's known bugs, a differ reports what a spec
refresh changes, and a generator writes one file per operation and model from the
definitions. That is **a method for every one of Prowlarr's 122 operations**, each with typed
options, a typed body, a `{Model, HttpResponse}` result, the status codes the operation
documents (anything else is an error), and a `Complete` pager on every paged list.
`make apicheck` proves the coverage claim against the spec, `make gencheck` (and the unit
tests) fail when the generated code is stale, and the integration suite proves the shapes
**against a running Prowlarr** - which is the only thing that catches the server changing
shape underneath a spec that says otherwise. See
[internal/pandorest/README.md](internal/pandorest/README.md).

## Development

```bash
make            # fmt + build
make check-all  # build + unit tests + both live suites (needs docker) + every linter
```

### Tests

`make test` is hermetic and fast. It covers the pure logic - tool registration and toolsets,
the audit heuristics, the CLI's flags, config files and HTTP auth, the fake Newznab and
Servarr servers the live suites run on - and, against a canned Prowlarr, the requests the
base client and the generated client build and the answers they decode. It also re-imports
the spec and regenerates the SDK to check the checked-in code is current, and applies every
importer workaround twice to prove each one notices when its bug is fixed.

Everything else runs against **a real Prowlarr in Docker**, because a stub can only confirm
what you already believed. Two suites, each in its own container:

| | Covers | Command |
|---|---|---|
| `integration/` | the `lib/prowlarr` client: bespoke tests that the calls the tools rely on decode with their fields populated and do what they say (creates answer 201 and updates 202, a failed "test all" answers 400 with every result, the settings sections round-trip, the feed an application reads, paging and the `Complete` pager), and a sweep that calls every GET in the document against the server and classifies the two that cannot answer in a container | `make testacc-integration` |
| `acceptance/` | the tools: name resolution, projections, audits, and journeys that chain them (an indexer added from the catalogue, synced into applications and grabbed from; every audit given something real to find, and then - for every one a tool can fix - the fix applied and the audit run again to prove the finding is gone; the built binary itself over stdio and HTTP - flags and environment reaching the server, nothing but protocol on stdout, the bearer check, clean shutdown, refusing to start without a token) | `make testacc-acceptance` |

```bash
make testacc            # both suites, each in a throwaway container
make check-all          # build + unit + live suites + every linter
make cover              # every suite merged into one coverage number
```

Coverage has to span every suite or it lies: `go test -cover ./...` reports a fraction for
`tools/`, because almost everything real happens in the live suites behind the `integration`
tag. `make cover` runs each into its own binary coverage directory and merges them with
`go tool covdata` - stdlib tooling, no third-party merger - which is what the badge reports.
The generated `lib/prowlarr` is left out of the number and reported on a line of its own: it
is one method per operation, and the suites exercise the ones the tools rely on rather than
all 122.

**Every tool is exercised.** Tool coverage is enforced rather than claimed: the acceptance
suite records every tool it calls and fails if the server registered one nothing called, so a
new tool cannot ship untested.

The environment is built by `scripts/testenv.sh`: a Prowlarr container with **no internet at
all** (its proxy variables point at a dead port), seeded with three real tracker definitions
so the catalogue has something in it, and a known API key. The indexers, applications,
profiles, proxies, download clients and tags are the suites' own job, through `indexer_add`,
`app_add` and the rest, so those tools are exercised rather than bypassed. What Prowlarr
talks to are fakes the suites run on the host (`internal/fakes`): Newznab and Torznab sites
serving a real RSS feed, real `.nzb` files and valid bencoded torrents, which can be told to
fail, to answer slowly, or to refuse their key; and Sonarr, Radarr, Lidarr and Readarr
instances that record what Prowlarr syncs into them. Requires docker and curl; the suites
skip when `PROWLARR_SERVER` and `PROWLARR_TOKEN` are unset, so they never fail for want of a
daemon.

```bash
eval "$(scripts/testenv.sh up)"   # start it and export the environment
scripts/testenv.sh logs           # what Prowlarr wrote, for a failing run
scripts/testenv.sh down           # remove it
```

Dev tools are pinned in `.tools/go.mod` (actionlint in `.tools/actionlint/go.mod`) and built
into `.tools/bin` by make. On a noexec checkout point `TOOLS_BIN` somewhere local, e.g.
`make TOOLS_BIN=~/.cache/prowlarr-mcp/bin lint`.
