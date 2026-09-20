// Package main implements prowlarr-mcp, an MCP server and CLI for auditing and running a Prowlarr indexer manager.
package main

import (
	"os"

	c "github.com/gookit/color"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/prowlarr-mcp/cli"
)

func main() {
	// the log level comes from PROWLARR_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("PROWLARR_LOG")

	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(c.Sprintf("<red>prowlarr-mcp: building cmd</> %v", err))

		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(c.Sprintf("<red>prowlarr-mcp:</> %v", err))

		os.Exit(1)
	}

	os.Exit(0)
}
