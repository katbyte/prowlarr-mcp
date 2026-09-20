package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/prowlarr-mcp/lib/prowlarr"
)

// Shared projections and lookups: the trimmed shapes every tool answers in,
// the settings every provider keeps in its fields, and the name resolution
// for the things Prowlarr only knows by id - indexers, applications, sync
// profiles, proxies, download clients and tags.

// boolv reads an optional flag, false when unset.
func boolv(b *bool) bool { return b != nil && *b }

// limitOr is a limit input with its default.
func limitOr(limit, def int) int {
	if limit <= 0 {
		return def
	}

	return limit
}

// daysOr is a days-back input with its default.
func daysOr(days, def int) int {
	if days <= 0 {
		return def
	}

	return days
}

// parseTime reads one of Prowlarr's timestamps; the zero time for none.
func parseTime(stamp string) time.Time {
	if stamp == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, stamp); err == nil {
			return t
		}
	}

	return time.Time{}
}

// stampUTC renders a time the way Prowlarr takes one in a query.
func stampUTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// humanSize renders a byte count the way a person reads a release size.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// percent is part of whole as a whole percentage, 0 for an empty whole.
func percent(part, whole int) int {
	if whole <= 0 {
		return 0
	}

	return int(math.Round(float64(part) * 100 / float64(whole)))
}

// hostOf is a URL's host name, lower case, "" when it has none.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	return strings.ToLower(u.Hostname())
}

// sameURL compares two URLs the way a person would: scheme and host
// ignoring case, a trailing slash ignored.
func sameURL(a, b string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.ToLower(strings.TrimSpace(s)), "/") }

	return a != "" && norm(a) == norm(b)
}

// Provider fields -----------------------------------------------------------

// Every provider Prowlarr holds - an indexer, an application, a proxy, a
// download client, a notification - keeps its settings as a list of named
// fields (baseUrl, apiKey, torrentBaseSettings.seedRatio...), each marked with
// how private its value is.

// field finds a provider's field by name, ignoring case.
func field(fields []prowlarr.Field, name string) *prowlarr.Field {
	for i := range fields {
		if strings.EqualFold(fields[i].Name, name) {
			return &fields[i]
		}
	}

	return nil
}

// fieldString is a field's value as text, "" when it is unset.
func fieldString(fields []prowlarr.Field, name string) string {
	f := field(fields, name)
	if f == nil || f.Value == nil {
		return ""
	}
	switch v := f.Value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// fieldNumber is a field's value as a number, and whether it is set.
func fieldNumber(fields []prowlarr.Field, name string) (float64, bool) {
	f := field(fields, name)
	if f == nil || f.Value == nil {
		return 0, false
	}
	switch v := f.Value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil
	}

	return 0, false
}

// fieldInts is a field's value as a list of numbers (sync categories).
func fieldInts(fields []prowlarr.Field, name string) []int {
	f := field(fields, name)
	if f == nil {
		return nil
	}
	list, ok := f.Value.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(list))
	for _, v := range list {
		if n, ok := v.(float64); ok {
			out = append(out, int(n))
		}
	}

	return out
}

// secretNames are the settings a definition names that hold a credential.
// Prowlarr marks the privacy of its native indexers' fields, but a catalogue
// definition's own settings (a password, a cookie, an API key, a 2FA code)
// all come back marked normal, so they are known by name as well.
var secretNames = []string{
	"username", "user", "password", "pass", "cookie", "cookies", "apikey", "api_key", "passkey", "pid", "uid",
	"rsskey", "authkey", "token", "secret", "pin", "2facode", "2fa_code", "alt2fatoken", "hash", "key",
}

// secret reports whether a field holds a credential: an API key, a password,
// a user name, a cookie.
func secret(f *prowlarr.Field) bool {
	if f.Privacy != "" && f.Privacy != prowlarr.PrivacyLevelNormal {
		return true
	}

	return strings.EqualFold(f.Type, "password") || slices.Contains(secretNames, strings.ToLower(f.Name))
}

// settings are a provider's settings as a map, without the credentials and
// without the empty ones: its URL, categories, seed goals, limits. A
// credential that is set is listed by name in secretsSet, so a caller can see
// that a key is there without seeing it. Info fields (the definition's notes
// to the user) are left out.
func settings(fields []prowlarr.Field) (out map[string]any, secretsSet []string) {
	out = map[string]any{}
	for i := range fields {
		f := &fields[i]
		if f.Type == "info" || strings.HasPrefix(f.Name, "info_") {
			continue
		}
		if empty(f.Value) {
			continue
		}
		if secret(f) {
			secretsSet = append(secretsSet, f.Name)
			continue
		}
		out[f.Name] = f.Value
	}
	slices.Sort(secretsSet)

	return out, secretsSet
}

// empty reports whether a field value is unset: null, "", or an empty list.
func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}

	return false
}

// setFields writes values into a provider's fields by name, converting each
// to what the field holds. A name the provider has no field for is an error
// that lists the ones it has, so a typo cannot silently save nothing.
func setFields(fields []prowlarr.Field, values map[string]any) error {
	for _, name := range sortedKeys(values) {
		f := field(fields, name)
		if f == nil {
			have := make([]string, 0, len(fields))
			for _, x := range fields {
				if x.Type != "info" && !strings.HasPrefix(x.Name, "info_") {
					have = append(have, x.Name)
				}
			}
			return fmt.Errorf("no setting %q (have: %s)", name, strings.Join(have, ", "))
		}
		v, err := fieldValue(f, values[name])
		if err != nil {
			return fmt.Errorf("setting %s: %w", f.Name, err)
		}
		f.Value = v
	}

	return nil
}

// fieldValue converts a value a caller gave into what a field holds: a
// number for a number field (from "2.5" as well as 2.5), true or false for a
// checkbox, a list for a multi-select, the text as it is otherwise. A
// select takes the option's value or its name.
func fieldValue(f *prowlarr.Field, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch strings.ToLower(f.Type) {
	case "number":
		switch x := v.(type) {
		case float64, int:
			return x, nil
		case string:
			if strings.TrimSpace(x) == "" {
				return nil, nil
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", x)
			}
			return n, nil
		}
	case "checkbox":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(x))
			if err != nil {
				return nil, fmt.Errorf("%q is not true or false", x)
			}
			return b, nil
		}
	case "select":
		if s, ok := v.(string); ok && len(f.SelectOptions) > 0 {
			for _, o := range f.SelectOptions {
				if strings.EqualFold(o.Name, s) || strconv.Itoa(o.Value) == strings.TrimSpace(s) {
					return o.Value, nil
				}
			}
			names := make([]string, 0, len(f.SelectOptions))
			for _, o := range f.SelectOptions {
				names = append(names, o.Name)
			}
			return nil, fmt.Errorf("%q is not one of: %s", s, strings.Join(names, ", "))
		}
	}

	return v, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}

// Lookups ---------------------------------------------------------------------

// named is a thing Prowlarr knows by id that a tool accepts by name.
type named interface {
	prowlarr.IndexerResource | prowlarr.ApplicationResource | prowlarr.AppProfileResource |
		prowlarr.IndexerProxyResource | prowlarr.DownloadClientResource | prowlarr.NotificationResource
}

// nameAndID reads the name and id every named thing carries.
func nameAndID[T named](x *T) (name string, id int) {
	switch v := any(x).(type) {
	case *prowlarr.IndexerResource:
		return v.Name, v.Id
	case *prowlarr.ApplicationResource:
		return v.Name, v.Id
	case *prowlarr.AppProfileResource:
		return v.Name, v.Id
	case *prowlarr.IndexerProxyResource:
		return v.Name, v.Id
	case *prowlarr.DownloadClientResource:
		return v.Name, v.Id
	case *prowlarr.NotificationResource:
		return v.Name, v.Id
	}

	return "", 0
}

// find picks one of a list by name (any case) or id, and otherwise answers an
// error naming what there is: "no indexer "x" (have: a, b)".
func find[T named](list []T, ref, kind string) (*T, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("name the %s", kind)
	}
	names := make([]string, 0, len(list))
	for i := range list {
		name, id := nameAndID(&list[i])
		if strings.EqualFold(name, ref) || strconv.Itoa(id) == ref {
			return &list[i], nil
		}
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no %s %q: Prowlarr has none", kind, ref)
	}

	return nil, fmt.Errorf("no %s %q (have: %s)", kind, ref, strings.Join(names, ", "))
}

// indexers reads every indexer.
func (r *registry) indexers(ctx context.Context) ([]prowlarr.IndexerResource, error) {
	res, err := r.client.GetIndexer(ctx)
	if err != nil {
		return nil, err
	}

	return res.Model, nil
}

// resolveIndexer finds an indexer by name or id.
func (r *registry) resolveIndexer(ctx context.Context, ref string) (*prowlarr.IndexerResource, error) {
	all, err := r.indexers(ctx)
	if err != nil {
		return nil, err
	}

	return find(all, ref, "indexer")
}

// resolveIndexers finds several indexers by name or id; an empty list is
// every one.
func (r *registry) resolveIndexers(ctx context.Context, refs []string) ([]prowlarr.IndexerResource, error) {
	all, err := r.indexers(ctx)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return all, nil
	}
	var out []prowlarr.IndexerResource
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		i, err := find(all, ref, "indexer")
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(out, func(x prowlarr.IndexerResource) bool { return x.Id == i.Id }) {
			out = append(out, *i)
		}
	}

	return out, nil
}

// indexerNames maps indexer ids to names.
func indexerNames(list []prowlarr.IndexerResource) map[int]string {
	out := make(map[int]string, len(list))
	for _, i := range list {
		out[i.Id] = i.Name
	}

	return out
}

// resolveApp finds an application by name or id.
func (r *registry) resolveApp(ctx context.Context, ref string) (*prowlarr.ApplicationResource, error) {
	res, err := r.client.GetApplications(ctx)
	if err != nil {
		return nil, err
	}

	return find(res.Model, ref, "application")
}

// resolveProfile finds a sync profile by name or id.
func (r *registry) resolveProfile(ctx context.Context, ref string) (*prowlarr.AppProfileResource, error) {
	res, err := r.client.GetAppProfile(ctx)
	if err != nil {
		return nil, err
	}

	return find(res.Model, ref, "sync profile")
}

// profileNames maps sync profile ids to names.
func (r *registry) profileNames(ctx context.Context) (map[int]string, error) {
	res, err := r.client.GetAppProfile(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(res.Model))
	for _, p := range res.Model {
		out[p.Id] = p.Name
	}

	return out, nil
}

// resolveDownloadClient finds a download client by name or id.
func (r *registry) resolveDownloadClient(ctx context.Context, ref string) (*prowlarr.DownloadClientResource, error) {
	res, err := r.client.GetDownloadClient(ctx)
	if err != nil {
		return nil, err
	}

	return find(res.Model, ref, "download client")
}

// Tags ----------------------------------------------------------------------

// tagLabels maps tag ids to labels.
func (r *registry) tagLabels(ctx context.Context) (map[int]string, error) {
	res, err := r.client.GetTag(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(res.Model))
	for _, t := range res.Model {
		out[t.Id] = t.Label
	}

	return out, nil
}

// labels names a list of tag ids, sorted.
func labels(ids []int, byID map[int]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if l, ok := byID[id]; ok {
			out = append(out, l)
		} else {
			out = append(out, strconv.Itoa(id))
		}
	}
	slices.Sort(out)

	return out
}

// resolveTags turns tag labels into ids. A label Prowlarr does not have is
// created when create is set, and an error naming the existing labels
// otherwise. Prowlarr stores labels lower case, so matching ignores case.
func (r *registry) resolveTags(ctx context.Context, refs []string, create bool) ([]int, error) {
	res, err := r.client.GetTag(ctx)
	if err != nil {
		return nil, err
	}
	out := []int{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		id := 0
		for _, t := range res.Model {
			if strings.EqualFold(t.Label, ref) || strconv.Itoa(t.Id) == ref {
				id = t.Id
				break
			}
		}
		if id == 0 {
			if !create {
				have := make([]string, 0, len(res.Model))
				for _, t := range res.Model {
					have = append(have, t.Label)
				}
				slices.Sort(have)
				return nil, fmt.Errorf("no tag %q (have: %s)", ref, strings.Join(have, ", "))
			}
			made, err := r.client.PostTag(ctx, prowlarr.TagResource{Label: strings.ToLower(ref)})
			if err != nil {
				return nil, fmt.Errorf("creating tag %q: %w", ref, err)
			}
			id = made.Model.Id
			res.Model = append(res.Model, *made.Model)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}

	return out, nil
}

// editTags applies a tag change: replace sets the list, add and remove edit
// the one there is.
func editTags(have, set, add, remove []int, replace bool) []int {
	out := slices.Clone(have)
	if replace {
		out = slices.Clone(set)
	}
	for _, id := range add {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	out = slices.DeleteFunc(out, func(id int) bool { return slices.Contains(remove, id) })
	if out == nil {
		out = []int{}
	}
	slices.Sort(out)

	return out
}

// Commands -----------------------------------------------------------------

// commandWait is how long a tool that runs a command waits for it by
// default: long enough for a sync to a few applications or a health check,
// short enough that a slow one is reported as still running rather than
// hanging the session.
const commandWait = 60 * time.Second

// commandPoll is how often a running command is looked at.
var commandPoll = 500 * time.Millisecond

// commandOut is what a tool that ran a command reports about it.
type commandOut struct {
	ID       int    `json:"command_id"`
	Name     string `json:"command"`
	Status   string `json:"status"             jsonschema:"queued, started, completed, failed, aborted or cancelled; queued or started means it was still running when the wait ended"`
	Message  string `json:"message,omitempty"  jsonschema:"what Prowlarr said about the run"`
	Started  string `json:"started,omitempty"`
	Ended    string `json:"ended,omitempty"`
	Duration string `json:"duration,omitempty"`
}

func projectCommand(c *prowlarr.CommandResource) commandOut {
	name := c.CommandName
	if name == "" {
		name = c.Name
	}

	return commandOut{
		ID: c.Id, Name: name, Status: string(c.Status), Message: c.Message,
		Started: c.Started, Ended: c.Ended, Duration: c.Duration,
	}
}

// runCommand queues a command with its own fields, and waits up to wait for
// it to finish (0 does not wait). A command that fails is an error; one still
// running when the wait ends is not, and is reported as it stands.
func (r *registry) runCommand(ctx context.Context, name string, fields map[string]any, wait time.Duration) (commandOut, error) {
	body := map[string]any{"name": name}
	maps.Copy(body, fields)
	raw, err := json.Marshal(body)
	if err != nil {
		return commandOut{}, err
	}
	res, err := r.client.PostCommand(ctx, raw)
	if err != nil {
		return commandOut{}, fmt.Errorf("queueing %s: %w", name, err)
	}
	cmd := res.Model
	if wait <= 0 {
		return projectCommand(cmd), nil
	}

	deadline := time.Now().Add(wait)
	for !commandDone(cmd.Status) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return projectCommand(cmd), ctx.Err()
		case <-time.After(commandPoll):
		}
		got, err := r.client.GetCommandById(ctx, cmd.Id)
		if err != nil {
			return projectCommand(cmd), fmt.Errorf("checking %s: %w", name, err)
		}
		cmd = got.Model
	}
	out := projectCommand(cmd)
	switch cmd.Status {
	case prowlarr.CommandStatusFailed, prowlarr.CommandStatusAborted, prowlarr.CommandStatusCancelled, prowlarr.CommandStatusOrphaned:
		msg := cmd.Message
		if cmd.Exception != "" {
			msg = strings.TrimSpace(msg + " " + firstLine(cmd.Exception))
		}
		return out, fmt.Errorf("%s %s: %s", name, cmd.Status, msg)
	default:
	}

	return out, nil
}

// commandDone reports whether a command has stopped, one way or another.
func commandDone(s prowlarr.CommandStatus) bool {
	switch s {
	case prowlarr.CommandStatusQueued, prowlarr.CommandStatusStarted, "":
		return false
	default:
		return true
	}
}

// waitFor is the wait a tool's input asks for: the default when unset, none
// when negative.
func waitFor(seconds int) time.Duration {
	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return commandWait
	default:
		return time.Duration(seconds) * time.Second
	}
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(before)
	}

	return strings.TrimSpace(s)
}
