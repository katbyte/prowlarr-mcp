package prowlarr

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/go-kt/chttp"
	"github.com/katbyte/pandorest/client"
)

// answering is a client for a server that answers every request with one
// status and body, and what the last request carried. Retries do not wait.
func answering(t *testing.T, status int, body string) (*Client, *http.Header) {
	t.Helper()

	seen := &http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "the-key", client.WithRetry(chttp.Retry{Wait: func(int) time.Duration { return 0 }}))
	if err != nil {
		t.Fatal(err)
	}

	return c, seen
}

// What Prowlarr says went wrong reaches the error as it said it: a failed
// validation field by field, any other failure by its message, and a body
// that is neither as it came. A 401 says which key to check, and a 404 that
// is not Prowlarr's own JSON says the address may be wrong.
func TestWhatProwlarrSaysWentWrong(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		status   int
		body     string
		messages []string
		says     []string
		not      string
	}{
		{
			name: "a failed validation", status: http.StatusBadRequest,
			body:     `[{"propertyName":"BaseUrl","errorMessage":"Unable to connect","severity":"error"},{"propertyName":"SeedRatio","errorMessage":"Below the tracker's minimum","severity":"warning"},{"propertyName":"Empty"}]`,
			messages: []string{"BaseUrl: Unable to connect", "SeedRatio: Below the tracker's minimum (warning)"},
			says:     []string{"HTTP 400", "BaseUrl: Unable to connect; SeedRatio: Below the tracker's minimum (warning)"},
		},
		{
			name: "any other failure", status: http.StatusConflict, body: `{"message":"Indexer already exists","description":"a stack trace"}`,
			messages: []string{"Indexer already exists"}, says: []string{"HTTP 409", "Indexer already exists"}, not: "stack trace",
		},
		{name: "a rejected key", status: http.StatusUnauthorized, body: `{"error":"Unauthorized"}`, says: []string{"HTTP 401", "API key rejected", "Settings → General → Security"}},
		{name: "a thing that is not there", status: http.StatusNotFound, body: `{"message":"NotFound"}`, messages: []string{"NotFound"}, says: []string{"HTTP 404", "NotFound"}, not: "URL base"},
		{name: "a path Prowlarr does not have", status: http.StatusNotFound, body: "<html>not here</html>", says: []string{"HTTP 404", "may lack Prowlarr's URL base"}},
		{name: "a body of no shape", status: http.StatusInternalServerError, body: "it broke", says: []string{"HTTP 500", "it broke"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := answering(t, tc.status, tc.body)
			_, err := c.GetSystemStatus(t.Context())
			if client.StatusCode(err) != tc.status {
				t.Fatalf("the error = %v, want one with status %d", err, tc.status)
			}
			if got := client.Messages(err); !slices.Equal(got, tc.messages) {
				t.Errorf("messages = %q, want %q", got, tc.messages)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error %q does not say %q", err, want)
				}
			}
			if tc.not != "" && strings.Contains(err.Error(), tc.not) {
				t.Errorf("the error %q says %q", err, tc.not)
			}
		})
	}
}

// Every request carries the API key in Prowlarr's own header and says which
// client is asking, and the key is in no error.
func TestRequestsCarryTheKeyAndTheClientsName(t *testing.T) {
	t.Parallel()

	c, seen := answering(t, http.StatusUnauthorized, "no")
	_, err := c.GetSystemStatus(t.Context())
	if got := seen.Get("X-Api-Key"); got != "the-key" {
		t.Errorf("X-Api-Key = %q", got)
	}
	if got := seen.Get("User-Agent"); !strings.HasPrefix(got, "prowlarr-mcp/") {
		t.Errorf("User-Agent = %q, want prowlarr-mcp and its version", got)
	}
	if err == nil || strings.Contains(err.Error(), "the-key") {
		t.Errorf("the error = %v, want one that does not carry the key", err)
	}
}

// An address that cannot be Prowlarr's is refused before anything is sent,
// saying what one looks like.
func TestNewRefusesWhatIsNotAnAddress(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, url, key, says string }{
		{"no key", "http://nas:9696", "", "API key is required"},
		{"no scheme", "nas:9696", "k", "http://nas:9696"},
		{"credentials in it", "http://user:secret@nas:9696", "k", "must not contain credentials"},
		{"a query on it", "http://nas:9696/?apikey=secret", "k", "query"},
	} {
		_, err := New(tc.url, tc.key)
		if err == nil || !strings.Contains(err.Error(), tc.says) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: New(%q) = %v, want a refusal saying %q", tc.name, tc.url, err, tc.says)
		}
	}
	c, err := New("http://nas:9696/prowlarr/", "k")
	if err != nil || c.Client.BaseURL != "http://nas:9696/prowlarr" {
		t.Errorf("a URL base with a trailing slash = %v, %v", c, err)
	}
}

// traced keeps what a client says it sent and was sent.
type traced struct {
	mu    sync.Mutex
	lines []string
}

func (l *traced) Debugf(format string, args ...any) { l.Tracef(format, args...) }

func (l *traced) Tracef(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *traced) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Join(l.lines, "\n")
}

// A trace shows every request and its answer, and no credential in either.
// A provider's settings are sent, and sent back, as pairs of a name and a
// value, so a tracker's key sits under "value", which no rule that goes by a
// field's name can know for one; and the password typed a second time is a
// password under a name that does not end as one.
func TestATraceShowsNoCredential(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// what was sent is what comes back, as Prowlarr answers a save
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(map[string]int{http.MethodPost: http.StatusCreated, http.MethodPut: http.StatusAccepted}[r.Method])
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	log := &traced{}
	c, err := New(srv.URL, "prowlarrs-own-key", client.WithLog(log))
	if err != nil {
		t.Fatal(err)
	}

	indexer := IndexerResource{Name: "Site", Fields: []Field{{Name: "baseUrl", Value: "https://site.example"}, {Name: "apiKey", Value: "trackers-key-value"}, {Name: "cookie", Value: "trackers-cookie-value"}}}
	if _, err := c.PostIndexer(t.Context(), indexer); err != nil {
		t.Fatal(err)
	}
	host := HostConfigResource{Id: 1, Port: 9696, ApiKey: "a-new-key-value", Password: "a-password-value", PasswordConfirmation: "typed-again-value", ProxyPassword: "proxy-password-value", Username: "someone"} //nolint:gosec // made-up values: keeping them out of the trace is what the test checks
	if _, err := c.PutConfigHostById(t.Context(), 1, host); err != nil {
		t.Fatal(err)
	}

	said := log.all()
	for _, secret := range []string{"prowlarrs-own-key", "trackers-key-value", "trackers-cookie-value", "a-new-key-value", "a-password-value", "typed-again-value", "proxy-password-value"} {
		if strings.Contains(said, secret) {
			t.Errorf("%q is in the trace", secret)
		}
	}
	// and it is still a trace: what was asked, of what, and the names sent
	for _, want := range []string{"POST", "/api/v1/indexer", "/api/v1/config/host/1", `"apiKey"`, `"cookie"`, "someone", "9696"} {
		if !strings.Contains(said, want) {
			t.Errorf("the trace does not show %s", want)
		}
	}
	if t.Failed() {
		t.Log("\n" + said)
	}
}
