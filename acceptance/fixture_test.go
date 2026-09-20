//go:build integration

package acceptance

import (
	"time"

	"github.com/katbyte/prowlarr-mcp/internal/fakes/newznab"
	"github.com/katbyte/prowlarr-mcp/internal/fakes/servarr"
)

// The fixture world. Every audit has something here to find and something it
// must leave alone: a healthy usenet and torrent indexer each, and beside
// them one of each thing that goes wrong in a real Prowlarr.

// releases are what the healthy sites carry: TV and films, real titles with
// their real ids.
var releases = []newznab.Release{
	{Title: "Severance.S01E01.Good.News.About.Hell.1080p.ATVP.WEB-DL.DDP5.1.H.264-FAKE", Category: newznab.CategoryTVHD, Seeders: 50, Peers: 5, TVDBID: 371980},
	{Title: "Severance.S02E01.Hello.Ms.Cobel.1080p.ATVP.WEB-DL.DDP5.1.H.264-FAKE", Category: newznab.CategoryTVHD, Seeders: 80, Peers: 9, TVDBID: 371980},
	{Title: "The.Expanse.S01E01.Dulcinea.1080p.BluRay.x264-FAKE", Category: newznab.CategoryTVHD, Seeders: 12, Peers: 1, TVDBID: 280619},
	{Title: "Dune.Part.Two.2024.1080p.BluRay.x264-FAKE", Category: newznab.CategoryMoviesHD, Seeders: 120, Peers: 10, TMDBID: 693134, IMDBID: 15239678},
	{Title: "Arrival.2016.1080p.BluRay.x264-FAKE", Category: newznab.CategoryMoviesHD, Seeders: 30, Peers: 2, TMDBID: 329865, IMDBID: 2543164, Freeleech: true},
}

// animeReleases are what the anime site carries.
var animeReleases = []newznab.Release{
	{Title: "[FakeSubs] Frieren - 01 (1080p)", Category: newznab.CategoryTVAnime, Seeders: 200, Peers: 20},
}

// musicReleases are what the music site carries.
var musicReleases = []newznab.Release{
	{Title: "Pink Floyd - The Dark Side of the Moon (1973) [FLAC]", Category: newznab.CategoryAudio, Seeders: 40, Peers: 3},
}

// The fake sites, by the name they are served under.
const (
	siteUsenet  = "nzb-main"
	siteTorrent = "tor-main"
	siteAnime   = "tor-anime"
	siteMusic   = "tor-music"
	siteDown    = "tor-down"
	siteFlaky   = "tor-flaky"
	siteSlow    = "tor-slow"
	siteRevoked = "nzb-revoked"
)

// siteKeys are the keys the sites take.
var siteKeys = map[string]string{
	siteUsenet: "nzbkey", siteTorrent: "torkey", siteAnime: "animekey", siteMusic: "musickey",
	siteDown: "downkey", siteFlaky: "flakykey", siteSlow: "slowkey", siteRevoked: "revokedkey",
}

// slowDelay is how long the slow site holds every answer back: over
// slowThreshold, which the tests give audit_slow, and short enough not to
// hold up a run.
const (
	slowDelay     = 1500 * time.Millisecond
	slowThreshold = 1000
)

func sites() []newznab.Site {
	return []newznab.Site{
		{Name: siteUsenet, Protocol: newznab.Usenet, APIKey: siteKeys[siteUsenet], Releases: releases},
		{Name: siteTorrent, Protocol: newznab.Torrent, APIKey: siteKeys[siteTorrent], Releases: releases},
		{
			Name: siteAnime, Protocol: newznab.Torrent, APIKey: siteKeys[siteAnime], Releases: animeReleases,
			Categories: []newznab.Category{{ID: newznab.CategoryTV, Name: "TV", Subs: []newznab.Category{{ID: newznab.CategoryTVAnime, Name: "TV/Anime"}}}},
			Searches:   []string{"search", "tv-search"},
		},
		{
			Name: siteMusic, Protocol: newznab.Torrent, APIKey: siteKeys[siteMusic], Releases: musicReleases,
			Categories: []newznab.Category{{ID: newznab.CategoryAudio, Name: "Audio"}},
			Searches:   []string{"search", "music-search"},
		},
		{Name: siteDown, Protocol: newznab.Torrent, APIKey: siteKeys[siteDown], Releases: releases},
		{Name: siteFlaky, Protocol: newznab.Torrent, APIKey: siteKeys[siteFlaky], Releases: releases},
		{Name: siteSlow, Protocol: newznab.Torrent, APIKey: siteKeys[siteSlow], Releases: releases, Delay: slowDelay},
		{Name: siteRevoked, Protocol: newznab.Usenet, APIKey: siteKeys[siteRevoked], Releases: releases},
	}
}

// The indexers as Prowlarr knows them.
const (
	idxUsenet      = "Main Usenet"
	idxTorrent     = "Main Torrent"
	idxTorrentCopy = "Main Torrent Copy"
	idxAnime       = "Anime Torrent"
	idxMusic       = "Music Torrent"
	idxDown        = "Down Torrent"
	idxFlaky       = "Flaky Torrent"
	idxSlow        = "Slow Torrent"
	idxRevoked     = "Revoked Usenet"
	// the catalogue's own definitions, seeded by scripts/testenv.sh; the
	// container has no internet, so these are added with force and never
	// searched
	idx1337x  = "1337x"
	idxMilkie = "Milkie"
	idxTasman = "Across The Tasman"
)

// genericIndexer is one of the indexers added as a generic Newznab or
// Torznab pointed at a fake site.
type genericIndexer struct {
	Name, Site string
	Tags       []string
}

var genericIndexers = []genericIndexer{
	{Name: idxUsenet, Site: siteUsenet, Tags: []string{tagMovies}},
	{Name: idxTorrent, Site: siteTorrent, Tags: []string{tagMovies}},
	// the same site again: what audit_duplicates finds
	{Name: idxTorrentCopy, Site: siteTorrent},
	{Name: idxAnime, Site: siteAnime},
	{Name: idxMusic, Site: siteMusic},
	{Name: idxDown, Site: siteDown},
	{Name: idxFlaky, Site: siteFlaky},
	{Name: idxSlow, Site: siteSlow},
	{Name: idxRevoked, Site: siteRevoked},
}

// The applications, as the fakes serve them and as Prowlarr knows them.
const (
	appSonarr = "Sonarr"
	appRadarr = "Radarr"
	appLidarr = "Lidarr"
)

var fakeApps = []servarr.App{
	{Name: "sonarr", Kind: servarr.Sonarr, APIKey: "sonarrkey"},
	{Name: "radarr", Kind: servarr.Radarr, APIKey: "radarrkey"},
	// Lidarr is added to Prowlarr but not served: every call to it fails
}

// The tags.
const (
	tagMovies       = "movies"       // Main Usenet, Main Torrent, Radarr
	tagFlaresolverr = "flaresolverr" // the FlareSolverr proxy
	tagIdle         = "idle"         // a proxy no indexer carries
	tagMusicOnly    = "music-only"   // Lidarr, and no indexer
	tagStray        = "stray"        // nothing at all
)

// The sync profiles beside the default Standard.
const (
	profileNothing = "Nothing" // RSS, automatic and interactive search all off
	profileSpare   = "Spare"   // used by no indexer
)

// The proxies and download clients.
const (
	proxyFlare       = "FlareSolverr"
	proxyIdle        = "Idle Proxy"
	clientTorrent    = "Torrent Blackhole"
	clientUsenet     = "Usenet Blackhole"
	clientSpare      = "Spare Blackhole"
	retiredURL1337x  = "https://1337x.is/"
	currentURL1337x  = "https://1337x.to/"
	unlistedURLMilki = "https://milkie.example/"
)
