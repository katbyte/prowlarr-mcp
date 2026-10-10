package tools

import "github.com/katbyte/go-kt/outside"

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

// outsideLines is each of a list of messages that came from outside, made
// safe to show.
func outsideLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, outsideProse(line))
	}

	return out
}
