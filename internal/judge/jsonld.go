package judge

import (
	"embed"
	"net/http"
)

// An LD signature (app/ldsig.py) normalizes a payload with the JSON-LD
// contexts that the payload names. A JSON-LD processor fetches these
// contexts from the web. The proxy serves captured copies in place of the
// web. Thus a run needs no network, and all signatures use the same bytes.

//go:embed testdata/jsonld/*.jsonld
var jsonld embed.FS

// jsonLDContexts maps a host and a path to a captured context document
// (testdata/jsonld/README.md).
var jsonLDContexts = map[string]map[string]string{
	"www.w3.org": {"/ns/activitystreams": "activitystreams.jsonld"},
	"w3id.org":   {"/security/v1": "security-v1.jsonld", "/identity/v1": "identity-v1.jsonld"},
	"raw.githubusercontent.com": {
		"/web-payments/web-payments.org/master/contexts/identity-v1.jsonld": "identity-v1.jsonld",
	},
	"purl.archive.org": {"/socialweb/webfinger": "webfinger.jsonld"},
}

// contextServer serves the captured documents of one host.
func contextServer(paths map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/ld+json")
		if _, err := w.Write(must(jsonld.ReadFile("testdata/jsonld/" + name))); err != nil {
			panic(err) // fails the run, see Proxy.exchange
		}
	})
}
