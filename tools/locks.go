package tools

import (
	"context"
	"strconv"

	"github.com/katbyte/go-kt/lock"
	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// The calls of one turn are kept from undoing each other with go-kt's locks.
// The MCP server runs a turn's calls at once, and an edit reads a record and
// sends it back whole: eight indexer_edit calls each adding a tag to one
// indexer each sent the tags as they were before the others, and two of nine
// stayed. The tag they shared was made by every call that found it missing.
//
// An edit locks the record it has resolved, and only then reads what it will
// write back. The locks are a set of this server's own (registry.locks),
// shared by every session it serves; Prowlarr's web interface, or another
// prowlarr-mcp, is not held back.

// tagsLock is the lock on the tags, which a tag made on the way to an edit
// reads the list of before adding to it: two calls making the same new tag
// would each find it missing.
const tagsLock = "tags"

// editing locks the record of type T with this id, for an edit that has
// resolved it and not yet read what it will send back, and the tags beside it
// when the edit may make one: a caller locks once at a time.
func editing[T any](r *registry, id int, makesTags bool) (unlock func()) {
	things := []lock.Thing{lock.ID[T](strconv.Itoa(id))}
	if makesTags {
		things = append(things, lock.String(tagsLock))
	}

	return r.locks.By(things...)
}

// makeTags resolves tags by label or id, making the ones that are not there,
// with the tags held for the length of it. It is for a caller that holds no
// lock of its own: one that does asks editing for the tags beside its record
// and calls resolveTags.
func (r *registry) makeTags(ctx context.Context, refs []string) ([]prowlarr.TagID, error) {
	unlock := r.locks.ByString(tagsLock)
	defer unlock()

	return r.resolveTags(ctx, refs, true)
}
