package tools

import (
	"regexp"
	"strings"

	"github.com/katbyte/go-kt/outside"
)

// Text that strangers wrote reaches a model through these tools: a release's
// name as a public indexer gave it, what a site answered a test with, a line
// of Prowlarr's log quoting either. go-kt's outside.Text takes out what does
// not show and cuts what is left to a length. What a later call hands back to
// name something - a release's guid, a definition's name, an indexer's - is
// left as it came, or it would no longer find it.
const (
	// nameLimit is for a name: a release's, a query's.
	nameLimit = 300
	// proseLimit is for running text: a message, a description, a line of a log.
	proseLimit = 2000
)

// outsideName is a name that came from outside, made safe to show.
func outsideName(s string) string { return outside.Text(s, nameLimit) }

// outsideProse is running text that came from outside, made safe to show.
func outsideProse(s string) string { return outside.Text(s, proseLimit) }

// outsideSaid is what Prowlarr, a site or an application said went wrong,
// made safe to show: cleaned as running text is, and with every address in
// it blanked of what may be a credential. Prowlarr quotes the whole address
// of a request that failed, and an indexer's API key is one of its
// parameters: "HTTP request failed: [503] [GET] at [http://site/api?t=search&apikey=...]".
func outsideSaid(s string) string { return outsideProse(blankAddresses(s)) }

// outsideLines is each of a list of messages that came from outside, made
// safe to show.
func outsideLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, outsideSaid(line))
	}

	return out
}

// addressIn finds an address in running text: it runs to a space, a quote or
// a closing bracket.
var addressIn = regexp.MustCompile(`https?://[^\s"'<>\])]+`)

// blankAddresses is text with every address in it blanked of what may be a
// credential (see blankAddress).
func blankAddresses(s string) string {
	return addressIn.ReplaceAllStringFunc(s, blankAddress)
}

// blankAddress is an address without what it may carry of a credential: who
// it signs in as, the value of each of its parameters and what follows its
// #. The parameters' names stay, which say what was asked without saying
// with what. A credential in the path itself, as some trackers put a passkey,
// cannot be told from the rest of the path and is left.
func blankAddress(address string) string {
	rest, fragment, _ := strings.Cut(address, "#")
	rest, query, _ := strings.Cut(rest, "?")

	scheme, after, _ := strings.Cut(rest, "://")
	host, path, hasPath := strings.Cut(after, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = hidden + host[at:]
	}
	out := scheme + "://" + host
	if hasPath {
		out += "/" + path
	}

	if query != "" {
		parameters := strings.Split(query, "&")
		for i, parameter := range parameters {
			if name, _, has := strings.Cut(parameter, "="); has {
				parameters[i] = name + "=" + hidden
			}
		}
		out += "?" + strings.Join(parameters, "&")
	}
	if fragment != "" {
		out += "#" + hidden
	}

	return out
}
