package tools

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/prowlarr-mcp/sdk/prowlarr"
)

// settingsLookedAt is every setting that is text and is shown to a client, by
// what it holds: each was looked at and is not a credential. A few can still
// be private and are shown all the same, because what they are is the point
// of showing a provider: the path of a webhook's address, an ntfy topic,
// what a script is run with, the extra parameters of a search.
var settingsLookedAt = map[string][]string{
	"an address or a part of one, shown without the values after its ?":        {"apiPath", "apiUrl", "api-url", "baseUrl", "clickUrl", "host", "port", "prowlarrUrl", "rpcPath", "server", "serverUrl", "url", "urlBase"},
	"a folder, a file, or what one is called":                                  {"category", "definitionFile", "destination", "destinationDirectory", "directory", "magnetFileExtension", "nzbFolder", "path", "strmFolder", "torrentFolder", "tvDirectory"},
	"who a message goes to and what it looks like":                             {"author", "avatar", "bcc", "cc", "channel", "channelTags", "chatId", "deviceIds", "deviceNames", "devices", "event", "from", "icon", "mention", "notificationName", "receiverId", "recipients", "senderDomain", "senderId", "sound", "tags", "to", "topicId", "topics"},
	"a number or a date kept as text":                                          {"expire", "group_id", "requestTimeout", "retry", "vipExpiration"},
	"how a search is made or a script run":                                     {"additionalParameters", "arguments", "audio_lang", "fansub_lang", "sub_lang", "thankyou", "uploader", "userAgent"},
	"a button, which holds nothing":                                            {"authorizeNotification"},
	"the name it is known by to the other side, which is not what signs it in": {"appId"},
}

// plainTypes are the kinds of setting that cannot hold a credential: a
// switch, a choice, a number and a note to the reader.
var plainTypes = []string{"checkbox", "select", "number", "info"}

// definitionSettings are the text settings of the catalogue's definitions,
// which name their own and which Prowlarr marks as nothing: every one in the
// 581 definitions of Prowlarr's definitions repository on 2026-10-09
// (86f0afc), with whether it is a credential.
var definitionSettings = map[string]bool{
	"username": true, "password": true, "cookie": true, "apikey": true, "passkey": true, "2facode": true, "2fa_code": true, "alt2fatoken": true, "pin": true, "user": true, "pass": true, "csrf_token": true, "passan": true, "staffpass": true,
	"useragent": false, "apiurl": false, "api-url": false, "uploader": false, "thankyou": false, "audio_lang": false, "fansub_lang": false, "sub_lang": false, "group_id": false,
}

// Every setting Prowlarr declares is a credential that is hidden, a switch,
// choice or number, or text whose name is among those looked at. The list is
// what a real Prowlarr declares, kept current by the client suite, so a
// setting a later release adds fails here until somebody has looked at it.
func TestEverySettingIsACredentialOrHasBeenLookedAt(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/settings.txt")
	if err != nil {
		t.Fatal(err)
	}

	var looked []string
	for _, names := range settingsLookedAt {
		for _, name := range names {
			looked = append(looked, strings.ToLower(name))
		}
	}

	declared := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		part := strings.Fields(line)
		if len(part) != 4 {
			t.Fatalf("%q is not a family, a name, a type and a privacy", line)
		}

		f := prowlarr.Field{Name: part[1], Type: part[2], Privacy: prowlarr.PrivacyLevel(part[3])}
		declared[strings.ToLower(f.Name)] = true
		if secret(&f) || slices.Contains(plainTypes, strings.ToLower(f.Type)) || slices.Contains(looked, strings.ToLower(f.Name)) {
			continue
		}
		t.Errorf("%s is shown and nobody has looked at it: a credential goes in secretNames, anything else in settingsLookedAt", line)
	}
	if len(declared) < 100 {
		t.Errorf("only %d settings were read", len(declared))
	}

	for name, credential := range definitionSettings {
		declared[name] = true
		f := prowlarr.Field{Name: name, Type: "textbox", Privacy: prowlarr.PrivacyLevelNormal}
		if secret(&f) != credential {
			t.Errorf("a definition's %s: hidden is %v, want %v", name, secret(&f), credential)
		}
		if !credential && !slices.Contains(looked, name) {
			t.Errorf("a definition's %s is shown and is not in settingsLookedAt", name)
		}
	}

	for _, name := range looked {
		if !declared[name] {
			t.Errorf("%s is in settingsLookedAt and nothing declares it any more", name)
		}
		if f := (prowlarr.Field{Name: name, Type: "textbox"}); secret(&f) {
			t.Errorf("%s is in settingsLookedAt and is hidden as a credential", name)
		}
	}
}

// provider is a canned notification of a kind, with its settings as a real
// Prowlarr 2.6.5 declares them: none of these is marked as a credential.
func provider(id int, name, kind string, fields ...map[string]any) map[string]any {
	all := make([]any, 0, len(fields))
	for _, f := range fields {
		all = append(all, f)
	}

	return map[string]any{"id": id, "name": name, "implementation": kind, "tags": []any{}, "supportsOnGrab": true, "onGrab": true, "fields": all}
}

func setting(name, kind string, value any) map[string]any {
	return map[string]any{"name": name, "type": kind, "privacy": "normal", "value": value}
}

// Some settings are a credential in themselves and Prowlarr does not mark
// them as one: whoever holds a Discord or Slack webhook's address can post
// with it, an Apprise address carries the password of what it posts to, and
// MyAnonamouse's id is its session. A list of headers is where a webhook's
// token goes, and nothing in a list of pairs says which value is the secret.
// None is shown; each is named as set. An address that is shown is shown
// without the values after its "?", which is where a feed's passkey is.
func TestASettingThatIsACredentialInItselfIsNotShown(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.answer("GET /api/v1/notification", []any{
		provider(1, "Chat", "Discord", setting("webHookUrl", "textbox", "https://discord.invalid/api/webhooks/1/discord-hook-token"), setting("author", "textbox", "nas")),
		provider(2, "Work", "Slack", setting("webHookUrl", "url", "https://hooks.slack.invalid/services/T0/B0/slack-hook-token"), setting("channel", "textbox", "#alerts")),
		provider(3, "Many", "Apprise", setting("serverUrl", "url", "http://apprise:8000"), setting("statelessUrls", "textbox", "mailto://someone:apprise-password@mail.invalid"), setting("configurationKey", "textbox", "apprise-config-key")),
		provider(4, "Hook", "Webhook", setting("url", "url", "https://hooks.invalid/in?token=hook-query-token&room=7"), setting("headers", "keyValueList", []any{map[string]any{"key": "Authorization", "value": "Bearer hook-header-token"}})),
	})
	f.answer("GET /api/v1/indexer", []any{
		torznab(1, "Mouse", "private", setting("mamId", "textbox", "mam-session-id"), setting("twoFactorAuthCode", "textbox", "two-factor-code"), setting("apiPath", "textbox", "/api")),
		map[string]any{"id": 2, "name": "Feed", "implementation": "TorrentRssIndexer", "definitionName": "TorrentRssIndexer", "protocol": "torrent", "privacy": "private", "enable": true, "priority": 25, "appProfileId": 1, "tags": []int{}, "added": "2020-01-01T00:00:00Z", "fields": []any{setting("baseUrl", "textbox", "https://feed.invalid/rss.php?passkey=feed-pass-key&cat=5")}},
	})
	cs := session(t, f, Options{})

	answers := map[string]map[string]any{
		"notification_list": mustCall(t, cs, "notification_list", map[string]any{}),
		"indexer_list":      mustCall(t, cs, "indexer_list", map[string]any{}),
		"indexer_get Mouse": mustCall(t, cs, "indexer_get", map[string]any{"indexer": "Mouse"}),
		"indexer_get Feed":  mustCall(t, cs, "indexer_get", map[string]any{"indexer": "Feed"}),
	}
	var all strings.Builder
	for name, out := range answers {
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(raw)

		for _, secret := range []string{"discord-hook-token", "slack-hook-token", "apprise-password", "apprise-config-key", "hook-query-token", "hook-header-token", "mam-session-id", "two-factor-code", "feed-pass-key"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s shows %q: %s", name, secret, raw)
			}
		}
	}
	// what is not a credential is still shown, and so is where an address goes
	for _, shown := range []string{`"author":"nas"`, `"channel":"#alerts"`, `"serverUrl":"http://apprise:8000"`, "https://hooks.invalid/in?token=", "https://feed.invalid/rss.php?passkey=", `"apiPath":"/api"`} {
		if !strings.Contains(all.String(), shown) {
			t.Errorf("no answer shows %s", shown)
		}
	}

	set := map[string][]string{}
	for _, row := range listOf(answers["notification_list"]["notifications"]) {
		who := str(t, objectOf(row)["name"], "name")
		for _, name := range listOf(objectOf(row)["secrets_set"]) {
			set[who] = append(set[who], str(t, name, "a credential's name"))
		}
	}
	for name, want := range map[string][]string{"Chat": {"webHookUrl"}, "Work": {"webHookUrl"}, "Many": {"configurationKey", "statelessUrls"}, "Hook": {"headers"}} {
		if !slices.Equal(set[name], want) {
			t.Errorf("%s names %v as set, want %v", name, set[name], want)
		}
	}
	if got, want := listOf(answers["indexer_get Mouse"]["secrets_set"]), []any{"mamId", "twoFactorAuthCode"}; !slices.Equal(got, want) {
		t.Errorf("Mouse names %v as set, want %v", got, want)
	}
}
