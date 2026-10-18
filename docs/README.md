# docs

- [audits.md](audits.md): the full rules of each audit.
- [tools.md](tools.md): every tool, the toolsets, and allowing or denying single tools.
- [configuration.md](configuration.md): every setting, serving over HTTP, Docker, and two Prowlarrs.
- [development.md](development.md): the Go client, building, and the tests.
- [ROADMAP.md](ROADMAP.md): the design rules behind the tools.
- This page: Prowlarr's API description, where it is wrong, and how Prowlarr behaves where the description does not say.

## The API description

The Go client (`sdk/prowlarr`) is generated from the OpenAPI document Prowlarr publishes, kept in `api-defs/`. [pandorest](https://github.com/katbyte/pandorest), run through `sdk/pandorest`, imports it into definitions checked in beside it, fixing the document's known faults on the way, and generates the client from those.

| File | From | Version |
|---|---|---|
| `api-defs/prowlarr-openapi-2.6.5.5623.json`, imported into `api-defs/prowlarr-2.6.5.5623/` | `src/Prowlarr.Api.V1/openapi.json` in [Prowlarr/Prowlarr](https://github.com/Prowlarr/Prowlarr), at the release tag | Prowlarr 2.6.5.5623: 93 paths, 122 operations once the web interface's six routes and `HEAD /ping` are left out. The client has 125 operations: those, and the three commands it runs |

- `make generate` imports and generates. `make gencheck` fails when the generated code is out of date, and `make apicheck` when an operation has no method.
- The file is named by the Prowlarr release it came from. The document's own version is `1.0.0` and never moves; the client carries both, as `DocumentVersion` and `APIVersion`.
- A running Prowlarr does not serve the document. It is built with the release and committed, so the copy at a tag is what that release has.

**To move to a new release:** take the file from the new tag, pretty-print it with `jq .`, and save it beside the old one under the new release's name. `make pandorest-import` imports the newest one present, and `go run ./sdk/pandorest diff -old api-defs/prowlarr-<old> -new api-defs/prowlarr-<new>` says what changed, breaking changes marked. Then `make generate`, review, and delete the old pair. A fix whose fault the new document no longer has stops the import and names itself; delete it. The test container's image (`.scripts/testenv.sh`) is pinned to the same release and moves with it.

The document says what Prowlarr means to do, not what it does. The live suites under `acceptance/` hold the client to a real Prowlarr, and what they found is below.

## Where the description is wrong

Each fault is fixed on import by a named workaround in `sdk/pandorest/workarounds`, which checks the fault is still there and stops the import once it is not.

- `prowlarr-ui-routes`: the web interface's own routes (the page, its files, the sign-in form) are not API, and are left out.
- `prowlarr-undeclared-responses`: nine reads say they answer nothing. The Newznab and Torznab feeds and the log files answer files, the route table answers text, and the rest answer JSON.
- `prowlarr-undeclared-write-responses`: a provider action (`POST /api/v1/{kind}/action/{name}`, how the web interface asks a definition to fill in a setting) answers JSON of its own shape, not nothing.
- `prowlarr-created-accepted`: every create answers `201` and every update `202`, where the document says `200`, which the client would take as an error.
- `prowlarr-put-id-integer`: every update types its `{id}` as text, where the matching read and delete type it as a number.
- `prowlarr-test-all-records`: there is no record for the result of testing a provider, or for why it failed. Both are declared.
- `prowlarr-test-all-results`: testing every provider of a kind (`POST .../testall`) says it answers nothing. It answers each provider's result, with `400` when any fails, which the client takes as an answer, so one bad indexer does not hide the rest.
- `prowlarr-command-body`: `POST /api/v1/command` says it takes a record of read-only status. It takes the command's name and whatever that command needs (`{"name": "ApplicationIndexerSync", "forceSync": true}`).
- `prowlarr-commands`: nothing says which commands there are or when one has finished. The three this repository runs (`ApplicationIndexerSync`, `Backup`, `CheckHealth`) are declared, so each gets a method and a form that waits. A scheduled task is still run by the name Prowlarr lists it under.
- `prowlarr-bulk-answer-lists`: the bulk updates and the bulk grab say they answer one record. They answer a list of everything they changed.
- `prowlarr-host-settings`: the host settings leave out `allowedHosts` and `trustedNetworks`, which Prowlarr sends and reads back. Without them no host setting could be changed on a server that lets local addresses in without signing in, so both are added and always sent.
- `prowlarr-restore-upload-body`: restoring from an upload declares no body, but Prowlarr reads the backup from a form upload. The body is declared as a file.

## How Prowlarr behaves

### Calling it

- The API key goes in `X-Api-Key` (Settings → General → Security). It is the server's key; Prowlarr has no users of its own.
- Dates are UTC (RFC 3339) and the client reads them as times. Year one, which Prowlarr sends for "never", reads as no date.
- Paged lists take `page` and `pageSize` and answer `records` and `totalRecords`. The client's `Complete` and `Each` forms walk every page.
- A true/false field in a record the server only answers is a plain value. In a record that is sent it is optional, so a request can leave it to the server's default; a list sent empty clears it.
- `DELETE /api/v1/command/{id}` cancels a command only while it is queued. One that has started answers `409`.
- Every read in the document is called against a real Prowlarr by the read sweep (`acceptance/prowlarr/read_sweep_test.go`). The one that cannot answer in the container, the upgrade log, is named there, and fails the sweep once it does answer.

### Providers

Indexers, applications, download clients, proxies and notifications are all providers: a name, a kind and a list of `fields` whose meaning comes from the kind's schema (`GET .../schema`).

- **Secrets come back masked** as `********`, and sending the mask back keeps the stored value. The schema marks which fields are secret, except on Cardigann definitions, which mark a tracker's own credentials as normal, so the tools keep their own list of secret names (`apiKey`, `password`, `cookie`, `passkey`, `2facode` and others). Whether Prowlarr masks a user name is unchecked; the tools hide it either way.
- **Saving one switched on tests it first**, and a failed test refuses it whatever `forceSave` says; `forceSave` skips only warnings. An update with `forceSave` does skip the test, so the tools add a provider switched off, then switch it on with an update. That is the only way to add something that is down for now.
- Updating a switched-on Newznab or Torznab indexer fetches what it can do again, so the save fails while the site is down even when nothing is wrong with it. The tools say so.
- A notification counts as on when it is sent for something. One sent for grabs hears of the applications' grabs; a grab from Prowlarr's own search needs `includeManualGrabs`.
- VIP expiry (`vipExpiration`) is a setting on Newznab and Torznab indexers, not Cardigann ones, and Prowlarr refuses a date in the past.
- Usenet indexers always redirect their downloads, so a usenet blackhole download client can never take a grab from Prowlarr.
- Saved filters belong to one page of the web interface (`indexerIndex`, `indexerStats`, `history`, `releases`), and only that page reads them.

### Failing indexers

- **A failed request holds the indexer back** for a minute, then five, then fifteen, longer with each failure. `GET /api/v1/indexerstatus` lists only those held back now, with `disabledTill`. Nothing clears it early, not even a passing test, so a suite that makes an indexer fail must not search through it for the next minute.
- Searches are cached briefly, and Prowlarr retries a failed request twice before counting it, so one visible failure takes a run of failures and a query it has not seen.

### What reaches the applications

- An application gets the indexers that share a tag with it (all of them if it has no tags), in the categories it syncs (anime needs the anime categories too), that offer the search it needs: TV for Sonarr, movies for Radarr, music for Lidarr, books for Readarr. That is how an indexer can be switched on and tagged and still reach nothing; `audit_unsynced` gives each application's reason.
- The sync skips an indexer that is held back, but leaves what it already sent: an indexer that breaks does not vanish from Sonarr. `forceSync` rewrites every indexer, not only the changed ones.
- An application's `syncLevel` is `disabled`, `addOnly` or `fullSync`. `addOnly` never updates what it has added.
- A proxy, FlareSolverr included, applies only to indexers sharing a tag with it. One with no tags is used by nothing, and `testall` then tests nothing; an indexer behind Cloudflare with no FlareSolverr on its tag fails every query.

### Settings, backups and restarts

- **Some settings are taken and not kept.** A username sent while sign-in is handled outside Prowlarr (`External`) is accepted and read back empty, so `server_settings_edit` reads the section back and answers what held as `changed` and what did not as `not_kept`.
- A backup made through the API is kept until deleted; Prowlarr prunes old ones only when its scheduled backup runs.
- A restore (`POST /api/v1/system/backup/restore/{id}`) answers at once and takes effect at the next start. `config.xml` is written over straight away.
- In the test container a shutdown does not last: the container starts Prowlarr again within seconds, which is how the suites test a shutdown and a restore. A real install stays down.

### Health, and the test container

- **Health checks stop with no internet.** Once a build is 14 days old, a health check asks Prowlarr's update server for a newer one, and an unanswered request fails the whole check, so nothing is recorded at all. The suites passed until two weeks after the release and failed from that day, which is why they now play the update server themselves (`lib/testenv`).
- The clock check asks the same server for the time and raises an error when it is over a day out, so its answer cannot be recorded once and replayed.
- With no internet, `GET /api/v1/update` answers a `500`, and Prowlarr uses the definitions in `/config/Definitions/*.yml` in place of its own catalogue, which the test container relies on.
- Prowlarr honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, and trusts the certificates `SSL_CERT_FILE` names, so `.scripts/testenv.sh` points the container at a proxy the suites run, and it reaches only what they answer.
- Prowlarr checks its health as it starts, before a suite's proxy is listening, so each suite runs `CheckHealth` once more as it sets up.
