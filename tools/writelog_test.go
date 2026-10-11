package tools

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every call of a write tool is written down with what it sent, and what a
// setting was set to is not among it: a tracker's cookie and passkey come
// under names nothing knows for a credential's, and go-kt's registry blanks
// every value under a free-form argument. Prowlarr's own settings are the
// exception, shown but for its credentials, since what a port was changed to
// is what the line is for.
func TestAWriteIsWrittenDownWithoutWhatItsSettingsWereSetTo(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		lines []string
	)
	f := newFakeServer(t)
	f.answer("GET /api/v1/config/host", hostSettings())
	client, err := prowlarr.New(f.srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	r := &registry{client: client, writeLog: func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}}
	queueTools(r)
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	registerEvery(t, r, srv)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// one that fails, one refused for an argument the tool does not take,
	// one answered, and a change to Prowlarr's own settings that is refused:
	// each is written down
	_ = callError(t, cs, "indexer_edit", map[string]any{"indexer": "Site", "settings": map[string]any{"cookie": "tracker-cookie-value", "passkey": "pass-key-value"}})
	_ = callError(t, cs, "indexer_edit", map[string]any{"indexer": "Site", "settings": map[string]any{"pid": "tracker-pid-value"}, "no_such_argument": true})
	mustCall(t, cs, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"port": 9697, "apiKey": "new-key-value", "passwordConfirmation": "typed-again-value"}}) //nolint:gosec // made-up values: keeping them out of the log is what the test checks
	_ = callError(t, cs, "server_settings_edit", map[string]any{"section": "host", "settings": map[string]any{"port": 9698}, "no_such_argument": true})

	mu.Lock()
	written := strings.Join(lines, "\n")
	mu.Unlock()
	if len(lines) != 4 {
		t.Fatalf("%d lines for four calls of a write tool:\n%s", len(lines), written)
	}
	// a password typed a second time is a password under a name that does
	// not end as one, and a setting's value is shown only for a call that
	// was answered: one refused is not held to the settings Prowlarr has
	for _, secret := range []string{"tracker-cookie-value", "pass-key-value", "tracker-pid-value", "new-key-value", "typed-again-value", "9698"} {
		if strings.Contains(written, secret) {
			t.Errorf("%q is in the log:\n%s", secret, written)
		}
	}
	for _, want := range []string{"indexer_edit", `"cookie"`, `"passkey"`, `"pid"`, "server_settings_edit", "9697"} {
		if !strings.Contains(written, want) {
			t.Errorf("the log does not say %s:\n%s", want, written)
		}
	}
}
