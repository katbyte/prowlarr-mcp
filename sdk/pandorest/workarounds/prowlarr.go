// Package workarounds is the fixes for the bugs in the Prowlarr document this
// repository vendors, one named workaround to a bug. pandorest applies them
// to the document before it reads it, in the order All lists them.
package workarounds

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// The Prowlarr document is the one in the release's source tree,
// src/Prowlarr.Api.V1/openapi.json, which Swashbuckle writes from the
// controllers. It describes the request side well and the answers poorly:
// every operation is documented as a 200, and an action returning object or
// IActionResult is documented as answering nothing. What follows was read off
// the controllers (Prowlarr.Api.V1, Prowlarr.Http) and confirmed by the
// client suite (acceptance/prowlarr) against the same release.

const prowlarr = "prowlarr"

// about is what one of pandorest's ready-made workarounds is called here and
// the bug it fixes.
func about(name, bug string) workarounds.About {
	return workarounds.About{Name: name, Service: prowlarr, Bug: bug}
}

// All is every workaround, in the order they are applied.
var All = []workarounds.Workaround{
	// the web interface's routes: the single page app, its static files, and
	// the forms login
	workarounds.NotAPI(about("prowlarr-ui-routes", "the document includes the routes that serve the web interface (the page, its static files, the forms login), which answer HTML to a browser rather than anything an API client reads"),
		"/", "/{path}", "/content/{path}", "/login", "/logout"),

	workarounds.UndeclaredAnswers(about("prowlarr-undeclared-responses", "nine GETs declare a 200 with no content, so nothing says whether they answer JSON (and in what shape) or a file"), map[string]workarounds.Answer{
		"GET /api/v1/filesystem":              workarounds.JSON(), // {"parent", "directories", "files"}
		"GET /api/v1/filesystem/type":         workarounds.JSON(), // {"type": "folder"}
		"GET /api/v1/localization":            workarounds.JSON(), // {"Strings": {...}}
		"GET /api/v1/system/routes/duplicate": workarounds.JSON(),

		// files: the route table is graphviz text, and the Newznab and
		// Torznab feeds the applications read are RSS. The download is the
		// release's .nzb or .torrent, or a redirect to the indexer (always,
		// for usenet) or to a magnet link
		"GET /api/v1/system/routes":         workarounds.File("text/plain"),
		"GET /api/v1/indexer/{id}/newznab":  workarounds.File("application/rss+xml"),
		"GET /{id}/api":                     workarounds.File("application/rss+xml"),
		"GET /api/v1/indexer/{id}/download": workarounds.File("application/octet-stream"),
		"GET /{id}/download":                workarounds.File("application/octet-stream"),
	}),

	// ProviderControllerBase.RequestAction: Json(whatever the provider's
	// action answers), the options a settings field offers. Only answers a
	// caller has a use for are declared; the writes that answer {} are left
	workarounds.UndeclaredAnswers(about("prowlarr-undeclared-write-responses", "a provider's actions return IActionResult, documented as answering nothing, though they answer the JSON the provider's action produced"), map[string]workarounds.Answer{
		"POST /api/v1/applications/action/{name}":   workarounds.JSON(),
		"POST /api/v1/downloadclient/action/{name}": workarounds.JSON(),
		"POST /api/v1/indexer/action/{name}":        workarounds.JSON(),
		"POST /api/v1/indexerproxy/action/{name}":   workarounds.JSON(),
		"POST /api/v1/notification/action/{name}":   workarounds.JSON(),
	}),

	workarounds.WrongStatus(about("prowlarr-created-accepted", "every operation is documented as answering 200, but the creates answer 201 Created and the updates 202 Accepted, with the same body"), http.StatusOK, map[int][]string{
		// the creates: RestController.Created, CreatedAtAction
		http.StatusCreated: {
			"POST /api/v1/applications",
			"POST /api/v1/appprofile",
			"POST /api/v1/command",
			"POST /api/v1/customfilter",
			"POST /api/v1/downloadclient",
			"POST /api/v1/indexer",
			"POST /api/v1/indexerproxy",
			"POST /api/v1/notification",
			"POST /api/v1/tag",
		},
		// the updates: RestController.Accepted, AcceptedAtAction, and the
		// bulk updates, which return Accepted(value)
		http.StatusAccepted: {
			"PUT /api/v1/applications/bulk",
			"PUT /api/v1/applications/{id}",
			"PUT /api/v1/appprofile/{id}",
			"PUT /api/v1/config/development/{id}",
			"PUT /api/v1/config/downloadclient/{id}",
			"PUT /api/v1/config/host/{id}",
			"PUT /api/v1/config/ui/{id}",
			"PUT /api/v1/customfilter/{id}",
			"PUT /api/v1/downloadclient/bulk",
			"PUT /api/v1/downloadclient/{id}",
			"PUT /api/v1/indexer/bulk",
			"PUT /api/v1/indexer/{id}",
			"PUT /api/v1/indexerproxy/{id}",
			"PUT /api/v1/notification/{id}",
			"PUT /api/v1/tag/{id}",
		},
	}),

	prowlarrPutIDInteger{},
	workarounds.DeclaredSchemas(about("prowlarr-test-all-records", "the document has no record for the result of testing a provider, or for a reason one failed, though testing every provider of a kind answers a list of them"), testAllSchemas()),
	workarounds.AlsoAnswers(about("prowlarr-test-all-results", "POST .../testall is documented as answering nothing, but answers each provider's result, with 400 instead of 200 when any provider fails"), testAllAnswers()),
	prowlarrCommandBody{},
	workarounds.Commands(about("prowlarr-commands", "a command is queued by its name in a body the document gives no shape, so nothing says which commands there are, what each takes, or when one has finished"), workarounds.CommandSet{
		Post:     "POST /api/v1/command",
		Get:      "GET /api/v1/command/{id}",
		Status:   "status",
		Finished: []string{"completed", "failed", "aborted", "cancelled", "orphaned"},
		Failed:   []string{"failed", "aborted", "cancelled", "orphaned"},
		// the commands this repository names; the scheduled tasks are the server's to list, and are queued by the name it gives
		Commands: map[string]map[string]*openapi.Schema{
			"ApplicationIndexerSync": {"forceSync": {Type: openapi.TypeBoolean}},
			"Backup":                 {},
			"CheckHealth":            {},
		},
	}),
	prowlarrRestoreUploadBody{},

	// the bulk writes whose action is declared as ActionResult<TResource>
	// and answers a list: ProviderControllerBase's bulk update, Accepted(the
	// list it updated), and SearchController's bulk grab, Ok(the releases it
	// grabbed)
	workarounds.WrongAnswers(about("prowlarr-bulk-answer-lists", "the bulk updates (PUT .../bulk) and the bulk grab (POST /api/v1/search/bulk) are documented as answering one resource, but answer the list of every resource they changed or grabbed, which one resource cannot decode"), map[string]workarounds.Correction{
		"PUT /api/v1/applications/bulk":   workarounds.ListDeclaredAsOne("ApplicationResource"),
		"PUT /api/v1/downloadclient/bulk": workarounds.ListDeclaredAsOne("DownloadClientResource"),
		"PUT /api/v1/indexer/bulk":        workarounds.ListDeclaredAsOne("IndexerResource"),
		"POST /api/v1/search/bulk":        workarounds.ListDeclaredAsOne("ReleaseResource"),
	}),

	// the host names Prowlarr answers to, and the networks it trusts to say
	// who is asking
	workarounds.UndeclaredProperties(about("prowlarr-host-settings", "HostConfigResource does not list allowedHosts or trustedNetworks, which the server sends with the host settings and reads back from them: a client built from the document sends neither, and the server refuses host settings whose allowed hosts are empty unless every request has to sign in, so nothing in them could be changed"), map[string]map[string]*openapi.Schema{
		"HostConfigResource": {
			"allowedHosts":    {Type: openapi.TypeString, Nullable: true},
			"trustedNetworks": {Type: openapi.TypeString, Nullable: true},
		},
	}),
}

// prowlarrPutIDInteger types the id of every update as the integer it is.
type prowlarrPutIDInteger struct{}

func (prowlarrPutIDInteger) Name() string    { return "prowlarr-put-id-integer" }
func (prowlarrPutIDInteger) Service() string { return prowlarr }
func (prowlarrPutIDInteger) Bug() string {
	return "the updates (PUT .../{id}) declare their id as a string, because the action takes it from the body rather than as an argument; the route binds it as the integer the reads and deletes of the same path declare"
}

func (prowlarrPutIDInteger) Apply(spec *openapi.Spec) error {
	fixed := 0
	for _, path := range openapi.SortedKeys(spec.Paths) {
		item := spec.Paths[path]
		if item.Put == nil || !strings.HasSuffix(path, "/{id}") {
			continue
		}
		p := item.Put.Parameter(openapi.InPath, "id")
		if p == nil || p.Schema == nil || p.Schema.Type != openapi.TypeString {
			continue
		}
		p.Schema = &openapi.Schema{Type: openapi.TypeInteger, Format: "int32"}
		fixed++
	}
	if fixed == 0 {
		return errors.New("no update declares its id as a string")
	}

	return nil
}

// prowlarrCommandBody lets a command carry its own fields.
type prowlarrCommandBody struct{}

func (prowlarrCommandBody) Name() string    { return "prowlarr-command-body" }
func (prowlarrCommandBody) Service() string { return prowlarr }
func (prowlarrCommandBody) Bug() string {
	return "POST /api/v1/command declares CommandResource as its body, but CommandController reads the command's own fields (which indexers to sync, which log files...) from the top level of the body, where CommandResource has no place for them"
}

func (prowlarrCommandBody) Apply(spec *openapi.Spec) error {
	op, err := workarounds.Operation(spec, http.MethodPost, "/api/v1/command")
	if err != nil {
		return err
	}
	if op.RequestBody == nil || len(op.RequestBody.Content) == 0 {
		return errors.New("POST /api/v1/command takes no body")
	}
	for ct, media := range op.RequestBody.Content {
		if media.Schema == nil || media.Schema.RefName() != "CommandResource" {
			return fmt.Errorf("POST /api/v1/command's %s body is no longer CommandResource", ct)
		}
		media.Schema = &openapi.Schema{Type: openapi.TypeObject, Description: "the command: its name and its own fields, at the top level"}
	}

	return nil
}

// The record testing every provider of a kind answers, and one reason in it, neither of which the document has. Read off ProviderControllerBase.TestAll.
const (
	testAllResult     = "ProviderTestAllResult"
	validationFailure = "ValidationFailure"
)

// testAllSchemas is the two records as the server sends them.
func testAllSchemas() map[string]*openapi.Schema {
	str := func() *openapi.Schema { return &openapi.Schema{Type: openapi.TypeString, Nullable: true} }

	return map[string]*openapi.Schema{
		validationFailure: {
			Type:        openapi.TypeObject,
			Description: "One reason a provider failed its test: the setting at fault and what is wrong with it.",
			Properties: map[string]*openapi.Schema{
				"propertyName":        str(),
				"errorMessage":        str(),
				"severity":            {Type: openapi.TypeString, Description: "error, warning or info"},
				"errorCode":           str(),
				"infoLink":            str(),
				"detailedDescription": str(),
				"isWarning":           {Type: openapi.TypeBoolean},
			},
		},
		testAllResult: {
			Type:        openapi.TypeObject,
			Description: "The outcome of testing one provider.",
			Properties: map[string]*openapi.Schema{
				"id":                 {Type: openapi.TypeInteger, Format: "int32"},
				"isValid":            {Type: openapi.TypeBoolean},
				"validationFailures": {Type: openapi.TypeArray, Items: &openapi.Schema{Ref: openapi.SchemaRefPrefix + validationFailure}},
			},
		},
	}
}

// testAllAnswers is what testing every provider of a kind answers, for each kind ProviderControllerBase serves: every provider's result, as a 200 when all
// pass and a 400 when any fails.
func testAllAnswers() map[string]map[int]workarounds.Answer {
	out := map[string]map[int]workarounds.Answer{}
	for _, kind := range []string{"applications", "downloadclient", "indexer", "indexerproxy", "notification"} {
		out["POST /api/v1/"+kind+"/testall"] = map[int]workarounds.Answer{http.StatusOK: workarounds.ListOf(testAllResult), http.StatusBadRequest: workarounds.ListOf(testAllResult)}
	}

	return out
}

// prowlarrRestoreUploadBody declares the file a restore from an upload takes.
type prowlarrRestoreUploadBody struct{}

const restoreUpload = "/api/v1/system/backup/restore/upload"

func (prowlarrRestoreUploadBody) Name() string    { return "prowlarr-restore-upload-body" }
func (prowlarrRestoreUploadBody) Service() string { return prowlarr }
func (prowlarrRestoreUploadBody) Bug() string {
	return "POST /api/v1/system/backup/restore/upload declares no request body, but BackupController reads the backup from a multipart form (Request.Form.Files), and answers a 500 to a request with no Content-Type: a client built from the document cannot send the file"
}

func (prowlarrRestoreUploadBody) Apply(spec *openapi.Spec) error {
	op, err := workarounds.Operation(spec, http.MethodPost, restoreUpload)
	if err != nil {
		return err
	}
	if op.RequestBody != nil {
		return fmt.Errorf("POST %s declares a body now", restoreUpload)
	}
	// a multipart form with the backup (a .zip) as a file field, sent with
	// the form's own Content-Type and its boundary
	op.RequestBody = &openapi.RequestBody{
		Required: true,
		Content:  map[string]*openapi.MediaType{"multipart/form-data": {Schema: &openapi.Schema{Type: openapi.TypeString, Format: "binary"}}},
	}

	return nil
}
