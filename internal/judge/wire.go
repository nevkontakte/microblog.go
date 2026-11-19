package judge

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The wire comparison holds one implementation's traffic to the reference's:
// for each scenario, the multiset of normalized exchanges between instances
// must equal the golden recording made with Python on both sides
// (testdata/golden). Normalization keeps what a receiving server can depend
// on (method, URL, content types, addressing, payload structure, which object
// references which, timestamp formats, the Date header's format, signature
// key, coverage and validity, and the order in which each sender delivers to
// each receiver) and masks what legitimately differs between runs (the values
// of generated ids and timestamps, keys, signature bytes).
//
// Left out of the comparison, though still recorded: the judge's own
// requests, failed CONNECTs, and JSON-LD context fetches, whose order and
// number depend on caching rather than on the protocol.

// A WireRecord is one normalized exchange.
type WireRecord struct {
	From      string `json:"from"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	Accept    string `json:"accept,omitempty"`
	Type      string `json:"contentType,omitempty"`
	Date      string `json:"date,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Signature string `json:"signature,omitempty"`
	Body      any    `json:"body,omitempty"`
	Status    int    `json:"status"`
	RespType  string `json:"respType,omitempty"`
	Response  any    `json:"response,omitempty"`
}

var (
	hexID     = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	shortID   = regexp.MustCompile(`/articles/([0-9a-f]{7})/`) // the first 7 characters of a public id
	timestamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)?`)
	digit     = regexp.MustCompile(`\d`)
)

// A masker replaces generated ids with <id1>, <id2>, … numbered by first
// appearance, so equal ids stay equal and a reference to the wrong object
// shows up as a different number.
type masker struct{ ids map[string]int }

// newMasker numbers the ids in exchanges in the order they appear. The
// judge's own admin actions run one at a time, so the ids they create keep
// their numbers from run to run; only their URLs and Locations (a new
// object's id) count, because admin pages and forms may differ in the port.
func newMasker(exchanges []Exchange) *masker {
	m := &masker{ids: map[string]int{}}
	for _, e := range exchanges {
		seen := []string{e.URL, e.RespHeader.Get("Location")}
		if e.From != "judge" {
			seen = append(seen, e.ReqBody, e.RespBody)
		}
		for _, s := range seen {
			for _, id := range hexID.FindAllString(s, -1) {
				if m.ids[id] == 0 {
					m.ids[id] = len(m.ids) + 1
				}
			}
		}
	}
	return m
}

// normalize turns recorded exchanges into wire records. keys maps a host to
// the public key of the instance serving it, for checking signatures.
func normalize(exchanges []Exchange, keys map[string]*rsa.PublicKey) []WireRecord {
	m := newMasker(exchanges)
	var out []WireRecord
	for _, e := range exchanges {
		if e.From == "judge" || e.Method == http.MethodConnect || isContext(e.URL) {
			continue
		}
		r := WireRecord{
			From:   e.From,
			Method: e.Method,
			URL:    m.str(e.URL),
			Accept: e.ReqHeader.Get("Accept"),
			Type:   e.ReqHeader.Get("Content-Type"),
			Body:   m.json(e.ReqBody),
			Status: e.Status,
		}
		if d := e.ReqHeader.Get("Date"); d != "" {
			r.Date = checkDate(d)
		}
		if d := e.ReqHeader.Get("Digest"); d != "" {
			r.Digest = checkDigest(d, e.ReqBody)
		}
		if s := e.ReqHeader.Get("Signature"); s != "" {
			r.Signature = m.str(checkSignature(e, s, keys))
		}
		r.RespType = e.RespHeader.Get("Content-Type")
		if strings.Contains(r.RespType, "json") {
			r.Response = m.json(e.RespBody)
		}
		out = append(out, r)
	}
	return out
}

func isContext(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && jsonLDContexts[u.Hostname()] != nil
}

func (m *masker) str(s string) string {
	s = hexID.ReplaceAllStringFunc(s, func(id string) string { return fmt.Sprintf("<id%d>", m.ids[id]) })
	s = shortID.ReplaceAllStringFunc(s, func(path string) string {
		short := shortID.FindStringSubmatch(path)[1]
		for id, n := range m.ids {
			if strings.HasPrefix(id, short) {
				return fmt.Sprintf("/articles/<id%d:short>/", n)
			}
		}
		return "/articles/<short-id>/"
	})
	// Keep a timestamp's shape (precision, zone) but not its value.
	return timestamp.ReplaceAllStringFunc(s, func(ts string) string { return digit.ReplaceAllString(ts, "9") })
}

// json parses a JSON body and masks its run-specific values. Bodies that
// aren't JSON are reduced to their length.
func (m *masker) json(body string) any {
	if body == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return fmt.Sprintf("<%d bytes>", len(body))
	}
	return m.value(v)
}

func (m *masker) value(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			switch k {
			case "publicKeyPem":
				v[k] = "<key>"
			case "signatureValue":
				v[k] = "<signature>"
			default:
				v[k] = m.value(x)
			}
		}
	case []any:
		for i, x := range v {
			v[i] = m.value(x)
		}
	case string:
		return m.str(v)
	}
	return v
}

// checkDate reports whether a Date header is an HTTP date (RFC 7231), which
// receivers such as Mastodon require for signed requests.
func checkDate(header string) string {
	if _, err := http.ParseTime(header); err != nil || !strings.HasSuffix(header, " GMT") {
		return "bad: " + header
	}
	return "http-date"
}

func checkDigest(header, body string) string {
	sum := sha256.Sum256([]byte(body))
	if header == "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]) {
		return "SHA-256 ok"
	}
	return "bad"
}

// checkSignature verifies a draft-cavage HTTP signature and describes it as
// "<algorithm> <signed headers> ok|bad|unverified".
func checkSignature(e Exchange, header string, keys map[string]*rsa.PublicKey) string {
	params := map[string]string{}
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		params[strings.TrimSpace(k)] = strings.Trim(v, `"`)
	}
	desc := "keyId=" + params["keyId"] + " " + params["algorithm"] + " " + params["headers"]
	keyURL, err := url.Parse(params["keyId"])
	if err != nil || keys[keyURL.Host] == nil {
		return desc + " unverified"
	}
	u, _ := url.Parse(e.URL)
	var lines []string
	for _, h := range strings.Fields(params["headers"]) {
		switch h {
		case "(request-target)":
			lines = append(lines, h+": "+strings.ToLower(e.Method)+" "+u.RequestURI())
		case "host":
			lines = append(lines, "host: "+u.Host)
		default:
			lines = append(lines, h+": "+e.ReqHeader.Get(h))
		}
	}
	sig, _ := base64.StdEncoding.DecodeString(params["signature"])
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	if rsa.VerifyPKCS1v15(keys[keyURL.Host], crypto.SHA256, sum[:], sig) != nil {
		return desc + " bad"
	}
	return desc + " ok"
}

// instanceKeys reads the public keys of the instances in a scenario
// directory, keyed by host.
func instanceKeys(dir string, names ...string) map[string]*rsa.PublicKey {
	keys := map[string]*rsa.PublicKey{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n, "data", "key.pem"))
		if err != nil {
			continue
		}
		if block, _ := pem.Decode(b); block != nil {
			if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				keys[n+".test"] = &k.PublicKey
			}
		}
	}
	return keys
}

// canonical renders records as sorted JSON lines, so that two recordings
// compare as multisets regardless of how deliveries from different senders
// interleave. One more line per sender and receiver lists the activities
// POSTed in order, since each sender delivers one at a time.
func canonical(records []WireRecord) []string {
	var lines []string
	sequences := map[string][]string{}
	for _, r := range records {
		b, _ := json.Marshal(r)
		lines = append(lines, string(b))
		if body, ok := r.Body.(map[string]any); ok && r.Method == http.MethodPost {
			u, _ := url.Parse(r.URL)
			key := r.From + " -> " + u.Host
			sequences[key] = append(sequences[key], fmt.Sprintf("%v %v", body["type"], body["id"]))
		}
	}
	for key, seq := range sequences {
		b, _ := json.Marshal(map[string]any{"sequence": key, "posts": seq})
		lines = append(lines, string(b))
	}
	sort.Strings(lines)
	return lines
}

// diffWire reports the records only in want and only in got.
func diffWire(want, got []string) (missing, extra []string) {
	count := map[string]int{}
	for _, l := range want {
		count[l]++
	}
	for _, l := range got {
		count[l]--
	}
	for l, n := range count {
		for ; n > 0; n-- {
			missing = append(missing, l)
		}
		for ; n < 0; n++ {
			extra = append(extra, l)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
