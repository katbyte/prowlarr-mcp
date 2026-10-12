// Package testenv is what the two live suites share that is this
// application's own: the environment .scripts/testenv.sh hands them, and how
// the proxy everything Prowlarr reaches for beyond the suites' fakes goes
// through is set up. The machinery is go-kt's (test/env and
// test/replayproxy).
//
// The tool suite (../../acceptance/tools) drives the tools over MCP and the client suite (../../acceptance/prowlarr) drives the generated SDK; each
// runs against a container and a proxy of its own.
package testenv

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/katbyte/go-kt/test/env"
	"github.com/katbyte/go-kt/test/replayproxy"
	"github.com/katbyte/go-kt/test/services/arrserver"
)

// NotConfigured is what a test says when it skips for want of a container.
const NotConfigured = "PROWLARR_SERVER and PROWLARR_TOKEN are not set; run: eval \"$(.scripts/testenv.sh up)\""

// environment is what .scripts/testenv.sh writes, every variable of which
// begins PROWLARR_.
var environment = env.New("PROWLARR")

// Configured reports whether the container environment is present: a server
// and a token.
func Configured() bool { return environment.Configured() }

// Server and Token are the address of the Prowlarr under test and its API
// key.
func Server() string { return environment.Server() }
func Token() string  { return environment.Token() }

// DataDir is the host path the container's /config and /downloads are
// bind-mounted from, so a test can look at what Prowlarr wrote, or take a
// definition away. It is "" when PROWLARR_TEST_DATA is not set.
func DataDir() string { return environment.DataDir() }

// Container is the name of the container .scripts/testenv.sh started, which is
// also the name Prowlarr is reached by from inside it.
func Container() string { return environment.Container() }

// Host is the address the container reaches the suites' fakes on.
func Host() string {
	if h := environment.Get("TEST_HOST"); h != "" {
		return h
	}

	return "host.docker.internal"
}

// IndexerPort and AppPort are the ports the fake sites and the fake
// applications listen on: the ones the environment names, or a suite's own
// default when it names none.
func IndexerPort(def int) (int, error) { return port("TEST_INDEXER_PORT", def) }
func AppPort(def int) (int, error)     { return port("TEST_APP_PORT", def) }

func port(name string, def int) (int, error) {
	v := environment.Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s=%q is not a port", environment.Var(name), v)
	}

	return n, nil
}

// The hosts Prowlarr calls home to (ProwlarrCloudRequestBuilder in its
// source): its makers' services, and the service that keeps its tracker
// definitions up to date.
const (
	servicesHost    = "prowlarr.servarr.com"
	definitionsHost = "indexers.prowlarr.com"
)

// Proxy is the proxy a suite runs for the length of its run, and what it saw
// once stopped.
type Proxy = env.Proxy

// StartProxy brings up the proxy the container's HTTPS_PROXY already points
// at, signing with the CA .scripts/testenv.sh minted and mounted into the
// container, and proves the container can reach it.
//
// Prowlarr's own services are played by the suite through it: home is the
// update server, the clock and the notices, and the definitions service is
// down, so that Prowlarr reads the definitions seeded on its disk, which is
// what it does offline. Nothing is recorded and there are no cassettes: any
// other request is one the suite did not expect Prowlarr to make, and fails
// the run (Proxy.Report).
func StartProxy(ctx context.Context, home *arrserver.Server) (*Proxy, error) {
	p, err := environment.StartProxy(ctx, replayproxy.Options{
		// required, and never written: the suites record nothing
		CassetteDir: filepath.Join("testdata", "cassettes"),
		RecordHint:  "Prowlarr is not expected to reach it: answer it from the suite (lib/testenv), or find what started asking",
	})
	if err != nil {
		return nil, err
	}
	p.Serve(servicesHost, home)
	p.Serve(definitionsHost, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the definitions service is not there", http.StatusServiceUnavailable)
	}))

	return p, nil
}
