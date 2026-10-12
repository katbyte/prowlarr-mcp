package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/pandorest"
	"github.com/katbyte/pandorest/config"
	pandorestgen "github.com/katbyte/pandorest/generator"
	"github.com/katbyte/pandorest/importer"
	"github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// repoRoot is where the config's paths resolve from.
const repoRoot = "../.."

func runCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, log bytes.Buffer
	err = pandorest.Run(generator, args, &out, &log)

	return out.String(), log.String(), err
}

// resolved is the services with the document and definitions each is at, in
// the repository.
func resolved(t *testing.T) []config.Service {
	t.Helper()

	out := make([]config.Service, 0, len(generator.Services))
	for i := range generator.Services {
		svc, err := generator.Services[i].In(repoRoot).Resolve()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, svc)
	}

	return out
}

// The checked-in definitions match the vendored specs and the generated
// packages have every method: make apicheck, as a test.
func TestCheckRepository(t *testing.T) {
	t.Parallel()

	stdout, _, err := runCmd(t, "check", "-root", repoRoot, "-quiet")
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range resolved(t) {
		if !strings.Contains(stdout, svc.Name+": ") || !strings.Contains(stdout, "every one has a method in "+filepath.Join(repoRoot, svc.Output)) {
			t.Errorf("check output lacks %s:\n%s", svc.Name, stdout)
		}
	}
	stdout, _, err = runCmd(t, "diff", "-root", repoRoot, "-quiet", "-exit-code")
	if err != nil || strings.Count(stdout, ": no changes") != len(generator.Services) {
		t.Errorf("diff against the specs = %v:\n%s", err, stdout)
	}
}

// import then generate in a scratch copy of the repository reproduces the
// checked-in definitions and packages byte for byte: make gencheck, as a test.
func TestImportGenerateReproduces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	services := resolved(t)
	for _, svc := range services {
		src, err := os.ReadFile(svc.Path(svc.Spec))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, svc.Spec)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, svc.Spec), src, 0o600); err != nil { //nolint:gosec // the config's own spec path under the test's temp dir
			t.Fatal(err)
		}
	}

	if _, stderr, err := runCmd(t, "import", "-root", root); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(stderr, "applied workaround prowlarr-ui-routes") {
		t.Errorf("import did not log its workarounds:\n%s", stderr)
	}
	if _, _, err := runCmd(t, "generate", "-root", root); err != nil {
		t.Fatal(err)
	}

	for _, svc := range services {
		for _, dir := range []string{svc.Definitions, svc.Output} {
			// (hand-written tests beside the generated code are not regenerated)
			want := listFiles(t, filepath.Join(repoRoot, dir), dir == svc.Output)
			got := listFiles(t, filepath.Join(root, dir), false)
			if len(got) != len(want) {
				t.Errorf("%s: %d files regenerated, %d checked in", dir, len(got), len(want))
			}
			for name, content := range want {
				if got[name] != content {
					t.Errorf("%s/%s differs from a fresh generation; run make generate", dir, name)
				}
			}
		}
	}

	// the diff between two copies of the same definitions is empty
	checkedIn, fresh := filepath.Join(repoRoot, services[0].Definitions), filepath.Join(root, services[0].Definitions)
	stdout, _, err := runCmd(t, "diff", "-old", checkedIn, "-new", fresh, "-exit-code")
	if err != nil || !strings.Contains(stdout, "no changes") {
		t.Errorf("diff -old -new = %v:\n%s", err, stdout)
	}
	if err := os.Remove(filepath.Join(fresh, "Tag.json")); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = runCmd(t, "diff", "-old", checkedIn, "-new", fresh, "-exit-code")
	if !errors.Is(err, pandorest.ErrChanges) || !strings.Contains(stdout, "- operation PostTag (POST /api/v1/tag) [breaking]") {
		t.Errorf("diff with a group gone = %v:\n%s", err, stdout)
	}
}

// listFiles reads a directory's files; generatedOnly skips the hand-written
// ones (no generated header) that live beside generated code.
func listFiles(t *testing.T, dir string, generatedOnly bool) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // a directory under test
		if err != nil {
			t.Fatal(err)
		}
		if generatedOnly && !pandorestgen.Generated(b) {
			continue
		}
		out[e.Name()] = string(b)
	}

	return out
}

// Every workaround fixes a bug that is in the vendored document, and fails
// once the bug is gone, so a refreshed spec that no longer has it stops the
// import rather than leaving a dead fix behind.
func TestWorkarounds(t *testing.T) {
	t.Parallel()

	services := resolved(t)
	load := func(service string) (*openapi.Spec, error) {
		svc, ok := config.Find(services, service)
		if !ok {
			return nil, errors.New("no service " + service)
		}

		return openapi.Load(svc.Path(svc.Spec))
	}
	if err := workarounds.Verify(generator.Workarounds, load); err != nil {
		t.Error(err)
	}
	if len(generator.Workarounds) != 12 {
		t.Errorf("%d workarounds, the docs count 12", len(generator.Workarounds))
	}
}

// The kinds of id the client tells apart each reach a place in the document, no two claim one, and no name is both a kind's and left plain.
func TestIDs(t *testing.T) {
	t.Parallel()

	svc, ok := config.Find(resolved(t), "prowlarr")
	if !ok {
		t.Fatal("no service prowlarr")
	}
	spec, err := openapi.Load(svc.Path(svc.Spec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workarounds.Apply(generator.Workarounds, svc.Name, spec, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if err := importer.VerifyIDs(svc, spec); err != nil {
		t.Error(err)
	}
}
