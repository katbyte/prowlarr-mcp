// Package newznab is a fake Newznab and Torznab indexer for the live suites:
// one listener serving any number of sites, each at /<site>/api, each usenet
// or torrent, and each healthy or broken in its own way.
//
// Prowlarr adds a site the way it adds any generic Newznab or Torznab
// indexer, asks it for its capabilities, searches it, and fetches the NZB or
// torrent of what is grabbed. Every answer is served in the shape Prowlarr's
// own parsers read (NewznabCapabilitiesProvider, NewznabRssParser,
// TorznabRssParser in Prowlarr's source), so a test controls exactly which
// releases exist, sees every request Prowlarr made, and can make a site fail,
// fail now and then, answer slowly or refuse its key, which is what drives
// Prowlarr's failure records and statistics - and so the audits - for real.
//
// It runs on the host and the container reaches it through
// host.docker.internal, so the links in its feeds are built from the address
// the container uses (Options.PublicHost), not the one the test dialled.
package newznab

import (
	"context"
	"crypto/sha1" //nolint:gosec // a stable GUID and info hash for a release title, not a security boundary
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Protocols a site speaks.
const (
	Usenet  = "usenet"
	Torrent = "torrent"
)

// The standard categories the fake sites carry by default. A release is
// filed under one subcategory; a search for the parent finds it.
const (
	CategoryMovies   = 2000
	CategoryMoviesHD = 2040
	CategoryAudio    = 3000
	CategoryTV       = 5000
	CategoryTVHD     = 5040
	CategoryTVAnime  = 5070
	CategoryBooks    = 7000
)

// The Newznab error codes a site can answer with. Prowlarr reads 100-199 as a
// rejected key and 500 as a request limit.
const (
	ErrIncorrectCredentials = 100
	ErrRequestLimitReached  = 500
	ErrUnknown              = 900
)

// Category is a category a site's capabilities list, with its
// subcategories.
type Category struct {
	ID   int
	Name string
	Subs []Category
}

// DefaultCategories are movies and TV with their HD subcategories, which is
// what both Sonarr's and Radarr's default sync categories need.
var DefaultCategories = []Category{
	{ID: CategoryMovies, Name: "Movies", Subs: []Category{{ID: CategoryMoviesHD, Name: "Movies/HD"}}},
	{ID: CategoryTV, Name: "TV", Subs: []Category{{ID: CategoryTVHD, Name: "TV/HD"}, {ID: CategoryTVAnime, Name: "TV/Anime"}}},
}

// Release is one entry in a site's catalogue.
type Release struct {
	// GUID identifies the release in its links; empty derives a stable one
	// from the title.
	GUID  string
	Title string
	// Size is the release size in bytes; 0 is a gigabyte.
	Size int64
	// PubDate is when it was posted; zero is an hour before the site started.
	PubDate time.Time
	// Category is the subcategory it is filed under; 0 is TV/HD.
	Category int
	// Seeders and Peers are what a torrent site reports; Grabs what any does.
	Seeders int
	Peers   int
	Grabs   int
	// IMDBID, TMDBID and TVDBID are ids a search by id matches.
	IMDBID int
	TMDBID int
	TVDBID int
	// Freeleech marks a torrent that costs no download ratio.
	Freeleech bool
}

// Failure is how a site misbehaves; the zero value is healthy.
type Failure struct {
	// Status, when set, is the HTTP status every call answers, the way a
	// site that is down answers a 503.
	Status int
	// Code, when set and Status is not, answers every call with a Newznab
	// error document under HTTP 200: a key the site has revoked
	// (ErrIncorrectCredentials) or a request limit (ErrRequestLimitReached).
	Code        int
	Description string
	// Every, when above 1, fails only every Every-th search, the way a flaky
	// site does, and answers the rest; capabilities are always answered, so
	// Prowlarr can still add and test the site. A failure lasts failureSpell,
	// long enough to outlast the two retries Prowlarr makes of a server
	// error, which would otherwise hide it: only a failure that outlasts them
	// is recorded, which is true of a real site too.
	Every int
}

// failureSpell is how long a flaky site's failure lasts once it starts.
const failureSpell = 12 * time.Second

// Site is one fake indexer.
type Site struct {
	// Name is the site's path: its API is at /<Name>/api.
	Name string
	// Protocol is Usenet or Torrent.
	Protocol string
	// APIKey is the key every call must carry as apikey=; empty accepts any.
	APIKey string
	// Categories are what its capabilities list; nil is DefaultCategories.
	Categories []Category
	// Searches are the kinds of search it offers - search, tv-search,
	// movie-search, music-search, book-search; nil is search, TV and movie.
	Searches []string
	// Releases is its catalogue.
	Releases []Release
	// Delay holds every answer back this long, the way a slow site does.
	Delay time.Duration
	// Failure is how it misbehaves.
	Failure Failure
}

// Request is one call a site received.
type Request struct {
	Site string
	// Function is the t= parameter (caps, search, tvsearch, movie...), or
	// "download" for a fetch of a release.
	Function string
	Query    url.Values
	Time     time.Time
}

// Options configure a Server.
type Options struct {
	// Addr is where to listen, e.g. ":19691"; empty picks a free port on
	// 127.0.0.1. The container reaches the host through
	// host.docker.internal, so a live run listens on every interface.
	Addr string
	// PublicHost is the host the container reaches this process by, which
	// the feeds' links are built from; default 127.0.0.1.
	PublicHost string
	// Sites are the sites to serve.
	Sites []Site
}

// Server is a running fake indexer.
type Server struct {
	publicHost string
	listener   net.Listener
	srv        *http.Server
	started    time.Time

	mu        sync.Mutex
	sites     map[string]*Site
	searches  map[string]int
	failUntil map[string]time.Time
	requests  []Request
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
		return nil, fmt.Errorf("newznab: listening on %s: %w", opts.Addr, err)
	}
	s := &Server{
		publicHost: opts.PublicHost, listener: ln, started: time.Now(),
		sites: map[string]*Site{}, searches: map[string]int{}, failUntil: map[string]time.Time{},
	}
	for i := range opts.Sites {
		if err := s.Add(opts.Sites[i]); err != nil {
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

// URL is a site's address as the container reaches it: what Prowlarr is
// given as the indexer's base URL, with /api as its API path.
func (s *Server) URL(site string) string {
	return "http://" + net.JoinHostPort(s.publicHost, strconv.Itoa(s.Port())) + "/" + site // a fake on the host, never TLS
}

// Add adds a site, or replaces one of the same name.
func (s *Server) Add(site Site) error {
	switch {
	case site.Name == "" || strings.ContainsAny(site.Name, "/?#"):
		return fmt.Errorf("newznab: %q is not a site name", site.Name)
	case site.Protocol != Usenet && site.Protocol != Torrent:
		return fmt.Errorf("newznab: site %s: protocol must be usenet or torrent, got %q", site.Name, site.Protocol)
	}
	if site.Categories == nil {
		site.Categories = DefaultCategories
	}
	if site.Searches == nil {
		site.Searches = []string{"search", "tv-search", "movie-search"}
	}
	site.Releases = slices.Clone(site.Releases)
	for i := range site.Releases {
		s.defaults(&site.Releases[i])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sites[site.Name] = &site

	return nil
}

func (s *Server) defaults(r *Release) {
	if r.GUID == "" {
		sum := sha1.Sum([]byte(r.Title)) //nolint:gosec // an id, not a secret
		r.GUID = hex.EncodeToString(sum[:10])
	}
	if r.Size == 0 {
		r.Size = 1 << 30
	}
	if r.PubDate.IsZero() {
		r.PubDate = s.started.Add(-time.Hour)
	}
	if r.Category == 0 {
		r.Category = CategoryTVHD
	}
}

// SetFailure changes how a site misbehaves.
func (s *Server) SetFailure(site string, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sites[site]; x != nil {
		x.Failure = f
		s.searches[site] = 0
		delete(s.failUntil, site)
	}
}

// SetDelay changes how long a site holds its answers back.
func (s *Server) SetDelay(site string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sites[site]; x != nil {
		x.Delay = d
	}
}

// Requests are the calls one site received, in order; every site's for
// an empty name.
func (s *Server) Requests(site string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.requests {
		if site == "" || r.Site == site {
			out = append(out, r)
		}
	}

	return out
}

// Downloads is how many times a site's releases were fetched.
func (s *Server) Downloads(site string) int {
	n := 0
	for _, r := range s.Requests(site) {
		if r.Function == "download" {
			n++
		}
	}

	return n
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	name, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	site, known := s.sites[name]
	var snapshot Site
	if known {
		snapshot = *site
	}
	s.mu.Unlock()
	if !known {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	fn := q.Get("t")
	if strings.HasPrefix(rest, "download/") {
		fn = "download"
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Site: name, Function: fn, Query: q, Time: time.Now()})
	s.mu.Unlock()

	if snapshot.Delay > 0 {
		select {
		case <-time.After(snapshot.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if s.misbehave(w, &snapshot, fn) {
		return
	}
	if snapshot.APIKey != "" && q.Get("apikey") != snapshot.APIKey && fn != "download" {
		writeError(w, ErrIncorrectCredentials, "Incorrect user credentials")
		return
	}

	switch {
	case fn == "download":
		s.download(w, &snapshot, strings.TrimPrefix(rest, "download/"))
	case rest != "api":
		http.NotFound(w, r)
	case fn == "caps":
		writeXML(w, capsXML(&snapshot))
	case fn == "search" || fn == "tvsearch" || fn == "movie" || fn == "music" || fn == "book":
		writeXML(w, s.feed(&snapshot, q))
	default:
		writeError(w, 202, "No such function ("+fn+")")
	}
}

// misbehave answers for a site that is failing, and reports whether it did;
// false means answer normally.
func (s *Server) misbehave(w http.ResponseWriter, site *Site, fn string) bool {
	f := site.Failure
	if f.Status == 0 && f.Code == 0 {
		return false
	}
	if f.Every > 1 {
		if fn == "caps" || fn == "download" {
			return false
		}
		s.mu.Lock()
		failing := time.Now().Before(s.failUntil[site.Name])
		if !failing {
			s.searches[site.Name]++
			if s.searches[site.Name]%f.Every == 0 {
				s.failUntil[site.Name] = time.Now().Add(failureSpell)
				failing = true
			}
		}
		s.mu.Unlock()
		if !failing {
			return false
		}
	}
	if f.Status != 0 {
		http.Error(w, http.StatusText(f.Status), f.Status)
		return true
	}
	desc := f.Description
	if desc == "" {
		desc = "Unknown error"
	}
	writeError(w, f.Code, desc)

	return true
}

func (s *Server) base(site *Site) string { return s.URL(site.Name) }

func (s *Server) download(w http.ResponseWriter, site *Site, guid string) {
	i := slices.IndexFunc(site.Releases, func(r Release) bool { return r.GUID == guid })
	if i < 0 {
		writeError(w, 300, "No such item")
		return
	}
	rel := &site.Releases[i]
	s.mu.Lock()
	if x := s.sites[site.Name]; x != nil && i < len(x.Releases) {
		x.Releases[i].Grabs++
	}
	s.mu.Unlock()
	if site.Protocol == Torrent {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.Title+".torrent"))
		_, _ = w.Write(torrentFile(rel))
		return
	}
	w.Header().Set("Content-Type", "application/x-nzb")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.Title+".nzb"))
	_, _ = w.Write([]byte(nzbFile(rel)))
}

// writeXML answers a document the callers have already built. Anything in it
// that came from the request went through xmlEscape on the way, which is
// what makes the write safe; a taint check cannot see that, hence the
// exemption. This server only ever runs in tests, on the host, for Prowlarr
// to read.
func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(body)) //nolint:gosec // escaped by the callers; a fake server for the tests
}

func writeError(w http.ResponseWriter, code int, desc string) {
	writeXML(w, fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<error code="%d" description=%q/>`, code, xmlEscape(desc)))
}
