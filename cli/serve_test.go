package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/server"
	"github.com/katbyte/prowlarr-mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testToken  = "s3cret"
	testListen = ":8080"
	// appName is what this tool calls itself to an operator and a client
	appName = "prowlarr-mcp"
)

// Serving itself is go-kt's, tested there. What is this tool's own is what it
// hands over: the flags, and the names an operator and a client are told.
func TestServeOptions(t *testing.T) {
	t.Parallel()

	f := &FlagData{Listen: testListen, AuthToken: testToken, AllowNoAuth: true}
	if got, want := f.serveOptions(), (server.Options{Listen: testListen, AuthToken: testToken, AllowNoAuth: true, Name: appName, EnvPrefix: "PROWLARR"}); got != want {
		t.Errorf("serveOptions = %+v, want %+v", got, want)
	}
}

// The health probe has to stay outside the auth check, or a container with a
// token configured never becomes healthy; the MCP endpoint is behind it, and
// a client sent away is told whose token it lacks.
func TestServeRoutes(t *testing.T) {
	t.Parallel()

	srv := mcp.NewServer(&mcp.Implementation{Name: appName, Version: "test"}, nil)
	routes := server.Handler(srv, (&FlagData{AuthToken: testToken}).serveOptions())

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, server.HealthPath, http.NoBody))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("%s with a token configured = %d %q, want 200 ok", server.HealthPath, rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, server.Path, strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("%s without a token = %d, want 401", server.Path, rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="prowlarr-mcp"` {
		t.Errorf("WWW-Authenticate = %q, want a Bearer challenge naming prowlarr-mcp", got)
	}
}

// A client that connects is told what holds for every server in the family,
// which is go-kt's to say, and then what holds between this one's tools.
func TestServeInstructions(t *testing.T) {
	t.Parallel()

	told := server.Instructions(tools.Instructions...)
	if !strings.HasPrefix(told, server.DefaultInstructions) {
		t.Errorf("the instructions do not open with go-kt's: %q", told)
	}
	for _, own := range tools.Instructions {
		if !strings.Contains(told, own) {
			t.Errorf("the instructions leave out %q", own)
		}
	}
}

// --listen with no bearer token is an open port, so it is refused unless the
// operator said so in as many words, and the refusal names this tool's own
// variables.
func TestServeNeedsAnAuthToken(t *testing.T) {
	t.Parallel()

	if err := server.CheckAuth((&FlagData{Listen: testListen}).serveOptions()); err == nil {
		t.Error("no token and no --allow-no-auth was not refused")
	} else if err.Error() != "--listen needs --auth-token (PROWLARR_AUTH_TOKEN); to serve with no token at all, pass --allow-no-auth (PROWLARR_ALLOW_NO_AUTH=true)" {
		t.Errorf("the refusal reads %q, want it as it always read", err)
	}
	if err := server.CheckAuth((&FlagData{Listen: testListen, AllowNoAuth: true}).serveOptions()); err != nil {
		t.Errorf("--allow-no-auth was refused: %v", err)
	}
	if err := server.CheckAuth((&FlagData{Listen: testListen, AuthToken: testToken}).serveOptions()); err != nil {
		t.Errorf("a token was refused: %v", err)
	}
}
