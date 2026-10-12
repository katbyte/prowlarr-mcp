//go:build acceptance

// Package prowlarr_test runs the generated SDK, sdk/prowlarr, against a real
// Prowlarr in Docker. It tests one thing: that every response Prowlarr sends
// decodes into the generated types with the fields actually populated, and
// that every request the generated options and bodies build is one Prowlarr
// accepts, in the status the operation is generated to expect. Nothing here
// is about the tools; ../tools covers those.
//
// The suite runs the same fakes the tool suite does (internal/fakes) -
// Newznab and Torznab sites, and a Sonarr - and creates what it tests
// through the SDK's own write methods: an indexer, an application, a profile,
// a tag, a proxy and a download client, each removed again at the end.
//
//	make testacc-sdk                 # container started and torn down
//
//	eval "$(.scripts/testenv.sh up)"          # or drive it by hand
//	go test -tags acceptance ./acceptance/prowlarr/...
//	.scripts/testenv.sh down
package prowlarr_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(runSuite(m)) }
