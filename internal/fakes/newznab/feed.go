package newznab

import (
	"crypto/sha1" //nolint:gosec // a torrent's info hash, which is SHA-1 by definition
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// capsXML is a site's capabilities document: its limits, the kinds of search
// it offers with their parameters, and its categories.
func capsXML(site *Site) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<caps>\n")
	fmt.Fprintf(&b, "  <server version=\"1.0\" title=%q/>\n", site.Name)
	b.WriteString("  <limits max=\"100\" default=\"100\"/>\n  <searching>\n")
	params := map[string]string{
		"search":       "q",
		"tv-search":    "q,season,ep,imdbid,tvdbid",
		"movie-search": "q,imdbid,tmdbid",
		"music-search": "q,artist,album",
		"audio-search": "q,artist,album",
		"book-search":  "q,author,title",
	}
	for _, kind := range []string{"search", "tv-search", "movie-search", "music-search", "audio-search", "book-search"} {
		on := slices.Contains(site.Searches, kind) || kind == "audio-search" && slices.Contains(site.Searches, "music-search")
		avail := "no"
		if on {
			avail = "yes"
		}
		fmt.Fprintf(&b, "    <%s available=%q supportedParams=%q/>\n", kind, avail, params[kind])
	}
	b.WriteString("  </searching>\n  <categories>\n")
	for _, c := range site.Categories {
		fmt.Fprintf(&b, "    <category id=\"%d\" name=%q>\n", c.ID, c.Name)
		for _, sub := range c.Subs {
			fmt.Fprintf(&b, "      <subcat id=\"%d\" name=%q/>\n", sub.ID, sub.Name)
		}
		b.WriteString("    </category>\n")
	}
	b.WriteString("  </categories>\n</caps>\n")

	return b.String()
}

// parentOf is a category's top-level category: 5040 is under 5000.
func parentOf(cat int) int { return cat / 1000 * 1000 }

// matches reports whether a release answers a query: every word of q in its
// title, a category asked for (its own or its parent), and any id asked for.
func matches(r *Release, q url.Values) bool {
	title := strings.ToLower(r.Title)
	for w := range strings.FieldsSeq(strings.ToLower(q.Get("q"))) {
		if !strings.Contains(title, w) {
			return false
		}
	}
	if cats := q.Get("cat"); cats != "" {
		ok := false
		for c := range strings.SplitSeq(cats, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(c))
			if err == nil && (n == r.Category || n == parentOf(r.Category)) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	for _, id := range []struct {
		param string
		have  int
	}{{"imdbid", r.IMDBID}, {"tmdbid", r.TMDBID}, {"tvdbid", r.TVDBID}} {
		want := strings.TrimPrefix(q.Get(id.param), "tt")
		if want == "" {
			continue
		}
		if n, err := strconv.Atoi(want); err != nil || n != id.have {
			return false
		}
	}

	return true
}

// feed is a search's answer: the matching releases, newest first, paged by
// offset and limit.
func (s *Server) feed(site *Site, q url.Values) string {
	var hits []*Release
	for i := range site.Releases {
		if matches(&site.Releases[i], q) {
			hits = append(hits, &site.Releases[i])
		}
	}
	slices.SortStableFunc(hits, func(a, b *Release) int { return b.PubDate.Compare(a.PubDate) })
	total := len(hits)
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 || limit > 100 {
		limit = 100
	}
	hits = hits[min(offset, len(hits)):]
	hits = hits[:min(limit, len(hits))]

	ns, enclosure := "newznab", "application/x-nzb"
	if site.Protocol == Torrent {
		ns, enclosure = "torznab", "application/x-bittorrent"
	}
	base := s.base(site)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/" xmlns:torznab="http://torznab.com/schemas/2015/feed">` + "\n<channel>\n")
	fmt.Fprintf(&b, "<title>%s</title>\n<description>a fake %s indexer</description>\n", xmlEscape(site.Name), site.Protocol)
	fmt.Fprintf(&b, "<%s:response offset=\"%d\" total=\"%d\"/>\n", ns, offset, total)
	for _, r := range hits {
		link := base + "/download/" + r.GUID
		b.WriteString("<item>\n")
		fmt.Fprintf(&b, "  <title>%s</title>\n", xmlEscape(r.Title))
		fmt.Fprintf(&b, "  <guid isPermaLink=\"true\">%s/details/%s</guid>\n", base, r.GUID)
		fmt.Fprintf(&b, "  <link>%s</link>\n", xmlEscape(link))
		fmt.Fprintf(&b, "  <comments>%s/details/%s#comments</comments>\n", base, r.GUID)
		fmt.Fprintf(&b, "  <pubDate>%s</pubDate>\n", r.PubDate.UTC().Format(time.RFC1123Z))
		fmt.Fprintf(&b, "  <size>%d</size>\n", r.Size)
		fmt.Fprintf(&b, "  <enclosure url=%q length=\"%d\" type=%q/>\n", link, r.Size, enclosure)
		attr := func(name string, value any) { fmt.Fprintf(&b, "  <%s:attr name=%q value=\"%v\"/>\n", ns, name, value) }
		attr("category", parentOf(r.Category))
		attr("category", r.Category)
		attr("size", r.Size)
		attr("grabs", r.Grabs)
		if r.IMDBID != 0 {
			attr("imdb", fmt.Sprintf("%07d", r.IMDBID))
		}
		if r.TMDBID != 0 {
			attr("tmdbid", r.TMDBID)
		}
		if r.TVDBID != 0 {
			attr("tvdbid", r.TVDBID)
		}
		if site.Protocol == Torrent {
			attr("seeders", r.Seeders)
			attr("peers", r.Seeders+r.Peers)
			attr("infohash", infoHash(r))
			down := 1
			if r.Freeleech {
				down = 0
			}
			attr("downloadvolumefactor", down)
			attr("uploadvolumefactor", 1)
		} else {
			attr("usenetdate", r.PubDate.UTC().Format(time.RFC1123Z))
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel>\n</rss>\n")

	return b.String()
}

// nzbFile is a release's NZB: one file of one segment, enough for a download
// client to accept.
func nzbFile(r *Release) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="fake@prowlarr-mcp" date="%d" subject="%s - &quot;%s.mkv&quot; yEnc (1/1)">
    <groups><group>alt.binaries.test</group></groups>
    <segments><segment bytes="%d" number="1">%s@prowlarr-mcp</segment></segments>
  </file>
</nzb>
`, r.PubDate.Unix(), xmlEscape(r.Title), xmlEscape(r.Title), r.Size, r.GUID)
}

// torrentPieceLength is the piece size of the fake torrents.
const torrentPieceLength = 1 << 22

// torrentInfo is a release's torrent info dictionary, bencoded: one file of
// its size, and a hash for every piece that size takes (zeroes: nothing is
// ever downloaded), which a torrent parser checks the count of.
func torrentInfo(r *Release) string {
	name := r.Title + ".mkv"
	pieces := int((r.Size + torrentPieceLength - 1) / torrentPieceLength)
	hashes := strings.Repeat("\x00", 20*max(pieces, 1))

	return fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi%de6:pieces%d:%s7:privatei1ee",
		r.Size, len(name), name, torrentPieceLength, len(hashes), hashes)
}

// torrentFile is a release's .torrent: a tracker and the info dictionary.
func torrentFile(r *Release) []byte {
	announce := "http://tracker.invalid/announce" //nolint:revive // a tracker that is never reached, in a file a client must parse

	return []byte(fmt.Sprintf("d8:announce%d:%s4:info%se", len(announce), announce, torrentInfo(r)))
}

// infoHash is the SHA-1 of a release's info dictionary, as a torrent client
// computes it.
func infoHash(r *Release) string {
	sum := sha1.Sum([]byte(torrentInfo(r))) //nolint:gosec // the info hash is SHA-1 by definition

	return hex.EncodeToString(sum[:])
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
