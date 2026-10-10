# docs

## The API spec

Prowlarr publishes an OpenAPI document, vendored under `api-defs/` as the reference for `sdk/prowlarr` - and it is a build input. [pandorest](https://github.com/katbyte/pandorest), run through `sdk/pandorest`, imports it into checked-in definitions beside it, fixing the document's known bugs with named workarounds on the way, and generates the client from those definitions. `make generate` runs both steps, `make pandorest-diff` reports what a refreshed document would change, `make gencheck` (and the unit tests) fail when the generated code is stale, and `make apicheck` proves every operation in the spec has a method.

| File | Source | Version vendored |
|---|---|---|
| `api-defs/prowlarr-openapi-2.6.5.5623.json`, imported into `api-defs/prowlarr-2.6.5.5623/` | `src/Prowlarr.Api.V1/openapi.json` in [Prowlarr/Prowlarr](https://github.com/Prowlarr/Prowlarr), at the release tag | Prowlarr 2.6.5.5623 - 93 paths, 122 operations (the six web interface routes are dropped on import, and `HEAD /ping` is skipped) |

The file is named by the Prowlarr release it was taken from, not by the version the document gives itself: that is `1.0.0`, the API's name, and does not move from release to release. The generated package carries both, as `DocumentVersion` and `APIVersion`.

Prowlarr generates the document with Swashbuckle at build time and commits it, so the copy in the repository at a tag is exactly what that release serves. A running server does not serve it. To refresh: take the file from the new tag, pretty-print it with `jq .` so the diff is readable, and save it beside the old one under the new release's name. `make pandorest-import` imports the highest release present into a definitions directory of its own, `go run ./sdk/pandorest diff -old api-defs/prowlarr-<old> -new api-defs/prowlarr-<new>` says what changed in API terms (breaking changes are marked), then `make generate`, review, and delete the old pair. A workaround whose bug the new document fixes fails the import and names itself; delete it. The test container's image (`scripts/testenv.sh`) is pinned to the same release and moves with it.

The spec is documentation of intent, not of behaviour: the live suites
(`integration/`, `acceptance/`) are what prove the shapes against a real
server, and the quirks they found are recorded below.

## Where the spec is wrong

These are shape bugs, fixed in the generated client by the importer's
workarounds (`sdk/pandorest/workarounds`, listed in the definitions'
`Service.json`). Each checks its bug is still in the document and fails the
import once it is not.

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
- `prowlarr-host-settings` - `HostConfigResource` does not list `allowedHosts` or `trustedNetworks`, which the server sends with the host settings and reads back from them. A client built from the document sends neither, and Prowlarr refuses host settings whose allowed hosts are empty unless every request has to sign in, so on a server that lets local addresses in unasked no host setting could be changed at all. Both are added, and are always sent.

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
- **Prowlarr takes some settings and does not keep them.** A username sent with the host settings while sign-in is handled outside Prowlarr (authentication method `External`) is accepted without complaint and read back empty. So `server_settings_edit` reads the section back after saving, and answers what held as `changed` and what did not as `not_kept`.
- Usenet indexers always redirect their downloads, so a usenet blackhole
  download client can never take a grab from Prowlarr.
- VIP expiry is a setting on Newznab and Torznab indexers
  (`vipExpiration`, a date), not on Cardigann definitions, and Prowlarr
  refuses to save one in the past.
- A backup asked for through the API is kept until it is deleted: Prowlarr prunes old backups only when its scheduled backup runs.
- Restoring a backup swaps the database in when Prowlarr next starts, so `POST /api/v1/system/backup/restore/{id}` answers at once and nothing looks different until a restart; `config.xml` is written over straight away.
- `DELETE /api/v1/command/{id}` cancels a command only while it is still queued; one that has started answers 409.
- A provider (an indexer, an application, a proxy, a download client, a notification) is tested when it is created switched on, and refused if the test fails whatever `forceSave` says; an update with `forceSave` skips the test. So a forced add creates it switched off and switches it on with an update. A notification is "on" when it is sent for something.
- A notification sent for grabs is sent for the applications' grabs; one made from Prowlarr's own search is sent only when `includeManualGrabs` is set.
- Saved filters are kept by the page of the web interface they belong to, under `indexerIndex`, `indexerStats`, `history` and `releases`; nothing but that page reads them.
- With no internet `GET /api/v1/update` answers a 500, and Prowlarr falls back to the definitions in `/config/Definitions/*.yml` rather than its own catalogue - which is what the test container relies on.
- Once a build is 14 days old, Prowlarr's health checks ask its update server whether there is a newer one (`UpdateCheck`), and do not catch a request that fails: with the update server out of reach the whole `CheckHealth` command fails and no health check is recorded at all, the others included. A Prowlarr cut off from the internet therefore reports no health from two weeks after its release. The live suites passed until then and failed from that day, which is why they now answer the update server themselves (`internal/fakes/servarrcloud`).
- The clock check (`SystemTimeCheck`) asks the same host for the time and raises a health error when it is over a day from the server's own, so an answer to it cannot be recorded once and replayed.
- .NET honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, and on Linux trusts the certificates `SSL_CERT_FILE` names, so a container pointed at a proxy the suites run reaches only what the suites answer and the hosts named in `NO_PROXY`. `scripts/testenv.sh` does exactly that.
- Prowlarr checks its health as it starts, which in the test container is before a suite's proxy is listening, so each suite runs `CheckHealth` once more in its setup.
- Every GET in the document is called against a real Prowlarr by the read sweep (`integration/read_sweep_test.go`), and the one that cannot answer in the container is classified there: the upgrade log of a container that is never upgraded in place. A classified GET that starts answering fails the sweep, the way a stale workaround fails the import.
- The generated models hold booleans as `*bool` and lists as `omitzero`
  slices, so a request body can leave a flag to the server's default, send an
  explicit false, and clear a list with an empty one.

`ROADMAP.md` records the tool design rules and what is deliberately not wrapped.
