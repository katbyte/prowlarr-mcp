//go:build integration

package acceptance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every audit against the fixtures: each finds the defect seeded for it, and
// leaves the healthy indexers, applications and settings alone. Where a tool
// fixes a finding, the fix is applied and the audit run again, so the
// finding and its fix are proved together.

// healthy are the fixtures no audit of indexer health may report.
var healthy = []string{idxUsenet, idxTorrent}

func noneOf(t *testing.T, got []string, subjects ...string) {
	t.Helper()

	for _, f := range got {
		subject, _, _ := strings.Cut(f, "/")
		if slices.Contains(subjects, subject) {
			t.Errorf("%s reported, but it is healthy: %v", f, got)
		}
	}
}

// cleared runs an audit again after a fix and fails if it still reports any
// of the subjects.
func cleared(t *testing.T, audit string, args map[string]any, subjects ...string) {
	t.Helper()

	for _, f := range findings(t, call(t, audit, args)) {
		if subject, _, _ := strings.Cut(f, "/"); slices.Contains(subjects, subject) {
			t.Errorf("%s still reports %s after the fix", audit, f)
		}
	}
}

// retire switches an indexer off, which is the fix several audits name, and
// puts it back when the test ends. The bulk edit is what puts it back:
// saving one indexer makes Prowlarr re-read the site's capabilities, which a
// site that is down cannot answer, and the bulk update writes the field
// without asking the site anything.
func retire(t *testing.T, indexer string) {
	t.Helper()

	call(t, "indexer_edit", map[string]any{"indexer": indexer, "enable": false})
	t.Cleanup(func() {
		if _, err := invoke("indexer_bulk_edit", map[string]any{"indexers": []string{indexer}, "enable": true}); err != nil {
			t.Errorf("%s could not be switched back on, and the fixtures are now wrong for what follows: %v", indexer, err)
		}
	})
}

func TestAuditFailing(t *testing.T) {
	// a search sends each broken site a request, which fails; Prowlarr then
	// holds them back
	search(t, "severance", idxDown, idxRevoked)
	out := call(t, "audit_failing", nil)
	got := findings(t, out)
	for _, name := range []string{idxDown, idxRevoked} {
		held, _ := finding(t, out, name, "held_back_after_failures")
		last, _ := finding(t, out, name, "last_request_failed")
		if held == "" && last == "" {
			t.Errorf("%s is not reported failing: %v", name, got)
		}
	}
	noneOf(t, got, append(healthy, idx1337x, idxMilkie)...)
	if num(t, out["scanned"], "scanned") < len(genericIndexers) {
		t.Errorf("scanned %v, want every enabled indexer", out["scanned"])
	}

	// test=true says why, from the site's own answer
	out = call(t, "audit_failing", map[string]any{"test": true})
	detail, ok := finding(t, out, idxRevoked, "held_back_after_failures")
	if !ok {
		detail, _ = finding(t, out, idxRevoked, "last_request_failed")
	}
	if !strings.Contains(detail, "test says") && !strings.Contains(detail, "passes its test") {
		t.Errorf("test=true did not report the test: %q", detail)
	}

	// the fix, for a site that is not coming back: retire it. (The revoked
	// one, not the one that is down: switching that one off and on again
	// changes whether Prowlarr has its site's capabilities, which
	// TestIndexerEdit reads.)
	retire(t, idxRevoked)
	cleared(t, "audit_failing", nil, idxRevoked)
}

func TestAuditUnreliable(t *testing.T) {
	// the flaky site answers one search in two; the queries differ, because
	// Prowlarr answers a repeated one from its cache without asking the
	// site, and once it fails Prowlarr holds it back and asks no more
	for _, q := range []string{"expanse", "severance", "arrival"} {
		_, _ = invoke("release_search", map[string]any{"query": q, "indexers": []string{idxFlaky, idxTorrent}})
	}
	out := call(t, "audit_unreliable", map[string]any{"min_requests": 2})
	detail, ok := finding(t, out, idxFlaky, "queries_failing")
	if !ok || !strings.Contains(detail, "%") {
		t.Errorf("the flaky indexer is not reported unreliable: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), healthy...)

	// the default wants more requests than a short run makes
	if got := findings(t, call(t, "audit_unreliable", nil)); slices.ContainsFunc(got, func(f string) bool { return strings.HasPrefix(f, idxFlaky+"/") }) {
		t.Errorf("judged on %s's handful of requests with the default minimum: %v", idxFlaky, got)
	}

	// the fix: retiring it takes it out of every search, and out of the audit
	retire(t, idxFlaky)
	cleared(t, "audit_unreliable", map[string]any{"min_requests": 2}, idxFlaky)
}

func TestAuditSlow(t *testing.T) {
	for range 2 {
		search(t, "dune", idxSlow, idxTorrent)
	}
	out := call(t, "audit_slow", map[string]any{"threshold_ms": slowThreshold, "min_queries": 1})
	if _, ok := finding(t, out, idxSlow, "slow_queries"); !ok {
		t.Errorf("the slow indexer is not reported: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), healthy...)
	// at the default five seconds it is not slow
	if _, ok := finding(t, call(t, "audit_slow", nil), idxSlow, "slow_queries"); ok {
		t.Error("reported slow at the default threshold")
	}

	// the fix: retire it rather than make every search wait for it
	retire(t, idxSlow)
	cleared(t, "audit_slow", map[string]any{"threshold_ms": slowThreshold, "min_queries": 1}, idxSlow)
}

func TestAuditUnused(t *testing.T) {
	// a grab from the main torrent indexer, which earns it its keep
	grabFrom(t, idxTorrent, "arrival")
	search(t, "severance", idxUsenet)

	out := call(t, "audit_unused", map[string]any{"min_age_days": 0})
	// the catalogue's indexers are never searched here
	if _, ok := finding(t, out, idx1337x, "never_queried"); !ok {
		t.Errorf("%s is not reported never queried: %v", idx1337x, findings(t, out))
	}
	if _, ok := finding(t, out, idxUsenet, "never_grabbed"); !ok {
		t.Errorf("%s is not reported never grabbed from: %v", idxUsenet, findings(t, out))
	}
	noneOf(t, findings(t, out), idxTorrent)

	// by default the new indexers are left alone
	out = call(t, "audit_unused", nil)
	if n := num(t, out["total_findings"], "total_findings"); n != 0 || !strings.Contains(str(out["note"]), "not judged") {
		t.Errorf("default = %d findings, note %q; want every indexer too new to judge", n, out["note"])
	}

	// the fix the finding names: retire what earns nothing
	retire(t, idx1337x)
	cleared(t, "audit_unused", map[string]any{"min_age_days": 0}, idx1337x)
}

func TestAuditLimits(t *testing.T) {
	call(t, "indexer_edit", map[string]any{"indexer": idxTorrentCopy, "query_limit": 2, "limits_unit": "hour"})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idxTorrentCopy, "query_limit": 0})
	})
	for range 2 {
		search(t, "severance", idxTorrentCopy)
	}
	out := call(t, "audit_limits", nil)
	detail, ok := finding(t, out, idxTorrentCopy, "query_limit_reached")
	if !ok || !strings.Contains(detail, "last hour") {
		t.Errorf("the limit reached is not reported: %v", findings(t, out))
	}
	if num(t, out["scanned"], "scanned") != 1 {
		t.Errorf("scanned %v, want only the indexer with a limit", out["scanned"])
	}

	// a grab, then a grab limit of one: spent as well
	grabFrom(t, idxTorrent, "dune")
	call(t, "indexer_edit", map[string]any{"indexer": idxTorrent, "grab_limit": 1, "limits_unit": "hour"})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idxTorrent, "grab_limit": 0})
	})
	out = call(t, "audit_limits", nil)
	if detail, ok := finding(t, out, idxTorrent, "grab_limit_reached"); !ok || !strings.Contains(detail, "grabs in the last hour") {
		t.Errorf("the grab limit reached is not reported: %q %v", detail, findings(t, out))
	}
	if num(t, out["scanned"], "scanned") != 2 {
		t.Errorf("scanned %v, want the two indexers with a limit", out["scanned"])
	}

	// the fix: a limit the site really allows
	call(t, "indexer_edit", map[string]any{"indexer": idxTorrent, "grab_limit": 100})
	cleared(t, "audit_limits", nil, idxTorrent)
}

func TestAuditUnsynced(t *testing.T) {
	out := call(t, "audit_unsynced", nil)
	detail, ok := finding(t, out, idxMusic, "reaches_no_app")
	if !ok || !strings.Contains(detail, "carries none of them") {
		t.Errorf("the music indexer is not reported reaching no application: %q %v", detail, findings(t, out))
	}
	if detail, ok := finding(t, out, appLidarr, "app_gets_nothing"); !ok || !strings.Contains(detail, tagMusicOnly) {
		t.Errorf("Lidarr is not reported receiving nothing: %q %v", detail, findings(t, out))
	}
	if _, ok := finding(t, out, idxAnime, "profile_uses_nothing"); !ok {
		t.Errorf("the anime indexer on a profile that turns everything off is not reported: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), append(healthy, appSonarr, appRadarr)...)

	// the fix the findings name: the music indexer takes the tag only the
	// music application carries, which answers both of them at once
	call(t, "indexer_edit", map[string]any{"indexer": idxMusic, "add_tags": []string{tagMusicOnly}})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idxMusic, "remove_tags": []string{tagMusicOnly}})
	})
	cleared(t, "audit_unsynced", nil, idxMusic, appLidarr)
}

func TestAuditApps(t *testing.T) {
	out := call(t, "audit_apps", map[string]any{"test": true})
	if _, ok := finding(t, out, appRadarr, "prowlarr_url_local"); !ok {
		t.Errorf("Radarr's localhost Prowlarr URL is not reported: %v", findings(t, out))
	}
	if _, ok := finding(t, out, appLidarr, "test_failed"); !ok {
		t.Errorf("Lidarr, which is not there, does not fail its test: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), appSonarr)

	// the fix, and it is gone
	call(t, "app_edit", map[string]any{"app": appRadarr, "prowlarr_url": "http://" + os.Getenv("PROWLARR_TEST_CONTAINER") + ":9696"})
	t.Cleanup(func() {
		_, _ = invoke("app_edit", map[string]any{"app": appRadarr, "prowlarr_url": "http://localhost:9696"})
	})
	if _, ok := finding(t, call(t, "audit_apps", nil), appRadarr, "prowlarr_url_local"); ok {
		t.Error("still reported after app_edit prowlarr_url")
	}
}

func TestAuditDuplicates(t *testing.T) {
	out := call(t, "audit_duplicates", nil)
	detail, ok := finding(t, out, idxTorrent, "duplicate")
	if !ok || !strings.Contains(detail, idxTorrentCopy) {
		t.Errorf("the copy is not reported: %v", findings(t, out))
	}
	if n := num(t, out["total_findings"], "total_findings"); n != 1 {
		t.Errorf("%d duplicates, want the one pair: %v", n, findings(t, out))
	}

	// a third copy of the same site joins the group, and indexer_delete -
	// the fix - takes it out again
	const third = "Main Torrent Third"
	call(t, "indexer_add", map[string]any{
		"definition": "Generic Torznab", "name": third, "base_url": sitesUp.URL(siteTorrent),
		"settings": map[string]any{"apiKey": siteKeys[siteTorrent]},
	})
	t.Cleanup(func() { _, _ = invoke("indexer_delete", map[string]any{"indexer": third}) })
	if detail, ok := finding(t, call(t, "audit_duplicates", nil), idxTorrent, "duplicate"); !ok || !strings.Contains(detail, third) {
		t.Errorf("the third copy is not in the group: %q", detail)
	}
	call(t, "indexer_delete", map[string]any{"indexer": third})
	if detail, _ := finding(t, call(t, "audit_duplicates", nil), idxTorrent, "duplicate"); strings.Contains(detail, third) {
		t.Errorf("still grouped with the deleted copy: %q", detail)
	}
}

func TestAuditSeeding(t *testing.T) {
	out := call(t, "audit_seeding", nil)
	for _, name := range []string{idxTorrent, idxMilkie} {
		if _, ok := finding(t, out, name, "no_seed_goal"); !ok {
			t.Errorf("%s has no seed goal and is not reported: %v", name, findings(t, out))
		}
	}
	// public, so not judged by default, and usenet never
	noneOf(t, findings(t, out), idx1337x, idxUsenet)
	if _, ok := finding(t, call(t, "audit_seeding", map[string]any{"public": true}), idx1337x, "no_seed_goal"); !ok {
		t.Error("public=true does not judge the public tracker")
	}

	// the fix, on every private torrent indexer at once
	call(t, "indexer_bulk_edit", map[string]any{"protocol": "torrent", "privacy": "private", "seed_ratio": 1.5, "seed_time": 4320})
	if n := num(t, call(t, "audit_seeding", nil)["total_findings"], "total_findings"); n != 0 {
		t.Errorf("%d still without a seed goal after indexer_bulk_edit", n)
	}
	// and the goal reached the indexer's own settings
	settings := object(t, call(t, "indexer_get", map[string]any{"indexer": idxTorrent})["settings"], "settings")
	if settings["torrentBaseSettings.seedRatio"] != 1.5 || settings["torrentBaseSettings.seedTime"] != float64(4320) {
		t.Errorf("settings after the bulk edit = %v", settings)
	}
}

func TestAuditVIP(t *testing.T) {
	out := call(t, "audit_vip", nil)
	if detail, ok := finding(t, out, idxUsenet, "vip_expiring"); !ok || !strings.Contains(detail, "in 5 days") {
		t.Errorf("%s's VIP is not reported running out: %q %v", idxUsenet, detail, findings(t, out))
	}
	if n := num(t, out["scanned"], "scanned"); n != 1 {
		t.Errorf("scanned %d, want only the indexer with a VIP expiry", n)
	}
	// Prowlarr refuses an expiry in the past, so the window is moved instead:
	// with a two day warning, five days away is fine
	if n := num(t, call(t, "audit_vip", map[string]any{"days": 2})["total_findings"], "total_findings"); n != 0 {
		t.Errorf("days=2 found %d, want nothing", n)
	}
	if msg := callErr(t, "indexer_edit", map[string]any{"indexer": idxUsenet, "vip_expiration": vipIn(-3)}); !strings.Contains(msg, "VipExpiration") {
		t.Errorf("an expiry in the past = %q, want Prowlarr's refusal", msg)
	}

	// the fix: renewed, so it is no longer running out
	call(t, "indexer_edit", map[string]any{"indexer": idxUsenet, "vip_expiration": vipIn(90)})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idxUsenet, "vip_expiration": vipIn(5)})
	})
	cleared(t, "audit_vip", nil, idxUsenet)
}

func TestAuditDefinitions(t *testing.T) {
	out := call(t, "audit_definitions", nil)
	if detail, ok := finding(t, out, idx1337x, "retired_url"); !ok || !strings.Contains(detail, currentURL1337x) {
		t.Errorf("1337x on a retired address is not reported with the current one: %q %v", detail, findings(t, out))
	}
	if _, ok := finding(t, out, idxMilkie, "unlisted_url"); !ok {
		t.Errorf("Milkie on an address its definition does not list is not reported: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), idxTasman)

	// the definition taken away, the way Prowlarr's catalogue drops one
	file := filepath.Join(definitionsDir(), "acrossthetasman.yml")
	data, err := os.ReadFile(file) //nolint:gosec // the test's own fixture
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(file, data, 0o644) //nolint:gosec // readable by the container, as testenv.sh wrote it
		_, _ = invoke("task_run", map[string]any{"name": "IndexerDefinitionUpdate"})
	})
	// the definitions update is what changes the catalogue (offline it only
	// rereads the folder), and running it drops the tools' kept copy
	call(t, "task_run", map[string]any{"name": "IndexerDefinitionUpdate"})
	out = call(t, "audit_definitions", nil)
	if detail, ok := finding(t, out, idxTasman, "definition_gone"); !ok || !strings.Contains(detail, "acrossthetasman") {
		t.Errorf("the indexer whose definition is gone is not reported: %v", findings(t, out))
	}

	// the fix the retired address names, with the address it names: forced,
	// because neither address answers from a container with no internet.
	// A definition that is gone is fixed by replacing the indexer, which is
	// indexer_add and indexer_delete, proved on their own.
	call(t, "indexer_edit", map[string]any{"indexer": idx1337x, "base_url": currentURL1337x, "force": true})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idx1337x, "base_url": retiredURL1337x, "force": true})
	})
	cleared(t, "audit_definitions", nil, idx1337x)
}

func TestAuditProxies(t *testing.T) {
	out := call(t, "audit_proxies", map[string]any{"test": true})
	if _, ok := finding(t, out, idx1337x, "needs_flaresolverr"); !ok {
		t.Errorf("1337x, behind Cloudflare with no FlareSolverr, is not reported: %v", findings(t, out))
	}
	if _, ok := finding(t, out, proxyIdle, "proxy_unused"); !ok {
		t.Errorf("the idle proxy is not reported unused: %v", findings(t, out))
	}
	if _, ok := finding(t, out, proxyFlare, "proxy_failing"); !ok {
		t.Errorf("FlareSolverr, which is not there, does not fail its test: %v", findings(t, out))
	}

	// the fix: 1337x takes the FlareSolverr proxy's tag, which also puts the
	// proxy to use
	call(t, "indexer_edit", map[string]any{"indexer": idx1337x, "add_tags": []string{tagFlaresolverr}, "force": true})
	t.Cleanup(func() {
		_, _ = invoke("indexer_edit", map[string]any{"indexer": idx1337x, "remove_tags": []string{tagFlaresolverr}, "force": true})
	})
	got := findings(t, call(t, "audit_proxies", nil))
	for _, gone := range []string{idx1337x + "/needs_flaresolverr", proxyFlare + "/proxy_unused"} {
		if slices.Contains(got, gone) {
			t.Errorf("%s still reported after the tag was added: %v", gone, got)
		}
	}
}

func TestAuditDownloadClients(t *testing.T) {
	out := call(t, "audit_download_clients", map[string]any{"test": true})
	if _, ok := finding(t, out, clientUsenet, "blackhole_takes_no_usenet"); !ok {
		t.Errorf("the usenet blackhole is not reported: %v", findings(t, out))
	}
	if detail, ok := finding(t, out, idxTorrentCopy, "pinned_client_disabled"); !ok || !strings.Contains(detail, clientSpare) {
		t.Errorf("the copy pinned to a disabled client is not reported: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), clientTorrent)

	// the fix the finding names: switch the client back on
	call(t, "downloadclient_edit", map[string]any{"client": clientSpare, "enable": true})
	t.Cleanup(func() {
		_, _ = invoke("downloadclient_edit", map[string]any{"client": clientSpare, "enable": false})
	})
	cleared(t, "audit_download_clients", nil, idxTorrentCopy)
}

func TestAuditTags(t *testing.T) {
	out := call(t, "audit_tags", nil)
	if _, ok := finding(t, out, tagStray, "tag_unused"); !ok {
		t.Errorf("the stray tag is not reported: %v", findings(t, out))
	}
	if detail, ok := finding(t, out, tagMusicOnly, "app_tag_matches_nothing"); !ok || !strings.Contains(detail, appLidarr) {
		t.Errorf("Lidarr's tag no indexer carries is not reported: %v", findings(t, out))
	}
	// a proxy's tag is not stray
	noneOf(t, findings(t, out), tagIdle, tagMovies)

	// the fix: tag_delete, and the tag is gone from the worklist
	call(t, "tag_delete", map[string]any{"label": tagStray})
	t.Cleanup(func() { _, _ = invoke("tag_create", map[string]any{"label": tagStray}) })
	cleared(t, "audit_tags", nil, tagStray)
}

func TestAuditProfiles(t *testing.T) {
	out := call(t, "audit_profiles", nil)
	if _, ok := finding(t, out, profileNothing, "profile_uses_nothing"); !ok {
		t.Errorf("the profile that turns everything off is not reported: %v", findings(t, out))
	}
	if _, ok := finding(t, out, profileSpare, "profile_unused"); !ok {
		t.Errorf("the spare profile is not reported unused: %v", findings(t, out))
	}
	noneOf(t, findings(t, out), "Standard")

	// both fixes: turn a search on in the one that uses nothing, and delete
	// the one nothing uses
	call(t, "profile_edit", map[string]any{"profile": profileNothing, "rss": true})
	t.Cleanup(func() {
		_, _ = invoke("profile_edit", map[string]any{"profile": profileNothing, "rss": false})
	})
	call(t, "profile_delete", map[string]any{"profile": profileSpare})
	t.Cleanup(func() {
		_, _ = invoke("profile_create", map[string]any{"name": profileSpare})
	})
	cleared(t, "audit_profiles", nil, profileNothing, profileSpare)
}

func TestAuditHealth(t *testing.T) {
	out := call(t, "audit_health", nil)
	if n := num(t, out["total_findings"], "total_findings"); n == 0 {
		t.Fatalf("no health findings, but Prowlarr warns about its allowed hosts at least: %v", out)
	}
	for _, f := range rows(t, out["findings"], "findings") {
		if p := str(f["problem"]); p != "warning" && p != "error" {
			t.Errorf("a %s among warnings and errors", p)
		}
	}
	if _, ok := finding(t, out, "AllowedHostsCheck", "warning"); !ok {
		t.Errorf("the allowed hosts warning is missing: %v", findings(t, out))
	}

	// most of Prowlarr's checks are about the indexers, and those the tools
	// do fix: a failed request raises the indexer check, and retiring what
	// it names clears it on the next run. (The allowed hosts warning is a
	// server setting, which these tools deliberately do not write.)
	// ignored: with every indexer it names already held back, the search
	// itself is refused, which is the state the check is about
	_, _ = invoke("release_search", map[string]any{"query": "severance", "indexers": []string{idxDown, idxRevoked}})
	call(t, "task_run", map[string]any{"name": "CheckHealth"})
	detail, ok := finding(t, call(t, "audit_health", nil), "IndexerStatusCheck", "warning")
	if !ok || !strings.Contains(detail, idxRevoked) {
		t.Fatalf("a failing indexer does not raise Prowlarr's indexer check: %q %v", detail, findings(t, call(t, "audit_health", nil)))
	}
	retire(t, idxRevoked)
	call(t, "task_run", map[string]any{"name": "CheckHealth"})
	// the check itself stays while other indexers are failing; what changes
	// is that it no longer names this one
	if detail, _ := finding(t, call(t, "audit_health", nil), "IndexerStatusCheck", "warning"); strings.Contains(detail, idxRevoked) {
		t.Errorf("the check still names the retired indexer: %q", detail)
	}
}

// audit_all is the audits' counts: each row matches the audit run on its
// own with the same defaults.
func TestAuditAll(t *testing.T) {
	out := call(t, "audit_all", nil)
	auditRows := rows(t, out["audits"], "audits")
	var names []string
	total := 0
	for _, row := range auditRows {
		name := str(row["audit"])
		names = append(names, name)
		total += num(t, row["findings"], "findings")
		if name == "audit_failing" {
			continue // Prowlarr's hold on a failing indexer can lapse between the two calls
		}
		alone := num(t, call(t, name, nil)["total_findings"], "total_findings")
		if got := num(t, row["findings"], "findings"); got != alone {
			t.Errorf("audit_all says %s found %d, alone it finds %d", name, got, alone)
		}
	}
	if total != num(t, out["total_findings"], "total_findings") {
		t.Errorf("total_findings %v is not the sum of the rows, %d", out["total_findings"], total)
	}
	// every audit tool is in it
	for _, tool := range toolNamesWith(t, "audit_") {
		if tool != "audit_all" && !slices.Contains(names, tool) {
			t.Errorf("%s is not in audit_all", tool)
		}
	}
}

// toolNamesWith lists the registered tools with a prefix.
func toolNamesWith(t *testing.T, prefix string) []string {
	t.Helper()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, prefix) {
			out = append(out, tool.Name)
		}
	}

	return out
}

// grabFrom searches one indexer and grabs the first release found.
func grabFrom(t *testing.T, indexer, query string) map[string]any {
	t.Helper()

	found := rows(t, search(t, query, indexer)["releases"], "releases")
	if len(found) == 0 {
		t.Fatalf("searching %s for %q found nothing to grab", indexer, query)
	}

	return call(t, "release_grab", map[string]any{"indexer": indexer, "guid": str(found[0]["guid"])})
}
