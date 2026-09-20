// Package cli implements the prowlarr-mcp command line interface: the cobra commands, flag and
// config handling, and the MCP server exposing Prowlarr's indexers and applications to AI clients.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/prowlarr-mcp/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// firstSentence trims a tool description to its opening sentence, so the list
// stays one line per tool.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}

	return s
}

func ValidateParams(params []string) func(cmd *cobra.Command, args []string) error {
	return func(_ *cobra.Command, _ []string) error {
		for _, p := range params {
			if viper.GetString(p) != "" {
				continue
			}
			return errors.New(p + " parameter can't be empty")
		}

		return nil
	}
}

// connectionParams are the flags every command that talks to Prowlarr needs.
var connectionParams = []string{"server", "token"}

// toolsetOrder is how `prowlarr-mcp tools` lists the sets: the base first,
// then the job most sessions are for.
var toolsetOrder = []string{"core", "curation", "setup", "grab", "admin"}

func Make() (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "prowlarr-mcp [command]",
		Short: "prowlarr-mcp is an MCP server and CLI for auditing and running a Prowlarr indexer manager",
		Long: `An MCP server (and CLI) for Prowlarr: audit the indexers and the applications they
feed for what goes wrong - failing, slow and unused indexers, indexers no application
receives, missing seed goals, retired addresses - and fix it, search the indexers, and
manage applications, proxies and tags from an AI client such as Claude Code.
Complete documentation is available at https://github.com/katbyte/prowlarr-mcp`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run \"prowlarr-mcp help\" for more information about available prowlarr-mcp commands.")
			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "Print the version number of prowlarr-mcp",
		Long:          `Print the version number of prowlarr-mcp`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "prowlarr-mcp "+version.Version)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "tools",
		Short: "List the tools and toolsets, and what the current flags would register",
		Long: `Lists every tool prowlarr-mcp would register with the current flags, grouped by toolset,
with its kind (read, write or delete) and what it does.

Needs no server: it reports what would be registered, not what a server accepts.

  prowlarr-mcp tools                        # the default set (core)
  prowlarr-mcp tools --toolsets all         # every tool
  prowlarr-mcp tools --toolsets curation    # audits and the tools that fix what they find
  prowlarr-mcp tools --read-only            # only the tools that never change state
  prowlarr-mcp tools -q                     # names only`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			quiet, _ := cmd.Flags().GetBool("quiet")

			f := GetFlags()
			list, err := tools.Describe(f.ToolOptions())
			if err != nil {
				return err
			}

			if quiet {
				for _, t := range list {
					_, _ = fmt.Fprintln(out, t.Name)
				}
				return nil
			}

			byset := map[string][]tools.ToolInfo{}
			for _, t := range list {
				byset[t.Toolset] = append(byset[t.Toolset], t)
			}
			counts := map[string]int{}
			for _, t := range list {
				counts[t.Kind]++
			}

			for _, set := range toolsetOrder {
				in := byset[set]
				if len(in) == 0 {
					continue
				}
				_, _ = fmt.Fprintf(out, "\n%s (%d)\n", set, len(in))
				for _, t := range in {
					_, _ = fmt.Fprintf(out, "  %-32s %-6s %s\n", t.Name, t.Kind, firstSentence(t.Description))
				}
			}
			_, _ = fmt.Fprintf(out, "\n%d tools: %d read, %d write, %d delete\n",
				len(list), counts["read"], counts["write"], counts["delete"])
			if !f.EnableDelete {
				_, _ = fmt.Fprintln(out, "delete tools are hidden; --enable-delete registers them")
			}
			_, _ = fmt.Fprintf(out, "\ntoolsets: all, %s\n", strings.Join(tools.ToolsetNames(), ", "))
			_, _ = fmt.Fprintf(out, "families: %s\n", strings.Join(tools.FamilyNames(), ", "))
			_, _ = fmt.Fprintf(out, "select with --toolsets / PROWLARR_TOOLSETS; core is always included. "+
				"Default is %s - use --toolsets all for every tool.\n", strings.Join(DefaultToolsets, ","))

			return nil
		},
	})
	if c, _, err := root.Find([]string{"tools"}); err == nil {
		c.Flags().BoolP("quiet", "q", false, "print tool names only")
	}

	root.AddCommand(&cobra.Command{
		Use:           "info",
		Short:         "Check connectivity and print Prowlarr's version and indexers",
		Long:          `Connects to the configured Prowlarr and prints its version and platform, and its indexers and applications.`,
		Args:          cobra.NoArgs,
		PreRunE:       ValidateParams(connectionParams),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()

			client, err := GetFlags().NewClient()
			if err != nil {
				return err
			}

			st, err := client.GetSystemStatus(cmd.Context())
			if err != nil {
				return err
			}
			idx, err := client.GetIndexer(cmd.Context())
			if err != nil {
				return err
			}
			apps, err := client.GetApplications(cmd.Context())
			if err != nil {
				return err
			}

			s := st.Model
			_, _ = fmt.Fprintf(out, "%s %s on %s %s at %s\n", s.AppName, s.Version, s.OsName, s.OsVersion, client.Client.BaseURL)
			for _, i := range idx.Model {
				state := "enabled"
				if i.Enable == nil || !*i.Enable {
					state = "disabled"
				}
				_, _ = fmt.Fprintf(out, "  indexer %q (%s, %s) %s\n", i.Name, i.Protocol, i.Privacy, state)
			}
			for _, a := range apps.Model {
				_, _ = fmt.Fprintf(out, "  application %q (%s) sync %s\n", a.Name, a.Implementation, a.SyncLevel)
			}
			return nil
		},
	})

	root.AddCommand(serveCmd())

	if err := configureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}

	return root, nil
}
