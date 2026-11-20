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
	"slices"
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

// newMasker numbers the ids in exchanges in an order that doesn't depend on
// timing. First come the judge's own requests, which run one at a time: the
// URLs and Locations (a new object's id) of its admin actions, but not their
// bodies, because admin pages and forms may differ in the port. Then the
// instances' and fakes' exchanges, sorted by their content with ids and
// timestamps masked alike: they run concurrently with the judge and each
// other, and an id an instance makes up (a post's conversation, say) first
// shows in whichever exchange happens to finish first.
func newMasker(exchanges []Exchange) *masker {
	m := &masker{ids: map[string]int{}}
	number := func(texts ...string) {
		for _, s := range texts {
			for _, id := range hexID.FindAllString(s, -1) {
				if m.ids[id] == 0 {
					m.ids[id] = len(m.ids) + 1
				}
			}
		}
	}
	var others []Exchange
	for _, e := range exchanges {
		if e.From == "judge" {
			number(e.URL, e.RespHeader.Get("Location"))
		} else {
			others = append(others, e)
		}
	}
	slices.SortStableFunc(others, func(a, b Exchange) int { return strings.Compare(orderKey(a), orderKey(b)) })
	for _, e := range others {
		number(e.URL, e.RespHeader.Get("Location"), canonicalJSON(e.ReqBody), canonicalJSON(e.RespBody))
	}
	return m
}

// canonicalJSON re-encodes a JSON text with its object members sorted, so
// that how ids are numbered doesn't depend on the order in which a
// serializer writes members; the port's may differ from the original's.
// Text that isn't JSON is returned as it is.
func canonicalJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, _ := json.Marshal(v) // maps marshal with sorted keys
	return string(b)
}

// orderKey is an exchange's content, with JSON in canonical form and
// everything that varies between runs masked alike: ids, timestamps, keys and
// signatures.
func orderKey(e Exchange) string {
	s := strings.Join([]string{e.From, e.Method, e.URL, canonicalJSON(e.ReqBody), fmt.Sprint(e.Status), canonicalJSON(e.RespBody)}, "\n")
	s = volatileField.ReplaceAllString(s, `"$1":""`)
	s = hexID.ReplaceAllString(s, "<id>")
	return timestamp.ReplaceAllStringFunc(s, func(ts string) string { return digit.ReplaceAllString(ts, "9") })
}

var volatileField = regexp.MustCompile(`"(publicKeyPem|signatureValue|nonce)":\s*"[^"]*"`)

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
			case "signatureValue": // LD signatures: verified in part 3, with the forwarded activities that carry them
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
		default: // including host, as the proxy recorded it
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
// POSTed in order, since each sender delivers one at a time. instances names
// the senders that are instances, whose retries count once.
func canonical(records []WireRecord, instances ...string) []string {
	var lines []string
	sequences := map[string][]string{}
	delivered := map[string]bool{}
	for _, r := range records {
		b, _ := json.Marshal(r)
		if r.Method == http.MethodPost && slices.Contains(instances, r.From) {
			// A delivery an instance sends again, identical, counts once:
			// the original gives up on a slow receiver after httpx's default
			// 5 s (app/activitypub.py:397) and retries, so on a loaded
			// machine the receiver's late answer leaves two recorded
			// attempts. Fakes never retry; a duplicate from one is meant.
			if delivered[string(b)] {
				continue
			}
			delivered[string(b)] = true
		}
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
