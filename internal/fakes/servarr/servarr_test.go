package servarr

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func start(t *testing.T) *Server {
	t.Helper()

	s, err := New(Options{Apps: []App{{Name: "sonarr", Kind: Sonarr, APIKey: "k"}, {Name: "lidarr", Kind: Lidarr, APIKey: "l"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func do(t *testing.T, s *Server, method, path, key string, body any) (status int, out []byte, header http.Header) {
	t.Helper()

	var in io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL("")+path, in)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ = io.ReadAll(resp.Body)

	return resp.StatusCode, out, resp.Header
}

func TestStatusAndKey(t *testing.T) {
	t.Parallel()

	s := start(t)
	status, body, hdr := do(t, s, http.MethodGet, "sonarr/api/v3/system/status", "k", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"version":"4.`) || hdr.Get("X-Application-Version") == "" {
		t.Errorf("status = %d %s %v", status, body, hdr)
	}
	if status, _, _ := do(t, s, http.MethodGet, "sonarr/api/v3/system/status", "wrong", nil); status != http.StatusUnauthorized {
		t.Errorf("a wrong key = %d", status)
	}
	// Lidarr speaks v1
	if status, _, _ := do(t, s, http.MethodGet, "lidarr/api/v1/system/status", "l", nil); status != http.StatusOK {
		t.Errorf("lidarr v1 = %d", status)
	}
	if status, _, _ := do(t, s, http.MethodGet, "lidarr/api/v3/system/status", "l", nil); status != http.StatusNotFound {
		t.Errorf("lidarr v3 = %d", status)
	}
	s.SetDown("sonarr", true)
	if status, _, _ := do(t, s, http.MethodGet, "sonarr/api/v3/system/status", "k", nil); status != http.StatusServiceUnavailable {
		t.Errorf("down = %d", status)
	}
}

func TestIndexerLifecycle(t *testing.T) {
	t.Parallel()

	s := start(t)
	// empty is [], never null
	if _, body, _ := do(t, s, http.MethodGet, "sonarr/api/v3/indexer", "k", nil); strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("no indexers = %s", body)
	}
	var schema []Indexer
	_, body, _ := do(t, s, http.MethodGet, "sonarr/api/v3/indexer/schema", "k", nil)
	if err := json.Unmarshal(body, &schema); err != nil || len(schema) != 2 || schema[1].Implementation != "Torznab" {
		t.Fatalf("schema = %s", body)
	}

	in := Indexer{Name: "Main (Prowlarr)", Implementation: "Torznab", Fields: []Field{{Name: "baseUrl", Value: "http://prowlarr/1/"}, {Name: "categories", Value: []int{5000, 5040}}}}
	status, body, _ := do(t, s, http.MethodPost, "sonarr/api/v3/indexer", "k", in)
	var made Indexer
	if err := json.Unmarshal(body, &made); err != nil || status != http.StatusCreated || made.ID != 1 {
		t.Fatalf("create = %d %s", status, body)
	}
	got := s.Indexers("sonarr")
	if len(got) != 1 || got[0].Value("baseUrl") != "http://prowlarr/1/" || len(got[0].Categories()) != 2 {
		t.Errorf("held = %+v", got)
	}

	in.Name = "Renamed (Prowlarr)"
	if status, _, _ := do(t, s, http.MethodPut, "sonarr/api/v3/indexer/1", "k", in); status != http.StatusAccepted || s.Indexers("sonarr")[0].Name != "Renamed (Prowlarr)" {
		t.Errorf("update = %d", status)
	}
	if status, _, _ := do(t, s, http.MethodGet, "sonarr/api/v3/indexer/1", "k", nil); status != http.StatusOK {
		t.Errorf("read one = %d", status)
	}
	if status, _, _ := do(t, s, http.MethodDelete, "sonarr/api/v3/indexer/1", "k", nil); status != http.StatusOK || len(s.Indexers("sonarr")) != 0 {
		t.Errorf("delete = %d", status)
	}
	if status, _, _ := do(t, s, http.MethodGet, "sonarr/api/v3/indexer/9", "k", nil); status != http.StatusNotFound {
		t.Errorf("an unknown indexer = %d", status)
	}
	if status, _, _ := do(t, s, http.MethodPost, "sonarr/api/v3/indexer/test", "k", in); status != http.StatusOK {
		t.Errorf("test = %d", status)
	}
	if calls := s.Calls("sonarr"); len(calls) != 8 || calls[0] != "GET /api/v3/indexer" {
		t.Errorf("calls = %v", calls)
	}
}

func TestAddValidates(t *testing.T) {
	t.Parallel()

	s := start(t)
	for _, bad := range []App{{Name: "", Kind: Sonarr}, {Name: "x", Kind: "Plex"}} {
		if err := s.Add(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if s.Indexers("nope") != nil || s.Calls("nope") != nil {
		t.Error("an unknown application holds something")
	}
	if status, _, _ := do(t, s, http.MethodGet, "nope/api/v3/system/status", "k", nil); status != http.StatusNotFound {
		t.Errorf("an unknown application = %d", status)
	}
}
