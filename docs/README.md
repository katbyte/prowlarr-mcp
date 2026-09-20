# docs

## The API spec

Prowlarr publishes an OpenAPI document, vendored here as the reference for
`lib/prowlarr` - and it is a build input. `internal/pandorest` (see its
[README](../internal/pandorest/README.md)) imports it into checked-in
definitions under `api-definitions/prowlarr/`, fixing the document's known
bugs with named workarounds on the way, and generates the client from those
definitions. `make generate` runs both steps, `make pandorest-diff` reports
what a refreshed document would change, `make gencheck` (and the unit tests)
fail when the generated code is stale, and `make apicheck` proves every
operation in the spec has a method.

| File | Source | Version vendored |
|---|---|---|
| `prowlarr-openapi.json` | `src/Prowlarr.Api.V1/openapi.json` in [Prowlarr/Prowlarr](https://github.com/Prowlarr/Prowlarr), at the release tag | Prowlarr 2.6.5.5623 - 93 paths, 122 operations (the six web interface routes are dropped on import, and `HEAD /ping` is skipped) |

Prowlarr generates the document with Swashbuckle at build time and commits it,
so the copy in the repository at a tag is exactly what that release serves. A
running server does not serve it. To refresh: take the file from the new tag,
pretty-print it with `jq .` so the diff is readable, run `make pandorest-diff`
to see what changed in API terms (breaking changes are marked), then
`make generate` and review. A workaround whose bug the new document fixes
fails the import and names itself; delete it.

The spec is documentation of intent, not of behaviour: the live suites
(`integration/`, `acceptance/`) are what prove the shapes against a real
server, and the quirks they found are recorded below.

## Where the spec is wrong

These are shape bugs, fixed in the generated client by the importer's
workarounds (`internal/pandorest/importer/workarounds`, listed in
`api-definitions/prowlarr/Service.json`). Each checks its bug is still in the
document and fails the import once it is not.

- `prowlarr-ui-routes` - the document includes the web interface's own routes
  (the single page app, its static files, the forms login). They are not API,
  and are dropped rather than generated.
- `prowlarr-undeclared-responses` - Swashbuckle documents a `200` with no
  schema for everything that does not return a typed resource. A table says
  what each one really answers: the Newznab and Torznab feeds and the log
  files are files (`application/rss+xml`, `text/plain`), the route table is
  text, and the rest is JSON - the API version list, the localisation
  dictionary, the file system browser, the duplicate route report.
- `prowlarr-undeclared-write-responses` - the same for the writes whose action
  returns `object` or `IActionResult`: a provider action (`POST
  /api/v1/{kind}/action/{name}`, how the web interface asks a definition to
  fill in a setting) answers JSON of its own shape, so it is declared as raw
  JSON rather than nothing.
- `prowlarr-created-accepted` - every create answers `201 Created` and every
  update `202 Accepted`; the document says `200` for both. The generated
  methods would treat the real status as an error.
- `prowlarr-put-id-integer` - the `{id}` of every update is typed as a string,
  while the matching GET and DELETE type it as an integer.
- `prowlarr-test-all-results` - testing every provider of a kind (`POST
  /api/v1/indexer/testall` and friends) is documented as answering nothing. It
  answers the result for each provider - id, whether it passed, and the
  validation failures - as a `200` when all pass and a `400` when any fails.
  The client takes that `400` as an answer rather than an error, so one bad
  indexer does not hide the other results.
- `prowlarr-command-body` - `POST /api/v1/command` is documented as taking a
  `CommandResource`, whose fields are all read-only status. A command's body
  is its name plus whatever that command takes (`{"name": "AppIndexerSync",
  "indexerIds": [1,2]}`), so the body is raw JSON.
- `prowlarr-bulk-answer-lists` - the bulk update of every provider kind, and
  the bulk grab, are documented as answering one resource. They answer the
  list of everything they changed.

## Conventions worth knowing

- Auth is an API key in `X-Api-Key` (Settings → General → Security). The key
  is the server's, not a user's; there are no per-user anything in Prowlarr.
- Dates are RFC 3339 in UTC; the generated client keeps them as strings.
  Paged lists take `page`/`pageSize` and answer `{records, totalRecords}`,
  which is what the `Complete` pagers walk.
- A provider - indexer, application, download client, proxy, notification -
  is a name, an implementation and a list of `fields`, whose meaning comes
  from the provider's schema (`GET .../schema`). Reading one back masks its
  secrets as `********`, and sending that masked value back keeps the stored
  one. Which fields are secret is the schema's `privacy` (`apiKey`,
  `password`, `userName`) - except on Cardigann definitions, where a
  tracker's own credentials are marked `normal`, so the tools keep their own
  list of names (`apiKey`, `password`, `cookie`, `passkey`, `2facode`, ...).
- **Saving a provider tests it first.** A create with `forceSave=true` skips
  the *warnings* but not the errors, so an indexer whose site is down cannot
  be created enabled at all. The tools create it disabled, then force-save it
  enabled (the update does skip the test), which is the only way to add
  something that is temporarily unreachable.
- Updating an enabled Newznab or Torznab indexer re-fetches its capabilities,
  so a save fails with a 500 while the site is down even when nothing about
  the request is wrong. The tools say so rather than passing the 500 on.
- **A failed request holds the indexer back.** Prowlarr records the failure
  and stops using that indexer for a minute, doubling with each further
  failure (a minute, five, fifteen ...). `GET /api/v1/indexerstatus` lists
  only the ones currently held back, with `disabledTill`. Nothing clears it
  early - not a successful test - so a suite that makes an indexer fail must
  not search through it for the next minute.
- Searches are cached briefly, and a failing request is retried twice inside
  Prowlarr before it counts as a failure, so making a flaky site produce one
  visible failure takes a spell of failures and a query it has not seen.
- **The application sync skips a held-back indexer**, but leaves what it
  already pushed in place: an indexer that breaks does not disappear from
  Sonarr. `forceSync` rewrites every indexer rather than only the changed
  ones, and an application's `syncLevel` is `disabled`, `addOnly` or
  `fullSync` - `addOnly` never updates what it has already added.
- What an application receives is decided by tags (an application with tags
  gets only the indexers sharing one), by categories (it syncs the ones it
  asks for; an anime application also needs the anime categories), and by the
  kind of search it needs - Sonarr needs a TV search, Radarr a movie search,
  Lidarr music, Readarr books - which is why an indexer can be enabled,
  tagged and still reach nothing. `audit_unsynced` reports the reason per
  application.
- A proxy applies to the indexers that share a tag with it, so a proxy with
  no tags is used by nothing (and `testall` then tests nothing). The same is
  true of FlareSolverr, which is why an indexer behind Cloudflare with no
  FlareSolverr sharing its tag fails every query.
- Usenet indexers always redirect their downloads, so a usenet blackhole
  download client can never take a grab from Prowlarr.
- VIP expiry is a setting on Newznab and Torznab indexers
  (`vipExpiration`, a date), not on Cardigann definitions, and Prowlarr
  refuses to save one in the past.
- With no internet the update check answers a 500, and Prowlarr falls back to
  the definitions in `/config/Definitions/*.yml` rather than its own
  catalogue - which is what the test container relies on.
- .NET honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, so pointing them
  at a dead port is enough to cut a container off from everything but the
  hosts named in `NO_PROXY`. `scripts/testenv.sh` does exactly that.
- Every GET in the document is called against a real Prowlarr by the read
  sweep (`integration/read_sweep_test.go`), and the two that cannot answer in
  the container are classified there: the update check (no internet) and the
  upgrade log of a container that is never upgraded in place. A classified
  GET that starts answering fails the sweep, the way a stale workaround fails
  the import.
- The generated models hold booleans as `*bool` and lists as `omitzero`
  slices, so a request body can leave a flag to the server's default, send an
  explicit false, and clear a list with an empty one.

`ROADMAP.md` records the tool design rules and what is deliberately not wrapped.
