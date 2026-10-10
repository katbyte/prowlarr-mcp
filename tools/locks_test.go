package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// keepingServer is a Prowlarr that keeps what it is sent: one indexer and the
// tags, each read and written back whole, which is all two edits need to
// undo each other. Every write takes a moment, as a real one does, so calls
// made at once overlap.
type keepingServer struct {
	srv *httptest.Server

	mu      sync.Mutex
	indexer map[string]any
	tags    []map[string]any
}

const keptIndexer = 4

func newKeepingServer(t *testing.T) *keepingServer {
	t.Helper()

	k := &keepingServer{indexer: torznab(keptIndexer, "Edit Me", "private")}
	k.srv = httptest.NewServer(http.HandlerFunc(k.serve))
	t.Cleanup(k.srv.Close)

	return k
}

func (k *keepingServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	one := "/api/v1/indexer/" + strconv.Itoa(keptIndexer)

	var answer any
	status := http.StatusOK
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/indexer":
		answer = []any{k.read()}
	case "GET " + one:
		answer = k.read()
	case "PUT " + one:
		var sent map[string]any
		if err := json.Unmarshal(body, &sent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		time.Sleep(20 * time.Millisecond)
		k.mu.Lock()
		k.indexer = sent
		k.mu.Unlock()
		answer, status = sent, http.StatusAccepted
	case "GET /api/v1/tag":
		k.mu.Lock()
		answer = slices.Clone(k.tags)
		k.mu.Unlock()
	case "POST /api/v1/tag":
		var sent map[string]any
		if err := json.Unmarshal(body, &sent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		time.Sleep(20 * time.Millisecond)
		k.mu.Lock()
		sent["id"] = len(k.tags) + 1
		k.tags = append(k.tags, sent)
		k.mu.Unlock()
		answer, status = sent, http.StatusCreated
	case "GET /api/v1/appprofile":
		answer = []any{map[string]any{"id": 1, "name": "Standard"}}
	default:
		// everything else a tool reads on the way: nothing configured
		if r.Method != http.MethodGet {
			http.NotFound(w, r)

			return
		}
		answer = []any{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(answer)
}

// read is the indexer as it is kept, as a copy.
func (k *keepingServer) read() map[string]any {
	k.mu.Lock()
	defer k.mu.Unlock()

	return maps.Clone(k.indexer)
}

// The calls of one turn run at once, and an edit sends the whole record
// back: eight indexer_edit calls each adding a tag of its own and one they
// share must leave all nine on the indexer, and make the shared one once.
// Without the locks each sent the tags as they were before the others, and
// the tag they shared was made by every call that found it missing.
func TestEditsOfOneIndexerAtOnceAllHold(t *testing.T) {
	t.Parallel()

	k := newKeepingServer(t)
	client, err := prowlarr.New(k.srv.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	r := queuedFor(client, Options{})
	registerEvery(t, r, srv)

	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	const edits = 8
	var wg sync.WaitGroup
	failures := make([]string, edits)
	for n := range edits {
		wg.Go(func() {
			res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "indexer_edit", Arguments: map[string]any{"indexer": "Edit Me", "add_tags": []string{"shared", fmt.Sprintf("own-%d", n)}, "force": true}})
			switch {
			case err != nil:
				failures[n] = err.Error()
			case res.IsError:
				failures[n] = fmt.Sprint(res.Content)
			}
		})
	}
	wg.Wait()
	for n, msg := range failures {
		if msg != "" {
			t.Errorf("edit %d: %s", n, msg)
		}
	}

	k.mu.Lock()
	labels := make([]string, 0, len(k.tags))
	for _, tag := range k.tags {
		labels = append(labels, fmt.Sprint(tag["label"]))
	}
	k.mu.Unlock()
	slices.Sort(labels)
	if len(labels) != edits+1 || len(slices.Compact(slices.Clone(labels))) != edits+1 {
		t.Errorf("the tags made = %s, want the shared one once and one for each edit", strings.Join(labels, ", "))
	}

	if held, ok := k.read()["tags"].([]any); !ok || len(held) != edits+1 {
		t.Errorf("the indexer holds tags %v, want all %d the edits added", k.read()["tags"], edits+1)
	}
	if !r.locks.Idle() {
		t.Error("a lock was left held")
	}
}
