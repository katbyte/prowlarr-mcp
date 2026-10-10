package tools

// A tool that deletes changes nothing without confirm, and says what it would
// take: what is deleted here cannot be put back by adding it again, since an
// indexer's history, a profile's switches and a backup's contents go with it.

// confirmIn is the argument every tool that deletes takes.
type confirmIn struct {
	Confirm bool `json:"confirm,omitempty" jsonschema:"true to delete; without it nothing changes and the answer says what a confirmed call would remove"`
}

// deletedOut is what a delete answers: what it removed, or without confirm
// what it would.
type deletedOut struct {
	Deleted     string   `json:"deleted,omitempty"      jsonschema:"what was deleted"`
	WouldDelete string   `json:"would_delete,omitempty" jsonschema:"without confirm: what a confirmed call deletes; nothing has changed"`
	Loses       []string `json:"loses"                  jsonschema:"what goes with it or stops working because of it, which adding it again does not bring back"`
	Note        string   `json:"note,omitempty"         jsonschema:"what to do next, or what to do instead"`
}

// deleting answers a delete of what, which takes loses with it: without
// confirm that it would go, and with it that it went, once remove has run.
func deleting(confirm bool, what string, loses []string, note string, remove func() error) (deletedOut, error) {
	out := deletedOut{Loses: loses, Note: note}
	if !confirm {
		out.WouldDelete = what

		return out, nil
	}
	if err := remove(); err != nil {
		return deletedOut{}, err
	}
	out.Deleted = what

	return out, nil
}
