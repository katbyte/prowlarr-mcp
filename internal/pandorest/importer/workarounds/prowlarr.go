package workarounds

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/katbyte/prowlarr-mcp/internal/pandorest/openapi"
)

// The Prowlarr document is the one in the release's source tree,
// src/Prowlarr.Api.V1/openapi.json, which Swashbuckle writes from the
// controllers. It describes the request side well and the answers poorly:
// every operation is documented as a 200, and an action returning object or
// IActionResult is documented as answering nothing. What follows was read off
// the controllers (Prowlarr.Api.V1, Prowlarr.Http) and confirmed by the
// integration suite against the same release.

const prowlarr = "prowlarr"

// prowlarrUIRoutes removes the routes that serve the web interface.
type prowlarrUIRoutes struct{}

// prowlarrUIPaths are the web interface's routes: the single page app, its
// static files, and the forms login.
var prowlarrUIPaths = []string{"/", "/{path}", "/content/{path}", "/login", "/logout"}

func (prowlarrUIRoutes) Name() string    { return "prowlarr-ui-routes" }
func (prowlarrUIRoutes) Service() string { return prowlarr }
func (prowlarrUIRoutes) Bug() string {
	return "the document includes the routes that serve the web interface (the page, its static files, the forms login), which answer HTML to a browser rather than anything an API client reads"
}

func (prowlarrUIRoutes) Apply(spec *openapi.Spec) error {
	for _, path := range prowlarrUIPaths {
		if spec.Paths[path] == nil {
			return fmt.Errorf("%s is not in the document", path)
		}
		delete(spec.Paths, path)
	}

	return nil
}

// prowlarrUndeclaredResponses declares what the GETs without a response
// schema answer. A value with a slash is a file of that type; answersJSON is
// JSON of no declared schema; anything else is the component schema the JSON
// decodes into, and "[]" before it makes a list of them.
type prowlarrUndeclaredResponses struct{}

const (
	answersJSON = "json"
	listOf      = "[]"
)

var prowlarrUndeclaredGets = map[string]string{
	// JSON
	"/api/v1/filesystem":              answersJSON, // {"parent", "directories", "files"}
	"/api/v1/filesystem/type":         answersJSON, // {"type": "folder"}
	"/api/v1/localization":            answersJSON, // {"Strings": {...}}
	"/api/v1/system/routes/duplicate": answersJSON,

	// files: the route table is graphviz text, and the Newznab and Torznab
	// feeds the applications read are RSS. The download is the release's
	// .nzb or .torrent, or a redirect to the indexer (always, for usenet) or
	// to a magnet link
	"/api/v1/system/routes":         "text/plain",
	"/api/v1/indexer/{id}/newznab":  "application/rss+xml",
	"/{id}/api":                     "application/rss+xml",
	"/api/v1/indexer/{id}/download": "application/octet-stream",
	"/{id}/download":                "application/octet-stream",
}

func (prowlarrUndeclaredResponses) Name() string    { return "prowlarr-undeclared-responses" }
func (prowlarrUndeclaredResponses) Service() string { return prowlarr }
func (prowlarrUndeclaredResponses) Bug() string {
	return "nine GETs declare a 200 with no content, so nothing says whether they answer JSON (and in what shape) or a file"
}

func (prowlarrUndeclaredResponses) Apply(spec *openapi.Spec) error {
	return declareAnswers(spec, http.MethodGet, prowlarrUndeclaredGets)
}

// prowlarrUndeclaredWriteResponses declares the JSON the writes answer when
// their action returns object or IActionResult, which Swashbuckle documents as
// nothing. Only answers a caller has a use for are declared: what a provider
// action looked up. The ones that answer {} are left as they are.
type prowlarrUndeclaredWriteResponses struct{}

var prowlarrUndeclaredWrites = map[string]string{
	// ProviderControllerBase.RequestAction: Json(whatever the provider's
	// action answers), the options a settings field offers
	"POST /api/v1/applications/action/{name}":   answersJSON,
	"POST /api/v1/downloadclient/action/{name}": answersJSON,
	"POST /api/v1/indexer/action/{name}":        answersJSON,
	"POST /api/v1/indexerproxy/action/{name}":   answersJSON,
	"POST /api/v1/notification/action/{name}":   answersJSON,
}

func (prowlarrUndeclaredWriteResponses) Name() string    { return "prowlarr-undeclared-write-responses" }
func (prowlarrUndeclaredWriteResponses) Service() string { return prowlarr }
func (prowlarrUndeclaredWriteResponses) Bug() string {
	return "a provider's actions return IActionResult, documented as answering nothing, though they answer the JSON the provider's action produced"
}

func (prowlarrUndeclaredWriteResponses) Apply(spec *openapi.Spec) error {
	byMethod := map[string]map[string]string{}
	for target, answer := range prowlarrUndeclaredWrites {
		method, path, _ := strings.Cut(target, " ")
		if byMethod[method] == nil {
			byMethod[method] = map[string]string{}
		}
		byMethod[method][path] = answer
	}
	for _, method := range openapi.SortedKeys(byMethod) {
		if err := declareAnswers(spec, method, byMethod[method]); err != nil {
			return err
		}
	}

	return nil
}

// declareAnswers gives each path's operation the answer the table names on
// its 200, failing for any that already declares one.
func declareAnswers(spec *openapi.Spec, method string, answers map[string]string) error {
	var declared []string
	for _, path := range openapi.SortedKeys(answers) {
		op, err := operation(spec, method, path)
		if err != nil {
			return err
		}
		ok := op.Responses["200"]
		if ok == nil {
			return fmt.Errorf("%s %s has no 200 response", method, path)
		}
		if len(ok.Content) > 0 {
			declared = append(declared, method+" "+path)
			continue
		}
		content, err := answerContent(spec, answers[path])
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		ok.Content = content
	}
	if len(declared) > 0 {
		return fmt.Errorf("these now declare their response, so take them out of the table: %s", strings.Join(declared, ", "))
	}

	return nil
}

// answerContent is the content map for one table entry.
func answerContent(spec *openapi.Spec, answer string) (map[string]*openapi.MediaType, error) {
	switch {
	case answer == answersJSON:
		return map[string]*openapi.MediaType{"application/json": {}}, nil
	case strings.Contains(answer, "/"):
		return map[string]*openapi.MediaType{answer: {Schema: &openapi.Schema{Type: openapi.TypeString, Format: "binary"}}}, nil
	}
	name, list := strings.CutPrefix(answer, listOf)
	if spec.Components.Schemas[name] == nil {
		return nil, fmt.Errorf("schema %s is not in the document", name)
	}
	schema := &openapi.Schema{Ref: openapi.SchemaRefPrefix + name}
	if list {
		schema = &openapi.Schema{Type: openapi.TypeArray, Items: schema}
	}

	return map[string]*openapi.MediaType{"application/json": {Schema: schema}}, nil
}

// prowlarrCreatedAccepted corrects the success status of the operations that
// answer 201 Created or 202 Accepted rather than the 200 documented.
type prowlarrCreatedAccepted struct{}

// prowlarrCreated are the creates: RestController.Created, CreatedAtAction.
var prowlarrCreated = []string{
	"POST /api/v1/applications",
	"POST /api/v1/appprofile",
	"POST /api/v1/command",
	"POST /api/v1/customfilter",
	"POST /api/v1/downloadclient",
	"POST /api/v1/indexer",
	"POST /api/v1/indexerproxy",
	"POST /api/v1/notification",
	"POST /api/v1/tag",
}

// prowlarrAccepted are the updates: RestController.Accepted, AcceptedAtAction,
// and the bulk updates, which return Accepted(value).
var prowlarrAccepted = []string{
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
}

func (prowlarrCreatedAccepted) Name() string    { return "prowlarr-created-accepted" }
func (prowlarrCreatedAccepted) Service() string { return prowlarr }
func (prowlarrCreatedAccepted) Bug() string {
	return "every operation is documented as answering 200, but the creates answer 201 Created and the updates 202 Accepted, with the same body"
}

func (prowlarrCreatedAccepted) Apply(spec *openapi.Spec) error {
	for _, set := range []struct {
		status  string
		targets []string
	}{{"201", prowlarrCreated}, {"202", prowlarrAccepted}} {
		for _, target := range set.targets {
			method, path, _ := strings.Cut(target, " ")
			op, err := operation(spec, method, path)
			if err != nil {
				return err
			}
			ok := op.Responses["200"]
			if ok == nil || op.Responses[set.status] != nil {
				return fmt.Errorf("%s no longer documents only a 200", target)
			}
			delete(op.Responses, "200")
			op.Responses[set.status] = ok
		}
	}

	return nil
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
	op, err := operation(spec, http.MethodPost, "/api/v1/command")
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

// prowlarrTestAllResults declares what testing every provider of a kind
// answers: the result for each, as a 200 when all pass and a 400 when any
// fails.
type prowlarrTestAllResults struct{}

// prowlarrTestAll are the providers ProviderControllerBase serves.
var prowlarrTestAll = []string{
	"/api/v1/applications/testall",
	"/api/v1/downloadclient/testall",
	"/api/v1/indexer/testall",
	"/api/v1/indexerproxy/testall",
	"/api/v1/notification/testall",
}

const (
	testAllResult     = "ProviderTestAllResult"
	validationFailure = "ValidationFailure"
)

func (prowlarrTestAllResults) Name() string    { return "prowlarr-test-all-results" }
func (prowlarrTestAllResults) Service() string { return prowlarr }
func (prowlarrTestAllResults) Bug() string {
	return "POST .../testall is documented as answering nothing, but answers each provider's result (ProviderTestAllResult, not in the document), with 400 instead of 200 when any provider fails"
}

func (prowlarrTestAllResults) Apply(spec *openapi.Spec) error {
	for _, name := range []string{testAllResult, validationFailure} {
		if spec.Components.Schemas[name] != nil {
			return fmt.Errorf("the document declares %s", name)
		}
	}
	str := func() *openapi.Schema { return &openapi.Schema{Type: openapi.TypeString, Nullable: true} }
	spec.Components.Schemas[validationFailure] = &openapi.Schema{
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
	}
	spec.Components.Schemas[testAllResult] = &openapi.Schema{
		Type:        openapi.TypeObject,
		Description: "The outcome of testing one provider.",
		Properties: map[string]*openapi.Schema{
			"id":                 {Type: openapi.TypeInteger, Format: "int32"},
			"isValid":            {Type: openapi.TypeBoolean},
			"validationFailures": {Type: openapi.TypeArray, Items: &openapi.Schema{Ref: openapi.SchemaRefPrefix + validationFailure}},
		},
	}
	results := func() map[string]*openapi.MediaType {
		return map[string]*openapi.MediaType{"application/json": {Schema: &openapi.Schema{
			Type: openapi.TypeArray, Items: &openapi.Schema{Ref: openapi.SchemaRefPrefix + testAllResult},
		}}}
	}
	for _, path := range prowlarrTestAll {
		op, err := operation(spec, http.MethodPost, path)
		if err != nil {
			return err
		}
		ok := op.Responses["200"]
		switch {
		case ok == nil:
			return fmt.Errorf("POST %s has no 200 response", path)
		case len(ok.Content) > 0 || op.Responses["400"] != nil:
			return fmt.Errorf("POST %s declares its answer", path)
		}
		ok.Content = results()
		op.Responses["400"] = &openapi.Response{Description: "At least one provider failed its test", Content: results()}
	}

	return nil
}

// prowlarrBulkAnswerLists declares what the bulk writes answer: every
// resource they changed, not the one resource documented.
type prowlarrBulkAnswerLists struct{}

// prowlarrBulkAnswers are the bulk writes whose action is declared as
// ActionResult<TResource> and answers a list: ProviderControllerBase's
// bulk update, Accepted(the list it updated), and SearchController's bulk
// grab, Ok(the releases it grabbed).
var prowlarrBulkAnswers = []string{
	"PUT /api/v1/applications/bulk",
	"PUT /api/v1/downloadclient/bulk",
	"PUT /api/v1/indexer/bulk",
	"POST /api/v1/search/bulk",
}

func (prowlarrBulkAnswerLists) Name() string    { return "prowlarr-bulk-answer-lists" }
func (prowlarrBulkAnswerLists) Service() string { return prowlarr }
func (prowlarrBulkAnswerLists) Bug() string {
	return "the bulk updates (PUT .../bulk) and the bulk grab (POST /api/v1/search/bulk) are documented as answering one resource, but answer the list of every resource they changed or grabbed, which one resource cannot decode"
}

func (prowlarrBulkAnswerLists) Apply(spec *openapi.Spec) error {
	for _, target := range prowlarrBulkAnswers {
		method, path, _ := strings.Cut(target, " ")
		op, err := operation(spec, method, path)
		if err != nil {
			return err
		}
		// the 202 prowlarr-created-accepted moves an update's answer to, or
		// the 200 the document has before it runs, and the grab's 200
		answer := op.Responses["202"]
		if answer == nil {
			answer = op.Responses["200"]
		}
		if answer == nil {
			return fmt.Errorf("%s has no success response", target)
		}
		for ct, media := range answer.Content {
			if media.Schema == nil || media.Schema.RefName() == "" {
				return fmt.Errorf("%s's %s answer is no longer one resource", target, ct)
			}
			media.Schema = &openapi.Schema{Type: openapi.TypeArray, Items: media.Schema}
		}
	}

	return nil
}
