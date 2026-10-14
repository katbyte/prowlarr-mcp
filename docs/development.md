# Development

## The Go client

`sdk/prowlarr` is a complete Go client for the Prowlarr API, built on the base client [pandorest](https://github.com/katbyte/pandorest) shares between its SDKs and on go-kt's HTTP package, and knows nothing of MCP. If you only want to talk to Prowlarr from Go, take the package and ignore the rest:

```go
import "github.com/katbyte/prowlarr-mcp/sdk/prowlarr"

c, err := prowlarr.New("http://nas:9696", os.Getenv("PROWLARR_TOKEN"))
res, err := c.GetSearch(ctx, prowlarr.GetSearchOptions{
    Query: "dune", Type: "search", Categories: []int{2000}, Limit: new(50),
})
for _, r := range res.Answer { ... }

all, err := c.GetHistoryComplete(ctx, prowlarr.GetHistoryOptions{PageSize: new(250)}) // every page
```

A number among the options is a pointer, so that 0 can be asked for; `new(50)` is Go 1.26's way to write one. Options that are all optional can be left out, and an operation with a single option takes it as a plain argument: `c.PutIndexerById(ctx, id, body, true)` saves without asking Prowlarr to test first. A read is asked again when a gateway could not reach Prowlarr or the connection dropped, three times in all, and a write is sent once; a redirect is refused rather than followed; and `prowlarr.New(url, key, client.WithLog(logger))` traces every request and answer with the API key, every password and token, and the text of every provider's setting blanked: a tracker's key or cookie is one of those settings, so a trace shows which were set and not what to.

It is generated from Prowlarr's own OpenAPI document (`api-defs/`, see [README.md](README.md)) by [pandorest](https://github.com/katbyte/pandorest), a generator modelled on [hashicorp/pandora](https://github.com/hashicorp/pandora) and shared with sonarr-mcp, radarr-mcp and embyfin-mcp: an importer normalises the spec into checked-in definitions (`api-defs/prowlarr-<release>/`, one file per tag) through named workarounds for the spec's known bugs, a differ reports what a spec refresh changes, and a generator writes one file per operation and model from the definitions. What is this repository's own is in `sdk/pandorest`: the service's settings and the workarounds for its document. That is **a method for every one of Prowlarr's 122 operations**, each with typed options, a typed body, an `{Answer, HttpResponse}` result, the status codes the operation documents (anything else is an error), and a `Complete` pager on every paged list. Dates are times, the ids of indexers, sync profiles, tags and download clients each have a type of their own so one cannot be passed for another, and the commands this repository queues (a sync, a backup, the health checks) each have a method that can wait for the command to finish. `make apicheck` proves the coverage claim against the spec, `make gencheck` (and the unit tests) fail when the generated code is stale, and the client suite in `acceptance/prowlarr` proves the shapes **against a running Prowlarr** - which is the only thing that catches the server changing shape underneath a spec that says otherwise.

## Building and checking

```bash
make            # fmt + build
make check-all  # build + unit tests + both live suites (needs docker) + every linter
```

## Tests

`make test` is hermetic and fast. It covers the pure logic - tool registration and toolsets, the audit heuristics, the CLI's flags, config files and HTTP auth, the fake Newznab and Servarr servers the live suites run on - and, against a canned Prowlarr, the requests the base client and the generated client build and the answers they decode. It also re-imports the spec and regenerates the SDK to check the checked-in code is current, and applies every importer workaround twice to prove each one notices when its bug is fixed.

Everything else runs against **a real Prowlarr in Docker**, because a stub can only confirm what you already believed. Two suites, each in its own container:

| | Covers | Command |
|---|---|---|
| `acceptance/prowlarr/` | the `sdk/prowlarr` client: bespoke tests that the calls the tools rely on decode with their fields populated and do what they say (creates answer 201 and updates 202, a failed "test all" answers 400 with every result, the settings sections round-trip, the feed an application reads, paging and the `Complete` pager), and a sweep that calls every GET in the document against the server and classifies the two that cannot answer in a container | `make testacc-sdk` |
| `acceptance/tools/` | the tools: name resolution, projections, audits, and journeys that chain them (a setup made from nothing with the tools alone, the two mistakes a first setup makes found by the audits and fixed with the tools they name, and everything taken down again until Prowlarr is as it was; an indexer added from the catalogue, synced into applications and grabbed from; every audit given something real to find, and then - for every one a tool can fix - the fix applied and the audit run again to prove the finding is gone; the built binary itself over stdio and HTTP - flags and environment reaching the server, nothing but protocol on stdout, the bearer check, clean shutdown, refusing to start without a token) | `make testacc-tools` |

```bash
make testacc            # both suites, each in a throwaway container
make check-all          # build + unit + live suites + every linter
make cover              # every suite merged into one coverage number
```

Coverage has to span every suite or it lies: `go test -cover ./...` reports a fraction for `tools/`, because almost everything real happens in the live suites behind the `acceptance` tag. `make cover` runs each into its own binary coverage directory and merges them with `go tool covdata` - stdlib tooling, no third-party merger - which is what the badge reports. The generated `sdk/prowlarr` is left out of the number and reported on a line of its own, since it is one mechanical method per operation.

**Every operation of the client is exercised.** The client suite records every call it makes and fails a whole run if any operation in the definitions went uncalled.

**Every tool is exercised.** Tool coverage is enforced rather than claimed: the tool suite records every tool it calls and fails if the server registered one that nothing saw answer, whether it was never called or only ever refused, so a new tool cannot ship untested.

The environment is built by `.scripts/testenv.sh`: a Prowlarr container with **no internet at all**, seeded with three real tracker definitions so the catalogue has something in it, and a known API key. The indexers, applications, profiles, proxies, download clients and tags are the suites' own job, through `indexer_add`, `app_add` and the rest, so those tools are exercised rather than bypassed. What Prowlarr talks to are fakes the suites run on the host (`internal/fakes`): Newznab and Torznab sites serving a real RSS feed, real `.nzb` files and valid bencoded torrents, which can be told to fail, to answer slowly, or to refuse their key; and Sonarr, Radarr, Lidarr and Readarr instances that record what Prowlarr syncs into them.

Everything else Prowlarr reaches for goes to a proxy the suites run (go-kt's `test/replayproxy`, whose certificates the container is made to trust). Through it the suites play Prowlarr's own services: its update server, the clock it checks its own against and its notices, so its health checks run the same on any day and `server_updates` has releases to report; and its definitions service, which is down, so it reads the definitions seeded on its disk. Nothing is recorded, and a request to anywhere else fails the run, naming it. Requires docker, curl and openssl; the suites skip when `PROWLARR_SERVER` and `PROWLARR_TOKEN` are unset, so they never fail for want of a daemon.

```bash
eval "$(.scripts/testenv.sh up)"   # start it and export the environment
.scripts/testenv.sh logs           # what Prowlarr wrote, for a failing run
.scripts/testenv.sh down           # remove it
```

Dev tools are pinned in `.tools/go.mod` and built into `.tools/bin` by make. On a noexec checkout point `TOOLS_BIN` somewhere local, e.g. `make TOOLS_BIN=~/.cache/prowlarr-mcp/bin lint`.
