package newznab

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T, sites ...Site) *Server {
	t.Helper()

	s, err := New(Options{Sites: sites})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func get(t *testing.T, s *Server, site string, q url.Values) (status int, body string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL(site)+"/api?"+q.Encode(), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}

var catalogue = []Release{
	{Title: "Severance.S01E01.1080p.WEB-DL-FAKE", Category: CategoryTVHD, Seeders: 10, Peers: 2, TVDBID: 371980},
	{Title: "Dune.Part.Two.2024.1080p.BluRay-FAKE", Category: CategoryMoviesHD, Seeders: 90, TMDBID: 693134, IMDBID: 15239678, Freeleech: true, PubDate: time.Now()},
}

type rss struct {
	Items []struct {
		Title     string `xml:"title"`
		GUID      string `xml:"guid"`
		Link      string `xml:"link"`
		Enclosure struct {
			URL  string `xml:"url,attr"`
			Type string `xml:"type,attr"`
		} `xml:"enclosure"`
		Attrs []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"attr"`
	} `xml:"channel>item"`
}

func parse(t *testing.T, body string) rss {
	t.Helper()

	var feed rss
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatalf("not RSS: %v\n%s", err, body)
	}

	return feed
}

func TestCaps(t *testing.T) {
	t.Parallel()

	s := start(t, Site{Name: "tv", Protocol: Torrent, Searches: []string{"search", "tv-search"}, Categories: []Category{{ID: CategoryTV, Name: "TV", Subs: []Category{{ID: CategoryTVHD, Name: "TV/HD"}}}}})
	status, body := get(t, s, "tv", url.Values{"t": {"caps"}})
	if status != http.StatusOK {
		t.Fatalf("caps = %d", status)
	}
	var caps struct {
		Searching struct {
			TV struct {
				Available string `xml:"available,attr"`
			} `xml:"tv-search"`
			Movie struct {
				Available string `xml:"available,attr"`
			} `xml:"movie-search"`
		} `xml:"searching"`
		Categories []struct {
			ID   int `xml:"id,attr"`
			Subs []struct {
				ID int `xml:"id,attr"`
			} `xml:"subcat"`
		} `xml:"categories>category"`
	}
	if err := xml.Unmarshal([]byte(body), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Searching.TV.Available != "yes" || caps.Searching.Movie.Available != "no" || len(caps.Categories) != 1 || caps.Categories[0].Subs[0].ID != CategoryTVHD {
		t.Errorf("caps = %+v", caps)
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()

	s := start(t, Site{Name: "tor", Protocol: Torrent, APIKey: "k", Releases: catalogue}, Site{Name: "nzb", Protocol: Usenet, Releases: catalogue})
	// everything, newest first
	_, body := get(t, s, "tor", url.Values{"t": {"search"}, "apikey": {"k"}})
	feed := parse(t, body)
	if len(feed.Items) != 2 || !strings.HasPrefix(feed.Items[0].Title, "Dune") {
		t.Fatalf("items = %+v", feed.Items)
	}
	attrs := map[string]string{}
	for _, a := range feed.Items[0].Attrs {
		attrs[a.Name] = a.Value
	}
	if attrs["seeders"] != "90" || attrs["peers"] != "90" || attrs["downloadvolumefactor"] != "0" || attrs["infohash"] == "" || attrs["tmdbid"] != "693134" {
		t.Errorf("torznab attributes = %v", attrs)
	}
	if feed.Items[0].Enclosure.Type != "application/x-bittorrent" || !strings.HasPrefix(feed.Items[0].Link, s.URL("tor")+"/download/") {
		t.Errorf("item = %+v", feed.Items[0])
	}

	// words, categories (the parent finds a subcategory) and ids
	for q, want := range map[string]int{
		"q=severance":               1,
		"q=nothing":                 0,
		"cat=2000":                  1,
		"cat=5040,2040":             2,
		"t=movie&imdbid=tt15239678": 1,
		"t=tvsearch&tvdbid=1":       0,
	} {
		v, _ := url.ParseQuery(q)
		if v.Get("t") == "" {
			v.Set("t", "search")
		}
		v.Set("apikey", "k")
		_, found := get(t, s, "tor", v)
		if got := len(parse(t, found).Items); got != want {
			t.Errorf("%s found %d, want %d", q, got, want)
		}
	}

	// usenet enclosures are NZBs
	_, body = get(t, s, "nzb", url.Values{"t": {"search"}})
	if feed := parse(t, body); feed.Items[0].Enclosure.Type != "application/x-nzb" {
		t.Errorf("usenet enclosure = %+v", feed.Items[0].Enclosure)
	}
	if got := len(s.Requests("tor")); got != 7 {
		t.Errorf("%d requests recorded, want 7", got)
	}
}

func TestKeyAndFailures(t *testing.T) {
	t.Parallel()

	s := start(t, Site{Name: "x", Protocol: Usenet, APIKey: "right", Releases: catalogue})
	if _, body := get(t, s, "x", url.Values{"t": {"search"}, "apikey": {"wrong"}}); !strings.Contains(body, `code="100"`) {
		t.Errorf("a wrong key = %s", body)
	}

	s.SetFailure("x", Failure{Status: http.StatusServiceUnavailable})
	if status, _ := get(t, s, "x", url.Values{"t": {"caps"}, "apikey": {"right"}}); status != http.StatusServiceUnavailable {
		t.Errorf("a site that is down answered %d", status)
	}
	s.SetFailure("x", Failure{Code: ErrRequestLimitReached, Description: "Request limit reached"})
	if _, body := get(t, s, "x", url.Values{"t": {"search"}, "apikey": {"right"}}); !strings.Contains(body, `code="500"`) || !strings.Contains(body, "Request limit") {
		t.Errorf("a request limit = %s", body)
	}

	// flaky: every second search starts a spell of failures, which outlasts
	// a retry; capabilities are answered throughout
	s.SetFailure("x", Failure{Status: http.StatusBadGateway, Every: 2})
	statuses := make([]int, 0, 3)
	for range 3 {
		status, _ := get(t, s, "x", url.Values{"t": {"search"}, "apikey": {"right"}})
		statuses = append(statuses, status)
	}
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusBadGateway || statuses[2] != http.StatusBadGateway {
		t.Errorf("flaky statuses = %v", statuses)
	}
	if status, _ := get(t, s, "x", url.Values{"t": {"caps"}, "apikey": {"right"}}); status != http.StatusOK {
		t.Errorf("caps from a flaky site = %d", status)
	}
	// healthy again
	s.SetFailure("x", Failure{})
	if status, _ := get(t, s, "x", url.Values{"t": {"search"}, "apikey": {"right"}}); status != http.StatusOK {
		t.Errorf("after the failure is cleared = %d", status)
	}
}

func TestDelay(t *testing.T) {
	t.Parallel()

	s := start(t, Site{Name: "slow", Protocol: Torrent, Delay: 300 * time.Millisecond})
	began := time.Now()
	get(t, s, "slow", url.Values{"t": {"search"}})
	if d := time.Since(began); d < 300*time.Millisecond {
		t.Errorf("answered in %v", d)
	}
	s.SetDelay("slow", 0)
	began = time.Now()
	get(t, s, "slow", url.Values{"t": {"search"}})
	if d := time.Since(began); d > 250*time.Millisecond {
		t.Errorf("answered in %v with no delay", d)
	}
}

func TestDownloads(t *testing.T) {
	t.Parallel()

	s := start(t, Site{Name: "tor", Protocol: Torrent, Releases: catalogue}, Site{Name: "nzb", Protocol: Usenet, Releases: catalogue})
	fetch := func(site string) (string, string) {
		_, body := get(t, s, site, url.Values{"t": {"search"}, "q": {"dune"}})
		link := parse(t, body).Items[0].Link
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, link, http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.Header.Get("Content-Type"), string(b)
	}
	ct, torrent := fetch("tor")
	// bencoded, with a piece hash for every piece of the file
	if ct != "application/x-bittorrent" || !strings.HasPrefix(torrent, "d8:announce") || !strings.Contains(torrent, "6:pieces5120:") {
		t.Errorf("torrent = %s %q", ct, torrent[:min(80, len(torrent))])
	}
	ct, nzb := fetch("nzb")
	if ct != "application/x-nzb" || !strings.Contains(nzb, "<nzb") {
		t.Errorf("nzb = %s %s", ct, nzb)
	}
	if s.Downloads("tor") != 1 || s.Downloads("nzb") != 1 {
		t.Errorf("downloads = %d, %d", s.Downloads("tor"), s.Downloads("nzb"))
	}
	if status, body := get(t, s, "nope", url.Values{"t": {"caps"}}); status != http.StatusNotFound {
		t.Errorf("an unknown site = %d %s", status, body)
	}
}

func TestAddValidates(t *testing.T) {
	t.Parallel()

	s := start(t)
	for _, bad := range []Site{{Name: "", Protocol: Usenet}, {Name: "a/b", Protocol: Usenet}, {Name: "x", Protocol: "ftp"}} {
		if err := s.Add(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if s.Port() == 0 || !strings.HasPrefix(s.URL("x"), "http://127.0.0.1:") {
		t.Errorf("url = %s", s.URL("x"))
	}
}
