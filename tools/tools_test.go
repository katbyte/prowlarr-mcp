package tools

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestClient(t *testing.T) *prowlarr.Client {
	t.Helper()

	c, err := prowlarr.New("http://127.0.0.1:1", "test")
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func register(t *testing.T, opts Options) []string {
	t.Helper()

	names, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), newTestClient(t), opts)
	if err != nil {
		t.Fatal(err)
	}

	return names
}

// kinds maps every tool to its kind, as registration queued it.
func kinds() map[string]toolKind {
	r := &registry{}
	queueTools(r)
	out := map[string]toolKind{}
	for _, p := range r.pending {
		out[p.name] = p.kind
	}

	return out
}

func TestRegisterAllKinds(t *testing.T) {
	t.Parallel()

	all := register(t, Options{EnableDelete: true})
	dflt := register(t, Options{})
	ro := register(t, Options{ReadOnly: true})

	if len(all) <= len(dflt) || len(dflt) <= len(ro) || len(ro) == 0 {
		t.Fatalf("counts all=%d default=%d read-only=%d", len(all), len(dflt), len(ro))
	}
	for _, name := range []string{"indexer_delete", "app_delete", "profile_delete", "proxy_delete", "downloadclient_delete"} {
		if slices.Contains(dflt, name) {
			t.Errorf("%s registered without --enable-delete", name)
		}
		if !slices.Contains(all, name) {
			t.Errorf("%s missing with --enable-delete", name)
		}
	}
	// every tool that changes something is named for it, so a name read
	// under --read-only is a read
	for _, name := range ro {
		for _, verb := range []string{"_add", "_edit", "_delete", "_create", "_run", "_grab", "_sync"} {
			if strings.HasSuffix(name, verb) {
				t.Errorf("%s registered under --read-only", name)
			}
		}
	}
	for _, name := range EssentialTools {
		if !slices.Contains(dflt, name) {
			t.Errorf("essential tool %s does not exist", name)
		}
	}
	if !slices.IsSorted(dflt) {
		t.Error("registered names not sorted")
	}
}

// The delete tools are the ones that remove what Prowlarr holds; tag_delete
// is not among them, because Prowlarr refuses to delete a tag anything
// carries, so it can only ever remove a label.
func TestDeleteToolsAreMarked(t *testing.T) {
	t.Parallel()

	for name, kind := range kinds() {
		isDelete := strings.HasSuffix(name, "_delete") && name != "tag_delete"
		if isDelete != (kind == deleteTool) {
			t.Errorf("%s is kind %d; the tools that delete, and only those, are delete tools", name, kind)
		}
	}
}

func TestRegisterAllFilters(t *testing.T) {
	t.Parallel()

	got := register(t, Options{Allow: []string{"essential"}})
	want := slices.Clone(EssentialTools)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("essential = %v", got)
	}

	got = register(t, Options{Allow: []string{"indexer_*,app_list"}, Deny: []string{"*_edit"}})
	for _, name := range got {
		if !strings.HasPrefix(name, "indexer_") && name != "app_list" {
			t.Errorf("unexpected %s", name)
		}
		if strings.HasSuffix(name, "_edit") {
			t.Errorf("denied tool %s registered", name)
		}
	}
	if !slices.Contains(got, "indexer_list") || !slices.Contains(got, "app_list") {
		t.Errorf("allow list not honoured: %v", got)
	}

	if _, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), newTestClient(t), Options{Allow: []string{"bogus_*"}}); err == nil {
		t.Error("unknown allow pattern accepted")
	}
	if _, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), newTestClient(t), Options{Deny: []string{"nope"}}); err == nil {
		t.Error("unknown deny pattern accepted")
	}
}

// Every tool belongs to exactly one toolset, every toolset names only real
// tools, and core comes along with whatever else is asked for.
func TestToolsetsPartition(t *testing.T) {
	t.Parallel()

	all := register(t, Options{EnableDelete: true})
	seen := map[string]string{}
	for set, members := range Toolsets {
		for _, m := range members {
			if !slices.Contains(all, m) {
				t.Errorf("toolset %s names %s, which is not a tool", set, m)
			}
			if prev, dup := seen[m]; dup {
				t.Errorf("%s is in both %s and %s", m, prev, set)
			}
			seen[m] = set
		}
	}
	for _, name := range all {
		if seen[name] == "" {
			t.Errorf("%s belongs to no toolset", name)
		}
	}

	got := register(t, Options{Toolsets: []string{"grab"}})
	for _, core := range Toolsets["core"] {
		if !slices.Contains(got, core) {
			t.Errorf("core tool %s missing when only grab was asked for", core)
		}
	}
	for _, name := range got {
		if seen[name] != "core" && seen[name] != "grab" {
			t.Errorf("%s (%s) registered for --toolsets grab", name, seen[name])
		}
	}

	// a resource family is every tool with that prefix, plus core
	got = register(t, Options{Toolsets: []string{"proxy"}})
	for _, name := range got {
		if !strings.HasPrefix(name, "proxy_") && seen[name] != "core" {
			t.Errorf("%s registered for the proxy family", name)
		}
	}
	if !slices.Contains(got, "proxy_list") {
		t.Errorf("the proxy family lacks proxy_list: %v", got)
	}

	// all is everything the kind gates allow
	if got = register(t, Options{Toolsets: []string{"all"}}); len(got) != len(register(t, Options{})) {
		t.Errorf("all registered %d tools, want %d", len(got), len(register(t, Options{})))
	}

	if _, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), newTestClient(t), Options{Toolsets: []string{"nope"}}); err == nil {
		t.Error("unknown toolset accepted")
	} else if !strings.Contains(err.Error(), "curation") || !strings.Contains(err.Error(), "indexer") {
		t.Errorf("the error should name the sets and families: %v", err)
	}
}

// A tool's description or schema that points at another tool points at one
// the same toolset (or core) loads, so a session never reads "fix it with
// indexer_edit" without indexer_edit to call. The audits are the exception
// they are allowed to be: naming the tool that fixes a finding is their job,
// and the fix may live in setup or admin.
func TestToolsetsNameOnlyWhatTheyLoad(t *testing.T) {
	t.Parallel()

	all := register(t, Options{EnableDelete: true})
	set := map[string]string{}
	for name, members := range Toolsets {
		for _, m := range members {
			set[m] = name
		}
	}
	res, err := session(t, newFakeServer(t), Options{EnableDelete: true}).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	word := regexp.MustCompile(`[a-z]+(?:_[a-z]+)+`)
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, "audit_") {
			continue
		}
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range word.FindAllString(string(raw), -1) {
			if ref == tool.Name || !slices.Contains(all, ref) {
				continue
			}
			if set[ref] != "core" && set[ref] != set[tool.Name] {
				t.Errorf("%s (%s) names %s, which only %s loads", tool.Name, set[tool.Name], ref, set[ref])
			}
		}
	}
}

// Describe reports the same selection RegisterAll makes, with its kinds and
// sets, and needs no server.
func TestDescribe(t *testing.T) {
	t.Parallel()

	list, err := Describe(Options{Toolsets: []string{"curation"}, EnableDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list))
	kinds := map[string]string{}
	for _, ti := range list {
		names = append(names, ti.Name)
		kinds[ti.Name] = ti.Kind
		if ti.Toolset != "core" && ti.Toolset != "curation" {
			t.Errorf("%s reported in %s", ti.Name, ti.Toolset)
		}
		if ti.Description == "" {
			t.Errorf("%s has no description", ti.Name)
		}
	}
	want := register(t, Options{Toolsets: []string{"curation"}, EnableDelete: true})
	if !slices.Equal(names, want) {
		t.Errorf("Describe = %v\nRegisterAll = %v", names, want)
	}
	if kinds["audit_all"] != "read" || kinds["indexer_edit"] != "write" {
		t.Errorf("kinds = %v", kinds)
	}
	if _, err := Describe(Options{Allow: []string{"nope"}}); err == nil {
		t.Error("Describe accepted a pattern that matches nothing")
	}

	if fam := FamilyNames(); !slices.Contains(fam, "audit") || !slices.Contains(fam, "indexer") {
		t.Errorf("families = %v", fam)
	}
	if sets := ToolsetNames(); !slices.Contains(sets, "core") || !slices.IsSorted(sets) {
		t.Errorf("toolset names = %v", sets)
	}
}

// Every audit is in audit_all, and every audit answers the same worklist
// shape.
func TestAuditsAreAllInAuditAll(t *testing.T) {
	t.Parallel()

	r := &registry{client: newTestClient(t)}
	tbl := &auditTable{}
	registerIndexerAudits(r, tbl)
	registerRoutingAudits(r, tbl)
	registerSettingAudits(r, tbl)
	inTable := make([]string, 0, len(tbl.list))
	for _, a := range tbl.list {
		inTable = append(inTable, a.name)
		if a.summary == "" {
			t.Errorf("%s has no summary for audit_all", a.name)
		}
	}
	for _, name := range register(t, Options{}) {
		if strings.HasPrefix(name, "audit_") && name != "audit_all" && !slices.Contains(inTable, name) {
			t.Errorf("%s is not run by audit_all", name)
		}
	}
	if len(inTable) != 16 {
		t.Errorf("%d audits, the README lists 16 beside audit_all: %v", len(inTable), inTable)
	}
}

func TestMatchPattern(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"indexer_get", "indexer_get", true},
		{"indexer_get", "indexer_gets", false},
		{"indexer_*", "indexer_get", true},
		{"indexer_*", "audit_indexers", false},
		{"*_delete", "indexer_delete", true},
		// the documented way to turn every destructive tool off has to reach these
		{"*_delete", "downloadclient_delete", true},
		{"*", "anything", true},
	} {
		if got := matchPattern(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v", tc.pattern, tc.name, got)
		}
	}
}

// A nil slice in a result must serialise as [], so a client can tell "none"
// from "not fetched".
func TestEmptyNilSlices(t *testing.T) {
	t.Parallel()

	type inner struct{ Tags []string }
	type out struct {
		Items  []inner
		Ptr    *inner
		Names  []string
		Nested [][]string
		Keep   []string
	}
	v := out{Items: []inner{{}}, Ptr: &inner{}, Keep: []string{"x"}}
	emptyNilSlices(reflect.ValueOf(&v).Elem())

	if v.Names == nil || v.Nested == nil || v.Items[0].Tags == nil || v.Ptr.Tags == nil {
		t.Errorf("nil slices survived: %+v", v)
	}
	if len(v.Keep) != 1 {
		t.Error("a populated slice was touched")
	}
}

// Every tool has a description and an input and output schema, and every
// schema property says what it is for: the model reads nothing else.
func TestSchemasAreDescribed(t *testing.T) {
	t.Parallel()

	res, err := session(t, newFakeServer(t), Options{EnableDelete: true}).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if len(tool.Description) < 40 {
			t.Errorf("%s: description %q is too thin to choose the tool by", tool.Name, tool.Description)
		}
		if tool.Annotations == nil {
			t.Errorf("%s: no annotations", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema", tool.Name)
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Fatal(err)
		}
		for name, p := range in.Properties {
			// a handful are self-explanatory
			if p.Description == "" && !slices.Contains([]string{"name", "enable", "limit", "label", "rss", "automatic_search", "interactive_search", "minimum_seeders", "add_tags", "remove_tags"}, name) {
				t.Errorf("%s: input %q has no description", tool.Name, name)
			}
		}
	}
}
