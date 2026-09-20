// Package servarr is a fake Sonarr, Radarr, Lidarr or Readarr for the live
// suites: the handful of API routes Prowlarr calls to push its indexers into
// an application, served for any number of applications on one listener,
// each under its own path, with its own API key.
//
// Prowlarr tests an application by reading its status and posting a test
// indexer, then syncs by reading the application's indexer schema and its
// indexers and adding, updating or deleting the ones it owns (SonarrV3Proxy
// and its siblings in Prowlarr's source). Recording what arrives is how the
// suites check which indexers really reach which application, rather than
// trusting the rules the tools work that out by.
package servarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kinds of application, and the API version each speaks.
const (
	Sonarr  = "Sonarr"
	Radarr  = "Radarr"
	Lidarr  = "Lidarr"
	Readarr = "Readarr"
)

// apiVersion is the API path an application kind answers under.
var apiVersion = map[string]string{Sonarr: "v3", Radarr: "v3", Lidarr: "v1", Readarr: "v1"}

// versions are what each kind reports by default: releases new enough for
// Prowlarr, which refuses to sync to one older than it supports.
var versions = map[string]string{Sonarr: "4.0.9.2244", Radarr: "5.8.3.8933", Lidarr: "2.5.3.4341", Readarr: "0.4.0.2593"}

// schemaFields are the settings of the indexer schemas the fake offers: every
// field Prowlarr fills in when it syncs to any of the kinds.
var schemaFields = []string{
	"baseUrl", "apiPath", "apiKey", "categories", "animeCategories", "animeStandardFormatSearch", "minimumSeeders",
	"seedCriteria.seedRatio", "seedCriteria.seedTime", "seedCriteria.seasonPackSeedTime", "seedCriteria.discographySeedTime",
	"rejectBlocklistedTorrentHashesWhileGrabbing", "additionalParameters", "earlyReleaseLimit", "multiLanguages", "removeYear", "requiredFlags",
}

// App is one fake application.
type App struct {
	// Name is the application's path: it answers under /<Name>.
	Name string
	// Kind is Sonarr, Radarr, Lidarr or Readarr.
	Kind string
	// APIKey is the key every call must carry in X-Api-Key.
	APIKey string
	// Version is what it reports, e.g. 4.0.9.2244; empty is a recent release
	// of its kind.
	Version string
	// Down answers every call with a 503, the way an application that is
	// stopped would, if it could.
	Down bool
}

// Field is one setting of an indexer, as the applications hold them.
type Field struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// Indexer is an indexer as an application holds it.
type Indexer struct {
	ID                      int     `json:"id"`
	Name                    string  `json:"name"`
	Implementation          string  `json:"implementation"`
	ConfigContract          string  `json:"configContract"`
	EnableRss               bool    `json:"enableRss"`
	EnableAutomaticSearch   bool    `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool    `json:"enableInteractiveSearch"`
	Priority                int     `json:"priority"`
	Tags                    []int   `json:"tags"`
	Fields                  []Field `json:"fields"`
}

// Value is one of an indexer's settings, nil when it has none.
func (i *Indexer) Value(name string) any {
	for _, f := range i.Fields {
		if f.Name == name {
			return f.Value
		}
	}

	return nil
}

// Categories are the categories an indexer was synced with.
func (i *Indexer) Categories() []int {
	list, ok := i.Value("categories").([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(list))
	for _, v := range list {
		if n, ok := v.(float64); ok {
			out = append(out, int(n))
		}
	}

	return out
}

// Options configure a Server.
type Options struct {
	// Addr is where to listen; empty picks a free port on 127.0.0.1.
	Addr string
	// PublicHost is the host the container reaches this process by; default
	// 127.0.0.1.
	PublicHost string
	Apps       []App
}

type appState struct {
	app      App
	indexers []Indexer
	nextID   int
	calls    []string
}

// Server is a running set of fake applications.
type Server struct {
	publicHost string
	listener   net.Listener
	srv        *http.Server

	mu   sync.Mutex
	apps map[string]*appState
}

// New starts a server listening on opts.Addr.
func New(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	if opts.PublicHost == "" {
		opts.PublicHost = "127.0.0.1"
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("servarr: listening on %s: %w", opts.Addr, err)
	}
	s := &Server{publicHost: opts.PublicHost, listener: ln, apps: map[string]*appState{}}
	for _, a := range opts.Apps {
		if err := s.Add(a); err != nil {
			_ = ln.Close()
			return nil, err
		}
	}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()

	return s, nil
}

// Close stops the server.
func (s *Server) Close() error { return s.srv.Close() }

// Port is the port the server listens on.
func (s *Server) Port() int {
	if a, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return a.Port
	}

	return 0
}

// URL is an application's address as the container reaches it, what
// Prowlarr is given as its base URL.
func (s *Server) URL(app string) string {
	return "http://" + net.JoinHostPort(s.publicHost, strconv.Itoa(s.Port())) + "/" + app // a fake on the host, never TLS
}

// Add adds an application, or replaces one of the same name and forgets
// what it held.
func (s *Server) Add(a App) error {
	if a.Name == "" || strings.ContainsAny(a.Name, "/?#") {
		return fmt.Errorf("servarr: %q is not an application name", a.Name)
	}
	if _, ok := apiVersion[a.Kind]; !ok {
		return fmt.Errorf("servarr: %s: kind must be Sonarr, Radarr, Lidarr or Readarr, got %q", a.Name, a.Kind)
	}
	if a.Version == "" {
		a.Version = versions[a.Kind]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps[a.Name] = &appState{app: a, nextID: 1}

	return nil
}

// SetDown makes an application answer every call with a 503, or stop.
func (s *Server) SetDown(app string, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.apps[app]; st != nil {
		st.app.Down = down
	}
}

// Indexers are the indexers an application holds now, sorted by name.
func (s *Server) Indexers(app string) []Indexer {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.apps[app]
	if st == nil {
		return nil
	}
	out := slices.Clone(st.indexers)
	slices.SortFunc(out, func(a, b Indexer) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// Calls are the requests an application received, as "METHOD /path".
func (s *Server) Calls(app string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.apps[app]; st != nil {
		return slices.Clone(st.calls)
	}

	return nil
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	name, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.apps[name]
	if st == nil {
		http.NotFound(w, r)
		return
	}
	st.calls = append(st.calls, r.Method+" /"+rest)
	if st.app.Down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("X-Api-Key") != st.app.APIKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	prefix := "api/" + apiVersion[st.app.Kind] + "/"
	route, ok := strings.CutPrefix(rest, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Application-Version", st.app.Version)

	switch {
	case r.Method == http.MethodGet && route == "system/status":
		writeJSON(w, http.StatusOK, map[string]any{"appName": st.app.Kind, "instanceName": st.app.Name, "version": st.app.Version})
	case r.Method == http.MethodGet && route == "indexer/schema":
		writeJSON(w, http.StatusOK, []Indexer{schema("Newznab"), schema("Torznab")})
	case r.Method == http.MethodGet && route == "indexer":
		// an empty list, never null: Prowlarr filters the answer with LINQ,
		// which throws on a null
		writeJSON(w, http.StatusOK, append([]Indexer{}, st.indexers...))
	case r.Method == http.MethodPost && route == "indexer/test":
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost && route == "indexer":
		var in Indexer
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		in.ID = st.nextID
		st.nextID++
		st.indexers = append(st.indexers, in)
		writeJSON(w, http.StatusCreated, in)
	case strings.HasPrefix(route, "indexer/"):
		id, err := strconv.Atoi(strings.TrimPrefix(route, "indexer/"))
		i := slices.IndexFunc(st.indexers, func(x Indexer) bool { return x.ID == id })
		if err != nil || i < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, st.indexers[i])
		case http.MethodPut:
			var in Indexer
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			in.ID = id
			st.indexers[i] = in
			writeJSON(w, http.StatusAccepted, in)
		case http.MethodDelete:
			st.indexers = slices.Delete(st.indexers, i, i+1)
			writeJSON(w, http.StatusOK, map[string]any{})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		http.NotFound(w, r)
	}
}

// schema is the template of a Newznab or Torznab indexer, with every field
// Prowlarr fills in.
func schema(impl string) Indexer {
	i := Indexer{Implementation: impl, ConfigContract: impl + "Settings", EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true, Priority: 25, Tags: []int{}}
	for _, f := range schemaFields {
		i.Fields = append(i.Fields, Field{Name: f})
	}

	return i
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
