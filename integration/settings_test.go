//go:build integration

package integration

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// settingsFile is every setting of every kind of indexer, application,
// download client, proxy and notification, as the Prowlarr the suites run
// against declares it: a line each of family, name, type and privacy. The
// tools' unit tests read it and refuse a setting that is neither hidden as a
// credential nor on the list of ones somebody has looked at.
const settingsFile = "../tools/testdata/settings.txt"

const settingsHeader = "# Every setting of every kind of provider, as Prowlarr declares it: family, name, type, privacy.\n# Written from a real Prowlarr by the client suite: PROWLARR_TEST_WRITE_SETTINGS=1 make testacc-integration\n"

// What is a credential is Prowlarr's to mark, and it leaves some unmarked.
// The tools' rule for those is checked against the list in settingsFile, so
// that list has to be what this Prowlarr declares: a setting a later release
// adds fails here until the list is written again and the new line looked at.
func TestEverySettingIsOnTheListThatWasLookedAt(t *testing.T) {
	need(t)

	var declared []string
	family := func(name string, kinds [][]prowlarr.Field, err error) {
		if err != nil || len(kinds) == 0 {
			t.Fatalf("%s kinds = %d, %v", name, len(kinds), err)
		}

		for _, fields := range kinds {
			for _, f := range fields {
				declared = append(declared, fmt.Sprintf("%s %s %s %s", name, f.Name, f.Type, f.Privacy))
			}
		}
	}

	indexers, err := sdk.GetIndexerSchema(ctx)
	family("indexer", fieldsOf(indexers.Model, func(x prowlarr.IndexerResource) []prowlarr.Field { return x.Fields }), err)
	applications, err := sdk.GetApplicationsSchema(ctx)
	family("application", fieldsOf(applications.Model, func(x prowlarr.ApplicationResource) []prowlarr.Field { return x.Fields }), err)
	clients, err := sdk.GetDownloadClientSchema(ctx)
	family("downloadclient", fieldsOf(clients.Model, func(x prowlarr.DownloadClientResource) []prowlarr.Field { return x.Fields }), err)
	proxies, err := sdk.GetIndexerProxySchema(ctx)
	family("proxy", fieldsOf(proxies.Model, func(x prowlarr.IndexerProxyResource) []prowlarr.Field { return x.Fields }), err)
	notifications, err := sdk.GetNotificationSchema(ctx)
	family("notification", fieldsOf(notifications.Model, func(x prowlarr.NotificationResource) []prowlarr.Field { return x.Fields }), err)

	slices.Sort(declared)
	declared = slices.Compact(declared)

	if os.Getenv("PROWLARR_TEST_WRITE_SETTINGS") != "" {
		if err := os.WriteFile(settingsFile, []byte(settingsHeader+strings.Join(declared, "\n")+"\n"), 0o644); err != nil { //nolint:gosec // a list of setting names, read by the unit tests
			t.Fatal(err)
		}
		t.Logf("wrote %d settings to %s", len(declared), settingsFile)
	}

	raw, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}

	for _, line := range declared {
		if !slices.Contains(listed, line) {
			t.Errorf("Prowlarr declares %q, which is not on the list: write the list again (%s) and look at what is new", line, strings.TrimSpace(strings.SplitN(settingsHeader, "\n", 3)[1]))
		}
	}
	for _, line := range listed {
		if !slices.Contains(declared, line) {
			t.Errorf("the list has %q, which Prowlarr no longer declares: write the list again", line)
		}
	}
}

// fieldsOf is the settings of each kind in a family's schema.
func fieldsOf[T any](kinds []T, fields func(T) []prowlarr.Field) [][]prowlarr.Field {
	out := make([][]prowlarr.Field, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, fields(k))
	}

	return out
}
