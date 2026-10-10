package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/katbyte/prowlarr-mcp/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type FlagData struct {
	Server       string   `mapstructure:"server"`
	Token        string   `mapstructure:"token"`
	ReadOnly     bool     `mapstructure:"read-only"`
	EnableDelete bool     `mapstructure:"enable-delete"`
	Toolsets     []string `mapstructure:"toolsets"`
	AllowTools   []string `mapstructure:"allow-tools"`
	DenyTools    []string `mapstructure:"deny-tools"`
	Listen       string   `mapstructure:"listen"`
	AuthToken    string   `mapstructure:"auth-token"`
	AllowNoAuth  bool     `mapstructure:"allow-no-auth"`
}

// envNames binds each flag to its environment variable.
var envNames = map[string]string{ //nolint:gosec // G101: these are env var names, not credentials
	"server":        "PROWLARR_SERVER",
	"token":         "PROWLARR_TOKEN",
	"read-only":     "PROWLARR_READ_ONLY",
	"enable-delete": "PROWLARR_ENABLE_DELETE",
	"toolsets":      "PROWLARR_TOOLSETS",
	"allow-tools":   "PROWLARR_ALLOW_TOOLS",
	"deny-tools":    "PROWLARR_DENY_TOOLS",
	"listen":        "PROWLARR_LISTEN",
	"auth-token":    "PROWLARR_AUTH_TOKEN",
	"allow-no-auth": "PROWLARR_ALLOW_NO_AUTH",
}

// persistent is the flag set configureFlags bound, for the settings that
// have to be read source by source rather than through viper.
var persistent *pflag.FlagSet

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.StringP("server", "s", "", "Prowlarr's url, e.g. http://nas:9696 (with its URL base, if it has one)")
	pflags.StringP("token", "t", "", "Prowlarr's API key (consider exporting to PROWLARR_TOKEN instead)")
	pflags.Bool("read-only", false, "register only tools that never change Prowlarr's state")
	pflags.Bool("enable-delete", false, "register the tools that delete indexers, applications, profiles, proxies, download clients, notifications, saved filters and backups")
	pflags.StringSlice("toolsets", nil, "groups of tools to register: all, core (default), curation, setup, grab, admin, or a resource family like indexer (core is always included)")
	pflags.StringSlice("allow-tools", nil, "register these tools as well as the toolsets asked for, or only these when no toolset is: names, prefix globs like indexer_*, or the essential preset")
	pflags.StringSlice("deny-tools", nil, "never register these tools, whatever asked for them: names or prefix globs like *_delete")
	pflags.String("listen", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	pflags.String("auth-token", "", "bearer token required on the HTTP endpoint (consider exporting to PROWLARR_AUTH_TOKEN instead)")
	pflags.Bool("allow-no-auth", false, "serve HTTP with no bearer token: anyone who can reach the port can use every tool")
	pflags.String("config", "", "read settings from this file and no other, in place of ./.prowlarr-mcp or ~/.prowlarr-mcp: how a second instance serves a second Prowlarr (or export PROWLARR_CONFIG)")

	persistent = pflags

	for name, env := range envNames {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}

		if env != "" {
			if err := viper.BindEnv(name, env); err != nil {
				return fmt.Errorf("error binding '%s' to env '%s' : %w", name, env, err)
			}
		}
	}

	return nil
}

// configEnv names the settings file from the environment, as --config does
// on the command line.
const configEnv = "PROWLARR_CONFIG"

// configPath is the settings file a run was told to read, "" for none: the
// flag's, then the environment's. A leading ~/ is the home directory, for a
// client that hands its arguments over without a shell to expand it.
func configPath() string {
	path := os.Getenv(configEnv)
	if persistent != nil {
		if fl := persistent.Lookup("config"); fl != nil && fl.Changed {
			path = fl.Value.String()
		}
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}

	return path
}

// loadConfig reads the settings file, once the flags are parsed: the one
// --config or PROWLARR_CONFIG names and no other, or else the first
// .prowlarr-mcp found in the working directory and then in $HOME.
//
// A file that was named and cannot be read is an error: an instance meant
// for one Prowlarr must not come up with another's settings, or with none.
// One merely absent from the usual places is not.
func loadConfig() error {
	viper.SetConfigType("env")
	if path := configPath(); path != "" {
		viper.SetConfigFile(path)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("reading the settings file %s (--config / %s): %w", path, configEnv, err)
		}
	} else {
		viper.SetConfigName(".prowlarr-mcp")
		// viper reads the first file it finds, so the working directory comes
		// first: a per-project .prowlarr-mcp overrides the one in $HOME
		viper.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(home)
		}
		if err := viper.ReadInConfig(); err != nil {
			if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
				clog.Log.Errorf("Error reading config file: %v", err)
			}
		}
	}

	// a config file spells a setting the way the environment does, less the
	// prefix - READ_ONLY for --read-only - and viper only matches a key
	// spelled as the flag is, so every two-word setting was ignored. Carried
	// across as defaults, they still lose to a flag or the environment.
	for name := range envNames {
		if alt := strings.ReplaceAll(name, "-", "_"); alt != name && viper.InConfig(alt) {
			viper.SetDefault(name, viper.Get(alt))
		}
	}

	return nil
}

// GetFlags returns the fully populated FlagData.
// We must unmarshal from Viper instead of using globally bound pflags variables
// because pflags only parses command-line arguments. Viper merges environment
// variables (and config files) on top of the CLI flags.
func GetFlags() *FlagData {
	var f FlagData
	if err := viper.Unmarshal(&f); err != nil {
		clog.Log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	return &f
}

// NewClient is a client for the configured server, which says what it sends
// and is sent to the process's log at the level PROWLARR_LOG sets, with the
// API key hidden.
func (f *FlagData) NewClient() (*prowlarr.Client, error) {
	return prowlarr.New(f.Server, f.Token, client.WithLog(clog.Log))
}

// DefaultToolsets is what the binary registers when neither --toolsets nor
// --allow-tools is given: enough to find things and read them, and nothing
// that writes. The whole surface is thousands of tokens of tool definitions
// before a question is asked, which is a poor thing to spend a client's
// context on by default. Ask for more with --toolsets, or --toolsets all for
// everything.
var DefaultToolsets = []string{"core"}

// ToolOptions maps the flags onto the tool registration options. The default
// toolsets are for a binary told nothing about which tools it is to serve:
// an allow list with no toolsets beside it is only the tools it names.
func (f *FlagData) ToolOptions() tools.Options {
	sets := f.Toolsets
	if len(sets) == 0 && len(f.AllowTools) == 0 {
		sets = DefaultToolsets
	}

	return tools.Options{
		ReadOnly:     f.ReadOnly,
		EnableDelete: f.EnableDelete,
		Toolsets:     sets,
		Allow:        f.AllowTools,
		Deny:         f.DenyTools,
	}
}
