package prowlarr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/pandorest/client"
)

// service is what is Prowlarr's own about being talked to, which the base
// client every generated method sends through is made from (see New): its
// name, the address it is found at, what a redirect or a web page in the
// API's place means on it, and how it says what went wrong. This file is
// written by hand and lives beside the generated ones; everything else in the
// package is pandorest's.
var service = client.Service{
	Name:           "Prowlarr",
	UserAgent:      "prowlarr-mcp/" + version.Version,
	Example:        "nas:9696",
	RedirectAdvice: "set the server url to the address Prowlarr itself answers on, with its URL base if it has one",
	WebPageAdvice:  "check the server url and its URL base",
	Messages:       errorMessages,
	Note:           note,
}

// note is what is known of a failure on a Prowlarr.
func note(f client.Failure) string {
	switch {
	case f.StatusCode == http.StatusUnauthorized:
		return "API key rejected; check the key under Settings → General → Security"
	case f.StatusCode == http.StatusNotFound && !looksLikeJSON(f.Body):
		return "this server has no such API call: the server url may lack Prowlarr's URL base, or may not be a Prowlarr"
	default:
		return ""
	}
}

// looksLikeJSON reports whether a body starts as a JSON object or list does,
// which is how Prowlarr's own refusals are told from a path it does not have.
func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)

	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// validationFailure is one entry of the list Prowlarr answers a failed
// validation with (FluentValidation's ValidationFailure).
type validationFailure struct {
	PropertyName string `json:"propertyName"`
	ErrorMessage string `json:"errorMessage"`
	// Severity is "error" or "warning"; a warning alone does not fail a
	// request, so one in an error answer rode along with a real failure.
	Severity string `json:"severity"`
}

// errorModel is the body of every other failure: a 404 for an id that does
// not exist, a 409 for a conflict, a 500 with the exception's message.
type errorModel struct {
	Message string `json:"message"`
}

// errorMessages reads what Prowlarr said went wrong out of an error body, or
// nothing when the body is not one of its shapes.
func errorMessages(body []byte) []string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}

	var failures []validationFailure
	if body[0] == '[' && json.Unmarshal(body, &failures) == nil {
		var out []string
		for _, f := range failures {
			if f.ErrorMessage == "" {
				continue
			}
			m := f.ErrorMessage
			if f.PropertyName != "" {
				m = f.PropertyName + ": " + m
			}
			if strings.EqualFold(f.Severity, "warning") {
				m += " (warning)"
			}
			out = append(out, m)
		}

		return out
	}

	var em errorModel
	if body[0] == '{' && json.Unmarshal(body, &em) == nil && em.Message != "" {
		return []string{em.Message}
	}

	return nil
}
