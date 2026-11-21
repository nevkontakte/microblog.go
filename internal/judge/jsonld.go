package judge

import (
	"embed"
	"net/http"
)

// LD signatures (app/ldsig.py) canonicalize a payload with the JSON-LD
// contexts it names, which a JSON-LD processor fetches from the web. The
// proxy serves captured copies instead, so a run needs no network and every
// signature is computed from the same bytes.

//go:embed testdata/jsonld/*.jsonld
var jsonld embed.FS

// jsonLDContexts maps host and path to a captured context document
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
