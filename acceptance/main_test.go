//go:build integration

// Package acceptance covers every tool against a real Prowlarr running in
// Docker - the API wrappers and the audits alike - so response shapes, query
// encoding and Prowlarr's own rules are checked against the thing
// prowlarr-mcp actually talks to rather than a stub.
//
// Prowlarr talks to fakes the suite runs on the host (internal/fakes):
// Newznab and Torznab sites seeded with releases, some of them broken in the
// ways the audits exist to find, and a Sonarr, a Radarr and a Lidarr that
// record what Prowlarr syncs into them. The fixtures are built through the
// tools themselves (indexer_add, app_add, proxy_add...), so the setup is part
// of the coverage.
//
//	make testacc-acceptance                     # container started and torn down
//
//	eval "$(scripts/testenv.sh up)"             # or drive it by hand
//	go test -tags integration ./acceptance/...
//	scripts/testenv.sh down
package acceptance

import "testing"

func TestMain(m *testing.M) { testMain(m) }
