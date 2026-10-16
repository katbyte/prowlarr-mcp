package tools

import mcpregistry "github.com/katbyte/go-kt/mcp/registry"

// Every list takes limit and offset and answers total and next_offset, so a
// client reads a long one a page at a time and always knows how much there is:
// a list cut short with no count reads as the whole of it.

// maxLimit caps a page: a limit of a million is one call reading everything,
// which is what the limit is there to stop.
const maxLimit = 1000

// defaultLimit is the page a list answers when no limit is given: one default
// for every list, so a client need not learn each one's.
const defaultLimit = 100

// pageIn is the two arguments every list takes.
type pageIn struct {
	Limit  int `json:"limit,omitempty"  jsonschema:"rows to return, default 100, at most 1000"`
	Offset int `json:"offset,omitempty" jsonschema:"rows to skip: a previous page's next_offset"`
}

// pageOut is where a list's rows sit in the whole.
type pageOut struct {
	Total      int `json:"total"                 jsonschema:"rows in all, before limit and offset"`
	Offset     int `json:"offset"                jsonschema:"how many rows were skipped"`
	NextOffset int `json:"next_offset,omitempty" jsonschema:"pass back as offset for the rows after these; absent when these were the last"`
}

// pageArgs is the limit and offset a list uses for the ones it was asked for:
// the default when no limit is given, never more than maxLimit, never before
// the start.
func pageArgs(asked, from, def int) (limit, offset int) {
	return min(mcpregistry.LimitOr(asked, def), maxLimit), max(from, 0)
}

// pageOf is one page of a list held whole: at most limit rows from offset,
// and where the next page starts, 0 when this was the last.
func pageOf[T any](all []T, limit, offset int) (page []T, next int) {
	offset = min(offset, len(all))
	page = all[offset:min(offset+limit, len(all))]

	return page, mcpregistry.NextOffset(offset, len(page), len(all))
}

// paged is the page of rows a list was asked for and where it sits in the
// whole: limit and offset as given, def the page when no limit was.
func paged[T any](rows []T, limit, offset, def int) ([]T, pageOut) {
	limit, offset = pageArgs(limit, offset, def)
	page, next := pageOf(rows, limit, offset)

	return page, pageOut{Total: len(rows), Offset: min(offset, len(rows)), NextOffset: next}
}
