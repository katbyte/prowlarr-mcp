#!/usr/bin/env bash
#
# Bring up a throwaway Prowlarr in Docker, cut off from the internet, with a
# known catalogue of tracker definitions, and print the environment the live
# test suites need.
#
#   eval "$(scripts/testenv.sh up)"   # start, export PROWLARR_*
#   scripts/testenv.sh down           # stop and remove everything
#   scripts/testenv.sh logs           # what Prowlarr wrote, for a failing run
#
# This script only does what prowlarr-mcp cannot: write the config Prowlarr
# reads its API key from, seed the definitions, and run the container. The
# indexers, applications, profiles, proxies, download clients and tags are
# the test suites' job, through indexer_add, app_add and the rest, so those
# tools are exercised rather than bypassed.
#
# The indexers and applications Prowlarr talks to are fakes the suites run on
# the host (internal/fakes): Newznab and Torznab sites that can be made to
# fail, answer slowly or refuse their key, and a Sonarr and a Radarr that
# record what Prowlarr syncs into them. The container reaches them through
# host.docker.internal. Everything else it would reach for - its definitions
# service, its update check - goes to a proxy that is not there, so a run
# needs no network and Prowlarr falls back to the definitions seeded here
# (scripts/testdata/definitions, three real ones from Prowlarr's
# definitions repository), which is exactly what it does offline.

set -euo pipefail

# pinned, because the spec in docs/ is this release's own: to move to a new
# one, vendor its spec (docs/README.md) and bump this
IMAGE="${PROWLARR_TEST_IMAGE:-lscr.io/linuxserver/prowlarr:version-2.6.5.5623}"
PORT="${PROWLARR_TEST_PORT:-19696}"
NAME="${PROWLARR_TEST_CONTAINER:-prowlarr-mcp-test}"
# the ports the suites' fakes listen on, reached from the container through
# host.docker.internal: the Newznab and Torznab sites, and the applications
INDEXER_PORT="${PROWLARR_TEST_INDEXER_PORT:-19691}"
APP_PORT="${PROWLARR_TEST_APP_PORT:-19692}"
# not TMPDIR: on macOS that is /var/folders/..., which Docker Desktop and
# Colima do not share by default, and the bind mounts silently come up empty
DATA="${PROWLARR_TEST_DATA:-${HOME}/.cache/prowlarr-mcp/testenv/default}"
# Prowlarr reads its API key from config.xml, which is written before the
# first start, so the key is known without scraping it out of a running
# server. It is a throwaway: the container is on localhost and gone after the
# run.
API_KEY="prowlarrmcp0test0key000000000000"
URL="http://127.0.0.1:${PORT}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# host_address is the address the container reaches the suites' fakes on.
# Docker Desktop and Colima provide host.docker.internal; on Linux docker maps
# it with --add-host, and the container is handed the bridge gateway's
# address instead, so a runtime that prefers an IPv6 answer cannot pick a
# route the host does not listen on.
host_address() {
  if [ "$(uname -s)" = "Linux" ]; then
    docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null && return 0
  fi
  echo "host.docker.internal"
}

log() { echo "==> $*" >&2; }

# wipe_data removes what a previous run left; the container wrote some of it
# as its own user, so a container does the removing when the host cannot.
wipe_data() {
  [ -d "${DATA}" ] || return 0
  rm -rf "${DATA}" 2>/dev/null ||
    docker run --rm -v "$(dirname "${DATA}"):/parent" alpine rm -rf "/parent/$(basename "${DATA}")"
}

# config writes what Prowlarr reads on its first start: config.xml with the
# API key the suites use, no browser, no analytics, and debug logging so a
# failing run leaves something to read; the seeded definitions; and the
# folders the blackhole download clients write grabs into. Everything is
# made by the host user, whom the container runs as (PUID/PGID), so Prowlarr
# can write where it needs to.
config() {
  mkdir -p "${DATA}/config/Definitions" "${DATA}/downloads/torrent" "${DATA}/downloads/usenet"
  cp "${HERE}/testdata/definitions/"*.yml "${DATA}/config/Definitions/"
  cat >"${DATA}/config/config.xml" <<EOF
<Config>
  <BindAddress>*</BindAddress>
  <Port>9696</Port>
  <SslPort>6969</SslPort>
  <EnableSsl>False</EnableSsl>
  <LaunchBrowser>False</LaunchBrowser>
  <ApiKey>${API_KEY}</ApiKey>
  <AuthenticationMethod>External</AuthenticationMethod>
  <AuthenticationRequired>DisabledForLocalAddresses</AuthenticationRequired>
  <Branch>master</Branch>
  <LogLevel>debug</LogLevel>
  <UrlBase></UrlBase>
  <InstanceName>prowlarr-mcp-test</InstanceName>
  <AnalyticsEnabled>False</AnalyticsEnabled>
</Config>
EOF
}

# wait_for WHAT TRIES COMMAND
wait_for() {
  local what=$1 tries=$2 cmd=$3
  log "waiting for ${what}"
  for _ in $(seq "$tries"); do
    if eval "$cmd" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  echo "timed out waiting for ${what}" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
  return 1
}

# logs prints what Prowlarr wrote about itself: docker logs shows its console,
# and /config/logs/prowlarr.txt the rest.
logs() {
  echo "==> docker logs ${NAME}" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
  local f="${DATA}/config/logs/prowlarr.txt"
  [ -f "$f" ] || return 0
  # the errors first: the tail of the log is the last scheduled task, and
  # what went wrong is usually well above it
  echo "==> ${f} (errors)" >&2
  grep -iE 'error|warn|exception|refused|timed out' "$f" | grep -v 'indexers.prowlarr.com\|prowlarr.servarr.com' | tail -60 >&2
  echo "==> ${f} (tail)" >&2
  tail -40 "$f" >&2
}

up() {
  command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }

  down >/dev/null 2>&1 || true
  config

  local host
  host="$(host_address)"
  log "starting ${IMAGE} as ${NAME} on ${PORT} (fakes reached via ${host}, no internet)"
  # the proxy is a closed port inside the container, so the definitions
  # service and the update check fail at once and Prowlarr carries on
  # offline; NO_PROXY lets the fakes on the host through
  docker run -d --name "$NAME" \
    -p "${PORT}:9696" \
    --hostname "$NAME" \
    --add-host "host.docker.internal:host-gateway" \
    -e "PUID=$(id -u)" -e "PGID=$(id -g)" -e "TZ=Etc/UTC" \
    -e "HTTP_PROXY=http://127.0.0.1:9" -e "HTTPS_PROXY=http://127.0.0.1:9" \
    -e "http_proxy=http://127.0.0.1:9" -e "https_proxy=http://127.0.0.1:9" \
    -e "NO_PROXY=localhost,127.0.0.1,${NAME},${host}" \
    -e "no_proxy=localhost,127.0.0.1,${NAME},${host}" \
    -v "${DATA}/config:/config" -v "${DATA}/downloads:/downloads" \
    "$IMAGE" >/dev/null

  wait_for "Prowlarr to answer /api/v1/system/status" 90 "curl -fsS -H 'X-Api-Key: ${API_KEY}' ${URL}/api/v1/system/status"

  # consumed with eval "$(scripts/testenv.sh up)"
  echo "export PROWLARR_SERVER='${URL}'"
  echo "export PROWLARR_TOKEN='${API_KEY}'"
  echo "export PROWLARR_TEST_DATA='${DATA}'"
  echo "export PROWLARR_TEST_CONTAINER='${NAME}'"
  echo "export PROWLARR_TEST_HOST='${host}'"
  echo "export PROWLARR_TEST_INDEXER_PORT='${INDEXER_PORT}'"
  echo "export PROWLARR_TEST_APP_PORT='${APP_PORT}'"
}

down() {
  log "removing ${NAME}"
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  wipe_data
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  logs) logs ;;
  *) echo "usage: $0 [up|down|logs]" >&2; exit 1 ;;
esac
