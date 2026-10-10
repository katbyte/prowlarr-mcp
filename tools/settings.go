package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prowlarr's own settings are kept in sections, each read and written back
// whole: host (how it is reached, who may sign in, its proxy, logs, backups
// and updates), ui (how its web interface shows dates and which theme) and
// development (what its logs record). A fourth, downloadclient, holds nothing
// in Prowlarr, so it is not offered.

// settingsSection is one section as the tools read and write it: every
// setting by the name Prowlarr's API gives it.
type settingsSection struct {
	name  string
	about string
	// names are the settings the section has, whether or not Prowlarr sent
	// each: one that is off or unset is left out of an answer
	names []string
	read  func(ctx context.Context) (map[string]any, error)
	write func(ctx context.Context, values map[string]any) error
}

// sectionOf makes a section of one of the SDK's settings resources: read into
// a map so a setting can be named, and written back through the resource so a
// value of the wrong kind is refused before anything is sent.
func sectionOf[T any](name, about string, get func(context.Context) (*T, error), put func(context.Context, int, T) error) settingsSection {
	var names []string
	for f := range reflect.TypeFor[T]().Fields() {
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag != "" && tag != "id" {
			names = append(names, tag)
		}
	}
	slices.Sort(names)

	return settingsSection{
		name: name, about: about, names: names,
		read: func(ctx context.Context) (map[string]any, error) {
			v, err := get(ctx)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			out := map[string]any{}
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}

			return out, nil
		},
		write: func(ctx context.Context, values map[string]any) error {
			raw, err := json.Marshal(values)
			if err != nil {
				return err
			}
			var v T
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&v); err != nil {
				return fmt.Errorf("a %s setting was given a value of the wrong kind: %w", name, err)
			}
			id, ok := values["id"].(float64)
			if !ok {
				return fmt.Errorf("the %s settings as read carry no id to save them under", name)
			}

			return put(ctx, int(id), v)
		},
	}
}

// settingsSections are the sections Prowlarr keeps settings in.
func (r *registry) settingsSections() []settingsSection {
	pc := r.client

	return []settingsSection{
		sectionOf("host", "how Prowlarr is reached and who may sign in, its own proxy, its logs, backups, history clean-up and updates",
			func(ctx context.Context) (*prowlarr.HostConfigResource, error) {
				res, err := pc.GetConfigHost(ctx)
				return res.Model, err
			},
			func(ctx context.Context, id int, v prowlarr.HostConfigResource) error {
				_, err := pc.PutConfigHostById(ctx, id, v)
				return err
			}),
		sectionOf("ui", "how the web interface shows dates and times, its language and its theme",
			func(ctx context.Context) (*prowlarr.UiConfigResource, error) {
				res, err := pc.GetConfigUi(ctx)
				return res.Model, err
			},
			func(ctx context.Context, id int, v prowlarr.UiConfigResource) error {
				_, err := pc.PutConfigUiById(ctx, id, v)
				return err
			}),
		sectionOf("development", "what Prowlarr's logs record beyond the usual: each indexer's answers, its SQL, and how many log files it keeps",
			func(ctx context.Context) (*prowlarr.DevelopmentConfigResource, error) {
				res, err := pc.GetConfigDevelopment(ctx)
				return res.Model, err
			},
			func(ctx context.Context, id int, v prowlarr.DevelopmentConfigResource) error {
				_, err := pc.PutConfigDevelopmentById(ctx, id, v)
				return err
			}),
	}
}

// secretSetting reports whether a setting is a credential, whose value no
// answer shows.
func secretSetting(name string) bool {
	lower := strings.ToLower(name)

	return lower == "apikey" || strings.Contains(lower, "password")
}

// reachSettings are the settings that decide how Prowlarr is reached and who
// may sign in. Changing one can cut prowlarr-mcp off from it, or lock its
// owner out, and this tool cannot put that back: they take confirm.
var reachSettings = []string{
	"port", "sslPort", "enableSsl", "bindAddress", "urlBase", "apiKey", "allowedHosts", "trustedNetworks",
	"authenticationMethod", "authenticationRequired", "username", "password", "passwordConfirmation",
	"sslCertPath", "sslCertPassword",
}

// shownSettings is a section's settings without its credentials, and the
// names of the credentials that are set.
func shownSettings(values map[string]any) (shown map[string]any, credentials []string) {
	shown = map[string]any{}
	for name, v := range values {
		switch {
		case name == "id":
		case !secretSetting(name):
			shown[name] = v
		case v != nil && v != "":
			credentials = append(credentials, name)
		}
	}
	slices.Sort(credentials)

	return shown, credentials
}

type settingChange struct {
	Setting string `json:"setting"`
	From    any    `json:"from"    jsonschema:"what it was; a credential's value is not shown"`
	To      any    `json:"to"      jsonschema:"what it is set to; a credential's value is not shown"`
}

func registerSettingsTools(r *registry) {
	sectionNamed := func(name string) (*settingsSection, error) {
		all := r.settingsSections()
		names := make([]string, 0, len(all))
		for i := range all {
			if strings.EqualFold(all[i].name, strings.TrimSpace(name)) {
				return &all[i], nil
			}
			names = append(names, all[i].name)
		}

		return nil, fmt.Errorf("no settings section %q (have: %s)", name, strings.Join(names, ", "))
	}

	type sectionOut struct {
		Section     string         `json:"section"`
		About       string         `json:"about"`
		Settings    map[string]any `json:"settings"    jsonschema:"every setting by the name server_settings_edit takes, without credentials; one that is off or unset may be left out"`
		Credentials []string       `json:"credentials" jsonschema:"the credentials that are set, by name; their values are never shown"`
	}
	type settingsOut struct {
		Sections []sectionOut `json:"sections"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_settings",
		Description: "Prowlarr's own settings, by section: host (how it is reached, who may sign in, its own proxy, its log level, how often it backs up and how long it keeps history, how it updates), ui (dates, language, theme) and development (what its logs record). Credentials are named when set and never shown. server_settings_edit changes them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Section string `json:"section,omitempty" jsonschema:"only this section: host, ui or development; default all three"`
	},
	) (*mcp.CallToolResult, settingsOut, error) {
		sections := r.settingsSections()
		if in.Section != "" {
			one, err := sectionNamed(in.Section)
			if err != nil {
				return nil, settingsOut{}, err
			}
			sections = []settingsSection{*one}
		}
		out := settingsOut{}
		for _, sec := range sections {
			values, err := sec.read(ctx)
			if err != nil {
				return nil, settingsOut{}, fmt.Errorf("%s settings: %w", sec.name, err)
			}
			row := sectionOut{Section: sec.name, About: sec.about}
			row.Settings, row.Credentials = shownSettings(values)
			out.Sections = append(out.Sections, row)
		}

		return nil, out, nil
	})

	type editIn struct {
		Section  string         `json:"section"           jsonschema:"host, ui or development"`
		Settings map[string]any `json:"settings"          jsonschema:"the settings to change, by the name server_settings shows them under, each with its new value: logLevel, backupInterval, backupRetention, historyCleanupDays, theme and the rest. A name the section does not have is refused with the ones it does"`
		Confirm  bool           `json:"confirm,omitempty" jsonschema:"true to change a setting that decides how Prowlarr is reached or who may sign in (its port, address, URL base, SSL, API key, the host names it answers to, the networks it trusts, authentication, username or password); without it such a change is not made, and the answer says what it would be"`
	}
	type editOut struct {
		Section     string          `json:"section"`
		Changed     []settingChange `json:"changed"                jsonschema:"what was changed, as Prowlarr reads back"`
		NotKept     []settingChange `json:"not_kept,omitempty"     jsonschema:"what Prowlarr took without complaint and did not keep: read back, the setting is as it was. Some only hold beside another, as a username does beside a way of signing in"`
		WouldChange []settingChange `json:"would_change,omitempty" jsonschema:"without confirm: what a confirmed call changes; nothing has changed"`
		Note        string          `json:"note,omitempty"         jsonschema:"what the change means, or what has to happen next"`
		Settings    map[string]any  `json:"settings"               jsonschema:"the section as it is now, without credentials"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "server_settings_edit",
		Description: "Change Prowlarr's own settings, by section and name: its log level, how often it backs up and how many backups it keeps, how long it keeps history, its own proxy, its theme and date formats, what its logs record. A setting that decides how Prowlarr is reached or who may sign in changes nothing without confirm, since getting it wrong cuts this tool off from Prowlarr with no way to put it back from here; some of those take effect only once Prowlarr has restarted, which server_restart does. Prowlarr refuses to save the host section while allowedHosts is empty unless every request has to sign in, so on such a server the first change to it has to name the hosts Prowlarr answers to (a comma separated list of names or addresses, with no port), which is also what clears its allowed hosts health warning. server_settings shows what there is to change.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, editOut, error) {
		sec, err := sectionNamed(in.Section)
		if err != nil {
			return nil, editOut{}, err
		}
		if len(in.Settings) == 0 {
			return nil, editOut{}, errors.New("nothing to change: name a setting and its new value")
		}

		// the section is written back whole, so it is read once no other edit of it can run
		unlock := r.locks.ByString("settings/" + sec.name)
		defer unlock()

		values, err := sec.read(ctx)
		if err != nil {
			return nil, editOut{}, err
		}
		out := editOut{Section: sec.name}
		var changes []settingChange
		reach := false
		for _, asked := range sortedKeys(in.Settings) {
			i := slices.IndexFunc(sec.names, func(n string) bool { return strings.EqualFold(n, strings.TrimSpace(asked)) })
			if i < 0 {
				return nil, editOut{}, fmt.Errorf("the %s section has no setting %q (have: %s)", sec.name, asked, strings.Join(sec.names, ", "))
			}
			name, to := sec.names[i], in.Settings[asked]
			if sameJSON(values[name], to) {
				continue
			}
			change := settingChange{Setting: name, From: values[name], To: to}
			if secretSetting(name) {
				change.From, change.To = "(not shown)", "(not shown)"
			}
			changes = append(changes, change)
			values[name] = to
			reach = reach || slices.Contains(reachSettings, name)
		}
		if len(changes) == 0 {
			return nil, editOut{}, errors.New("nothing to change: every setting named already has that value")
		}
		if reach && !in.Confirm {
			out.WouldChange = changes
			out.Note = "nothing has changed: at least one of these decides how Prowlarr is reached or who may sign in, and a wrong value cuts prowlarr-mcp off from it. confirm=true makes the change"
			if now, readErr := sec.read(ctx); readErr == nil {
				out.Settings, _ = shownSettings(now)
			}

			return nil, out, nil
		}

		if err := sec.write(ctx, values); err != nil {
			return nil, editOut{}, err
		}
		// read back rather than trusted: what Prowlarr kept is what it says
		// now, and it takes some settings without keeping them
		after, err := sec.read(ctx)
		if err != nil {
			return nil, out, fmt.Errorf("the %s settings were saved, but reading them back failed: %w", sec.name, err)
		}
		for _, change := range changes {
			// a credential is not read back as it was sent, so it cannot be compared
			if secretSetting(change.Setting) || sameJSON(after[change.Setting], change.To) {
				out.Changed = append(out.Changed, change)
				continue
			}
			out.NotKept = append(out.NotKept, change)
		}
		if reach && len(out.Changed) > 0 {
			out.Note = "a change to how Prowlarr is reached takes effect once it has restarted (server_restart), and prowlarr-mcp must then be given the new address or key"
		}
		out.Settings, _ = shownSettings(after)

		return nil, out, nil
	})
}

// sameJSON reports whether two values are the same once written as JSON,
// which is how 30 and 30.0 are one number.
func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)

	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
