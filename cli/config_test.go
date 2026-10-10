package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// viper is global, so none of these can run in parallel and each resets it.
const (
	testServer = "http://nas:9696"
	testTool   = "indexer_get"
)

// load drives the real flag wiring against a temporary home and working
// directory.
func load(t *testing.T, home, wd string) *FlagData {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", home)
	t.Chdir(wd)

	if err := configureFlags(&cobra.Command{Use: appName}); err != nil {
		t.Fatalf("configureFlags: %v", err)
	}
	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	return GetFlags()
}

// loadArgs is load with command-line arguments parsed as well.
func loadArgs(t *testing.T, home, wd string, args ...string) *FlagData {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", home)
	t.Chdir(wd)

	root := &cobra.Command{Use: appName}
	if err := configureFlags(root); err != nil {
		t.Fatalf("configureFlags: %v", err)
	}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	return GetFlags()
}

func write(t *testing.T, dir, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, ".prowlarr-mcp"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// on is how a boolean is switched on from the environment.
const on = "true"

// A .prowlarr-mcp in the working directory is documented as per-project
// settings, which it only is if it is found before the one in $HOME. viper
// reads the first file it finds.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestConfigFilePrecedence(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()

	t.Run("project wins over home", func(t *testing.T) {
		write(t, home, "SERVER=http://from-home\n")
		write(t, project, "SERVER=http://from-project\n")
		if got := load(t, home, project).Server; got != "http://from-project" {
			t.Errorf("server = %q, want the project config", got)
		}
	})

	t.Run("home is used when there is no project config", func(t *testing.T) {
		empty := t.TempDir()
		if got := load(t, home, empty).Server; got != "http://from-home" {
			t.Errorf("server = %q, want the home config", got)
		}
	})

	t.Run("no config at all is not an error", func(t *testing.T) {
		if got := load(t, t.TempDir(), t.TempDir()).Server; got != "" {
			t.Errorf("server = %q, want empty", got)
		}
	})
}

// The environment beats a config file, so a container can override what is
// baked into an image.
func TestEnvironmentOverridesConfigFile(t *testing.T) {
	home := t.TempDir()
	write(t, home, "SERVER=http://from-home\nTOKEN=from-home\n")

	t.Setenv("PROWLARR_SERVER", "http://from-env")
	f := load(t, home, t.TempDir())

	if f.Server != "http://from-env" {
		t.Errorf("server = %q, want the environment", f.Server)
	}
	// and a key the environment did not set still comes from the file
	if f.Token != "from-home" {
		t.Errorf("token = %q, want the config file", f.Token)
	}
}

// Every documented PROWLARR_* variable has to actually reach its field; a
// typo in the binding map is invisible until someone sets the variable and
// nothing happens.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestEnvironmentBindings(t *testing.T) {
	dir := t.TempDir()
	for _, env := range []struct {
		key, value string
		check      func(*FlagData) bool
	}{
		{"PROWLARR_SERVER", testServer, func(f *FlagData) bool { return f.Server == testServer }},
		{"PROWLARR_TOKEN", "tok", func(f *FlagData) bool { return f.Token == "tok" }},
		{"PROWLARR_READ_ONLY", on, func(f *FlagData) bool { return f.ReadOnly }},
		{"PROWLARR_ENABLE_DELETE", on, func(f *FlagData) bool { return f.EnableDelete }},
		{"PROWLARR_LISTEN", ":8080", func(f *FlagData) bool { return f.Listen == ":8080" }},
		{"PROWLARR_AUTH_TOKEN", "bearer", func(f *FlagData) bool { return f.AuthToken == "bearer" }},
		{"PROWLARR_ALLOW_NO_AUTH", on, func(f *FlagData) bool { return f.AllowNoAuth }},
		{"PROWLARR_TOOLSETS", "curation", func(f *FlagData) bool { return len(f.Toolsets) == 1 && f.Toolsets[0] == "curation" }},
		{"PROWLARR_ALLOW_TOOLS", testTool, func(f *FlagData) bool { return len(f.AllowTools) == 1 && f.AllowTools[0] == testTool }},
		{"PROWLARR_DENY_TOOLS", "*_delete", func(f *FlagData) bool { return len(f.DenyTools) == 1 && f.DenyTools[0] == "*_delete" }},
	} {
		t.Setenv(env.key, env.value)
		if f := load(t, dir, dir); !env.check(f) {
			t.Errorf("%s=%s did not reach its field: %+v", env.key, env.value, f)
		}
		_ = os.Unsetenv(env.key)
	}
}

// NewClient is where a missing server or token, or a server URL that is not
// one, is caught, before anything tries to talk to Prowlarr.
func TestNewClientValidation(t *testing.T) {
	t.Parallel()

	if _, err := (&FlagData{}).NewClient(); err == nil {
		t.Error("a client with no server should be refused")
	}
	if _, err := (&FlagData{Server: testServer}).NewClient(); err == nil {
		t.Error("a client with no token should be refused")
	}
	if _, err := (&FlagData{Server: "nas:9696", Token: "t"}).NewClient(); err == nil {
		t.Error("a server URL with no scheme should be refused")
	}
	if _, err := (&FlagData{Server: testServer, Token: "t"}).NewClient(); err != nil {
		t.Errorf("a complete configuration was refused: %v", err)
	}
}

// A settings file spells a setting the way the environment does, less the
// prefix: READ_ONLY for --read-only. viper matches a key only when it is
// spelled as the flag is, so every two-word setting in a file was ignored
// without a word.
func TestConfigFileTwoWordKeys(t *testing.T) {
	home := t.TempDir()
	write(t, home, "SERVER=http://from-home\nREAD_ONLY=true\nDENY_TOOLS=indexer_delete\nAUTH_TOKEN=from-home\n")

	f := load(t, home, t.TempDir())
	if !f.ReadOnly || len(f.DenyTools) != 1 || f.DenyTools[0] != "indexer_delete" || f.AuthToken != "from-home" {
		t.Errorf("read only %v, deny %v, auth token %q: want each from the file", f.ReadOnly, f.DenyTools, f.AuthToken)
	}

	// and the environment still beats the file
	t.Setenv("PROWLARR_AUTH_TOKEN", "from-env")
	if got := load(t, home, t.TempDir()).AuthToken; got != "from-env" {
		t.Errorf("auth token = %q, want the environment's", got)
	}
}

// --config names the settings file to read, and it is read in place of the
// usual ones: nothing of a .prowlarr-mcp in the working directory or in $HOME
// reaches an instance told to read another, which is how two instances serve
// two Prowlarrs. The environment can name it too, a flag beats that, and a
// leading ~/ is the home directory.
func TestConfigFlagReadsThatFileAndNoOther(t *testing.T) {
	home, wd := t.TempDir(), t.TempDir()
	write(t, home, "SERVER=http://from-home\nTOKEN=home-token\nENABLE_DELETE=true\n")
	write(t, wd, "TOOLSETS=grab\n")
	second := filepath.Join(home, ".prowlarr-mcp-second")
	if err := os.WriteFile(second, []byte("SERVER=http://second\nTOKEN=second-token\nREAD_ONLY=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := filepath.Join(wd, "third.env")
	if err := os.WriteFile(third, []byte("SERVER=http://third\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// with no file named, the usual ones are read as before: the first
	// found, which is the working directory's
	if f := load(t, home, wd); len(f.Toolsets) != 1 || f.Toolsets[0] != "grab" || f.Server != "" {
		t.Errorf("with no file named: %+v", f)
	}

	for name, f := range map[string]*FlagData{
		"the flag":            loadArgs(t, home, wd, "--config", second),
		"the flag, from home": loadArgs(t, home, wd, "--config", "~/.prowlarr-mcp-second"),
	} {
		if f.Server != "http://second" || f.Token != "second-token" || !f.ReadOnly {
			t.Errorf("%s: the named file was not read: %+v", name, f)
		}
		if f.EnableDelete || len(f.Toolsets) != 0 {
			t.Errorf("%s: settings of a file that was not named came through: delete %v, toolsets %v", name, f.EnableDelete, f.Toolsets)
		}
	}

	t.Setenv(configEnv, second)
	if f := load(t, home, wd); f.Server != "http://second" || f.EnableDelete {
		t.Errorf("named by the environment: %+v", f)
	}
	if f := loadArgs(t, home, wd, "--config", third); f.Server != "http://third" || f.Token != "" {
		t.Errorf("a flag over the environment's file: %+v", f)
	}
	// and a flag or the environment still beats what the file says
	t.Setenv("PROWLARR_SERVER", "http://from-env")
	if f := loadArgs(t, home, wd, "--config", second, "--token", "from-flag"); f.Server != "http://from-env" || f.Token != "from-flag" || !f.ReadOnly {
		t.Errorf("a flag and the environment over the named file: %+v", f)
	}
}

// A settings file that was named and is not there is an error, not a run
// with no settings: an instance meant for one Prowlarr would otherwise come
// up with nothing, or be given another's by the environment.
func TestConfigFlagNamingNoFileIsAnError(t *testing.T) {
	home := t.TempDir()
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", home)
	t.Chdir(home)

	root := &cobra.Command{Use: appName}
	if err := configureFlags(root); err != nil {
		t.Fatal(err)
	}
	if err := root.ParseFlags([]string{"--config", filepath.Join(home, "no-such-file")}); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(); err == nil || !strings.Contains(err.Error(), "reading the settings file") || !strings.Contains(err.Error(), "no-such-file") {
		t.Errorf("a named file that is not there = %v", err)
	}
}
