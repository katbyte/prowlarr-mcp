package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSelect(t *testing.T) {
	t.Parallel()

	all, err := Select("")
	if err != nil || len(all) != len(Services) {
		t.Errorf("Select(\"\") = %d services, %v", len(all), err)
	}
	one, err := Select(" prowlarr ")
	if err != nil || len(one) != 1 || one[0].Package != "prowlarr" {
		t.Errorf("Select(prowlarr) = %+v, %v", one, err)
	}
	if _, err := Select("prowlarr,radarr"); err == nil || !strings.Contains(err.Error(), `unknown service "radarr" (have prowlarr)`) {
		t.Errorf("Select with an unknown service = %v", err)
	}
}

func TestPaths(t *testing.T) {
	t.Parallel()

	svc, ok := Find("prowlarr")
	if !ok {
		t.Fatal("no prowlarr service")
	}
	rooted := svc.In("/repo")
	// the configured paths stay repository-relative; they are recorded in the definitions
	if rooted.Spec != svc.Spec || rooted.Path(rooted.Spec) != filepath.Join(string(filepath.Separator)+"repo", "docs", "prowlarr-openapi.json") {
		t.Errorf("In(/repo) = %+v", rooted)
	}
	if svc.Path(svc.Output) != filepath.Join("lib", "prowlarr") {
		t.Errorf("Path without a root = %q", svc.Path(svc.Output))
	}
}

// Every spelling is keyed by the lower-case segment it replaces and only
// changes the case: a typo that dropped or added a letter would silently
// rename every method on that path.
func TestWords(t *testing.T) {
	t.Parallel()

	for _, svc := range Services {
		for seg, word := range svc.Words {
			if seg != strings.ToLower(seg) {
				t.Errorf("%s: key %q is not lower case", svc.Name, seg)
			}
			if !strings.EqualFold(word, seg) {
				t.Errorf("%s: %q spells %q, which is not the same letters", svc.Name, seg, word)
			}
		}
	}
}
