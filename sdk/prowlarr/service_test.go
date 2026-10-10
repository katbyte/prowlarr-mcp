package prowlarr

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
