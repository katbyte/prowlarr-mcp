# Configuration and running

## Settings

All options can be passed as command-line flags, environment variables, or via a configuration file.

| Variable | Flag | Description |
|---|---|---|
| `PROWLARR_SERVER` | `--server`, `-s` | Prowlarr URL, e.g. `http://nas:9696` |
| `PROWLARR_TOKEN` | `--token`, `-t` | API key (Settings → General → Security → API Key) |
| `PROWLARR_READ_ONLY` | `--read-only` | register only tools that never change Prowlarr's state |
| `PROWLARR_ENABLE_DELETE` | `--enable-delete` | register the eight tools that delete: indexers, applications, profiles, proxies, download clients, notifications, saved filters and backups |
| `PROWLARR_TOOLSETS` | `--toolsets` | groups of tools to register, default `core`: `all`, `core`, `curation`, `setup`, `grab`, `admin`, or a resource family like `indexer` (`core` is always included) |
| `PROWLARR_ALLOW_TOOLS` | `--allow-tools` | register these tools as well as the toolsets asked for, or only these when no toolset is (names, `indexer_*` globs, or `essential`) |
| `PROWLARR_DENY_TOOLS` | `--deny-tools` | never register these tools, whatever asked for them (names or globs such as `*_delete`) |
| `PROWLARR_LOG` | | log level (`WARN` default; `INFO` adds a line for every call of a tool that writes or deletes, with what it sent and no credential or setting's value in it; `DEBUG`, `TRACE`, ...) |
| `PROWLARR_LISTEN` | `--listen` | serve MCP over HTTP on this address (e.g. `:8080`) instead of stdio |
| `PROWLARR_AUTH_TOKEN` | `--auth-token` | bearer token required on the HTTP endpoint (required with `--listen`) |
| `PROWLARR_ALLOW_NO_AUTH` | `--allow-no-auth` | serve HTTP with no bearer token at all: anyone who can reach the port can use every tool |
| `PROWLARR_CONFIG` | `--config` | read settings from this file and no other, in place of `./.prowlarr-mcp` or `~/.prowlarr-mcp` |

A flag wins over the environment, and the environment over a settings file.

## Configuration File

You can place a `.prowlarr-mcp` file in your home directory `~/.prowlarr-mcp` (for global settings) or in your current directory `./.prowlarr-mcp` (for per-project settings); the one in the current directory is read when both are there. Keys are the environment's names without the prefix, in the `env` format:

```env
SERVER=http://nas:9696
TOKEN=...
TOOLSETS=curation
READ_ONLY=true
```

## Two Prowlarrs

`--config` (or `PROWLARR_CONFIG`) names the settings file to read, and it is read in place of the usual ones, so nothing of another instance's settings reaches it. Give each Prowlarr a file of its own and register prowlarr-mcp once for each:

```json
{
  "mcpServers": {
    "prowlarr":     {"command": "prowlarr-mcp", "args": ["serve"]},
    "prowlarr-two": {"command": "prowlarr-mcp", "args": ["serve", "--config", "~/.prowlarr-mcp-two"]}
  }
}
```

A file that is named and is not there stops the start, rather than leaving an instance to come up with no settings or another's.

## Run as a service (HTTP transport)

`serve --listen :8080` serves the MCP Streamable HTTP transport at `/mcp` (plus `GET /healthz`) instead of stdio. `PROWLARR_AUTH_TOKEN` is required: clients must send `Authorization: Bearer <token>`, and the server refuses to start without one unless `PROWLARR_ALLOW_NO_AUTH=true` says that anyone who can reach the port may use every tool. Register it from any machine:

```bash
claude mcp add --transport http prowlarr http://nas:8080/mcp \
  --header "Authorization: Bearer $PROWLARR_AUTH_TOKEN"
```

## Docker

Releases publish a multi-arch (amd64, arm64) image to `ghcr.io/katbyte/prowlarr-mcp`, tagged `vX.Y.Z`, `vX.Y` and `latest`. `docker-compose.yml` is the default always-on deployment: it runs that image and reads secrets from a gitignored `.env` (copy `.env.example`). Adjust `PROWLARR_SERVER` and `TZ` in the compose file, then:

```bash
cp .env.example .env      # fill in PROWLARR_TOKEN and PROWLARR_AUTH_TOKEN
docker compose up -d
```

`make docker` builds the same image from source, tagged `prowlarr-mcp`, with version info from git. The image is alpine-based (so `docker exec -it prowlarr-mcp sh` works), runs as a non-root user and has a healthcheck against `/healthz`. The binary is the entrypoint, so `docker run --rm ghcr.io/katbyte/prowlarr-mcp info` works as a connectivity check with the `PROWLARR_*` variables passed via `-e`.
