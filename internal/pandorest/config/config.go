// Package config lists the services pandorest imports and generates, the
// equivalent of Pandora's resource-manager.hcl. Paths are relative to the
// repository root, which is where the make targets run pandorest from.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Naming is how operations get their Go method names.
type Naming string

const (
	// PathNaming builds names from the HTTP method and path, for documents
	// with no operationIds (Prowlarr's has none: Swashbuckle writes none).
	PathNaming Naming = "path"
	// OperationIDNaming uses the operationId, for documents whose ids are
	// hand-written and unique.
	OperationIDNaming Naming = "operationId"
)

// Service is one server API.
type Service struct {
	// Name is the -service flag and the definitions directory name.
	Name string
	// Package is the Go package name of the generated SDK.
	Package string
	// Spec is the vendored OpenAPI document.
	Spec string
	// Definitions is where the importer writes and the generator reads.
	Definitions string
	// Output is the generated package directory.
	Output string
	// Naming picks how method names are built.
	Naming Naming
	// TagSuffix is trimmed from tag names to make group names (a document
	// whose tags are LibraryService would trim "Service").
	TagSuffix string
	// PathPrefix is trimmed from a path before a method is named after it,
	// so every Prowlarr operation is not named GetApiV1...: GET
	// /api/v1/indexer/{id} is GetIndexerById. Paths outside it are named in
	// full.
	PathPrefix string
	// Words spells the run-together words of the paths, which name methods
	// with one capital otherwise: Prowlarr's /api/v1/indexerproxy would make
	// GetIndexerproxy, and with "indexerproxy": "IndexerProxy" makes
	// GetIndexerProxy. Keys are lower case; a segment not listed is
	// capitalised as it stands.
	Words map[string]string
	// WrittenWhole names the object schemas callers read and write back
	// whole - settings objects - whose empty strings and zero numbers the
	// generated models send rather than leave out (definitions.Model's
	// WrittenWhole). A name the document does not have fails the import.
	WrittenWhole []string
	// Auth names the lib/client authorizer the generated New uses.
	Auth string

	// Root is the repository root the paths above are relative to; empty
	// is the working directory.
	Root string
}

// Services is every service, in the order the make targets process them.
// There is one: pandorest came from embyfin-mcp, which generates two SDKs,
// and keeps its shape so the copies stay easy to compare.
var Services = []Service{
	{
		Name:         "prowlarr",
		Package:      "prowlarr",
		Spec:         "docs/prowlarr-openapi.json",
		Definitions:  "api-definitions/prowlarr",
		Output:       "lib/prowlarr",
		Naming:       PathNaming,
		PathPrefix:   "/api/v1",
		Words:        prowlarrWords,
		WrittenWhole: prowlarrSettings,
		Auth:         "Prowlarr",
	},
}

// prowlarrSettings are Prowlarr's settings sections, each one object read
// with a GET and saved with a PUT of the whole of it. Prowlarr saves a
// section field by field, and a string left out arrives as null: the host
// settings' ConfigFileProvider.SaveConfigDictionary calls ToString on it and
// answers 500, and ConfigService.SaveConfigDictionary skips it, so a setting
// cleared to "" is quietly kept instead.
var prowlarrSettings = []string{
	"DevelopmentConfigResource",
	"DownloadClientConfigResource",
	"HostConfigResource",
	"UiConfigResource",
}

// prowlarrWords are the path segments of Prowlarr's API that run two or more
// words together, spelled the way its tags and models spell them.
var prowlarrWords = map[string]string{
	"appprofile":     "AppProfile",
	"customfilter":   "CustomFilter",
	"downloadclient": "DownloadClient",
	"filesystem":     "FileSystem",
	"indexerproxy":   "IndexerProxy",
	"indexerstats":   "IndexerStats",
	"indexerstatus":  "IndexerStatus",
	"testall":        "TestAll",
}

// Select returns the named services, or all of them for an empty list.
func Select(names string) ([]Service, error) {
	if names == "" {
		return Services, nil
	}
	var out []Service
	for name := range strings.SplitSeq(names, ",") {
		svc, ok := Find(strings.TrimSpace(name))
		if !ok {
			return nil, fmt.Errorf("unknown service %q (have %s)", name, strings.Join(serviceNames(), ", "))
		}
		out = append(out, svc)
	}

	return out, nil
}

// Find returns a service by name.
func Find(name string) (Service, bool) {
	for _, s := range Services {
		if s.Name == name {
			return s, true
		}
	}

	return Service{}, false
}

// In returns a copy of the service whose files are under root. The paths
// stay as configured, relative to the repository, because they are recorded
// in the definitions and the generated docs; Path resolves them.
func (s Service) In(root string) Service {
	s.Root = root

	return s
}

// Path resolves one of the service's paths under its root.
func (s Service) Path(p string) string { return filepath.Join(s.Root, p) }

func serviceNames() []string {
	out := make([]string, 0, len(Services))
	for _, s := range Services {
		out = append(out, s.Name)
	}

	return out
}
