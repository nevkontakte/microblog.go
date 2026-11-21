package judge

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// The wire comparison compares one implementation's traffic with the
// original's. For each scenario, the multiset of normalized exchanges between
// instances must equal the golden recording, made with Python on both sides
// (testdata/golden).
//
// Normalization keeps what a receiving server can depend on:
//   - the method, URL, content types, addressing and payload structure,
//   - which object refers to which,
//   - the format of timestamps and of the Date header,
//   - the key, coverage and validity of signatures,
//   - the order in which each sender delivers to each receiver.
//
// It masks what can differ between correct runs: the values of generated ids
// and timestamps, keys, and signature bytes.
//
// The comparison leaves out these exchanges, which the proxy still records:
// the judge's own requests, failed CONNECTs, and JSON-LD context fetches. The
// order and number of context fetches depend on caching, not on the
// protocol.

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
	// LDSignature is the verdict on the body's LD signature, if the body has
	// one. Body masks the signature's value.
	LDSignature string `json:"ldSignature,omitempty"`
	Body        any    `json:"body,omitempty"`
	Status      int    `json:"status"`
	RespType    string `json:"respType,omitempty"`
	Response    any    `json:"response,omitempty"`
}

var (
	hexID     = regexp.MustCompile(`(\b|%2F)([0-9a-f]{32})\b`) // also in an encoded URL in a query
	shortID   = regexp.MustCompile(`/articles/([0-9a-f]{7})/`) // the first 7 characters of a public id
	timestamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)?`)
	digit     = regexp.MustCompile(`\d`)
	// rfc822 is the date format of RSS feeds.
	rfc822 = regexp.MustCompile(`(Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d\d (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{4} \d\d:\d\d:\d\d`)
	// A media proxy URL expires a week after the day that the instance made
	// it, and its signature covers the expiry (app/media.py:16-41). The URL
	// that the media proxy fetches may hold an instance's address, which
	// changes from run to run.
	proxyURL = regexp.MustCompile(`/proxy/media/\d+/[A-Za-z0-9_=-]+/[A-Za-z0-9_=-]+`)
	// A multipart form's boundary is random.
	multipartBoundary = regexp.MustCompile(`boundary=[^;]+`)
	// A pagination cursor encodes a timestamp (app/utils/pagination.py).
	cursor = regexp.MustCompile(`next_cursor=[A-Za-z0-9_=%-]+`)
)

// A masker replaces generated ids with <id1>, <id2>, and so on, in the order
// in which each id first appears. Equal ids thus stay equal, and a reference
// to the wrong object shows as a different number.
type masker struct{ ids map[string]int }

// newMasker numbers the ids in exchanges in an order that doesn't depend on
// timing:
//  1. The judge's own requests, which run one at a time. newMasker uses the
//     URLs and Locations (a new object's id) of its admin actions, but not
//     their bodies, because admin pages and forms may differ in the port.
//  2. The instances' and fakes' exchanges, sorted by their content, with ids
//     and timestamps masked alike. They run at the same time as the judge and
//     as each other. An id that an instance makes (for example a post's
//     conversation) first shows in the exchange that finishes first.
func newMasker(exchanges []Exchange) *masker {
	m := &masker{ids: map[string]int{}}
	number := func(texts ...string) {
		for _, s := range texts {
			for _, match := range hexID.FindAllStringSubmatch(s, -1) {
				if id := match[2]; m.ids[id] == 0 {
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

// canonicalJSON encodes a JSON text again, with its object members sorted.
// The numbers of ids then don't depend on the order in which a serializer
// writes members, which the port may change. canonicalJSON returns text that
// isn't JSON without a change.
func canonicalJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	return string(must(json.Marshal(v))) // maps marshal with sorted keys
}

// orderKey is an exchange's content, with JSON in canonical form. It masks
// alike all values that change between runs: ids, timestamps, keys and
// signatures.
func orderKey(e Exchange) string {
	s := strings.Join([]string{e.From, e.Method, e.URL, canonicalJSON(e.ReqBody), fmt.Sprint(e.Status), canonicalJSON(e.RespBody)}, "\n")
	s = volatileField.ReplaceAllString(s, `"$1":""`)
	s = hexID.ReplaceAllString(s, "${1}<id>")
	return maskTimes(s)
}

// maskTimes keeps the shape of timestamps (precision, zone), and of values
// that come from the time, but not their values.
func maskTimes(s string) string {
	s = timestamp.ReplaceAllStringFunc(s, func(ts string) string { return digit.ReplaceAllString(ts, "9") })
	s = rfc822.ReplaceAllString(s, "Day, 99 Mon 9999 99:99:99")
	s = proxyURL.ReplaceAllString(s, "/proxy/media/<expiry>/<signature>/<url>")
	return cursor.ReplaceAllString(s, "next_cursor=<cursor>")
}

var volatileField = regexp.MustCompile(`"(publicKeyPem|signatureValue|nonce|access_token|refresh_token|client_secret)":\s*"[^"]*"`)

// normalize changes recorded exchanges into wire records. keys maps a host to
// the public key of the instance that serves it, to verify signatures.
func normalize(exchanges []Exchange, keys map[string]*rsa.PublicKey) []WireRecord {
	exchanges = slices.Clone(exchanges)
	for i, e := range exchanges {
		if e.From == "visitor" {
			exchanges[i].RespBody = asJSON(e.RespHeader.Get("Content-Type"), e.RespBody)
		}
	}
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
			Type:   multipartBoundary.ReplaceAllString(e.ReqHeader.Get("Content-Type"), "boundary=<boundary>"),
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
		r.LDSignature = checkLDSignature(e.ReqBody, keys)
		r.RespType = e.RespHeader.Get("Content-Type")
		if strings.Contains(r.RespType, "json") || e.From == "visitor" {
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
	s = hexID.ReplaceAllStringFunc(s, func(match string) string {
		sub := hexID.FindStringSubmatch(match)
		return fmt.Sprintf("%s<id%d>", sub[1], m.ids[sub[2]])
	})
	s = shortID.ReplaceAllStringFunc(s, func(path string) string {
		short := shortID.FindStringSubmatch(path)[1]
		for id, n := range m.ids {
			if strings.HasPrefix(id, short) {
				return fmt.Sprintf("/articles/<id%d:short>/", n)
			}
		}
		return "/articles/<short-id>/"
	})
	return maskTimes(s)
}

// json parses a JSON body and masks its run-specific values. It replaces a
// body that isn't JSON with its length.
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
			case "access_token", "refresh_token", "client_secret":
				v[k] = "<secret>"
			case "client_id_issued_at", "exp", "iat":
				v[k] = "<time>"
			case "signatureValue": // the LD signature's value. LDSignature gives the verdict
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

// checkSignature verifies a draft-cavage HTTP signature. It describes the
// signature as "<algorithm> <signed headers> ok|bad|unverified".
func checkSignature(e Exchange, header string, keys map[string]*rsa.PublicKey) string {
	params := map[string]string{}
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return "malformed: " + header
		}
		params[strings.TrimSpace(k)] = strings.Trim(v, `"`)
	}
	desc := "keyId=" + params["keyId"] + " " + params["algorithm"] + " " + params["headers"]
	keyURL, err := url.Parse(params["keyId"])
	if err != nil || keys[keyURL.Host] == nil {
		return desc + " unverified"
	}
	u := must(url.Parse(e.URL)) // the proxy recorded the URL from a parsed request
	var lines []string
	for _, h := range strings.Fields(params["headers"]) {
		switch h {
		case "(request-target)":
			lines = append(lines, h+": "+strings.ToLower(e.Method)+" "+u.RequestURI())
		default: // host too, as the proxy recorded it
			lines = append(lines, h+": "+e.ReqHeader.Get(h))
		}
	}
	sig, err := base64.StdEncoding.DecodeString(params["signature"])
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	if err != nil || rsa.VerifyPKCS1v15(keys[keyURL.Host], crypto.SHA256, sum[:], sig) != nil {
		return desc + " bad"
	}
	return desc + " ok"
}

// checkLDSignature verifies the LD signature of a JSON body, if the body has
// one, with the key of the creator's host. It returns "<type> <creator>
// ok|bad|unverified".
func checkLDSignature(body string, keys map[string]*rsa.PublicKey) string {
	var doc map[string]any
	if json.Unmarshal([]byte(body), &doc) != nil {
		return ""
	}
	sig, ok := doc["signature"].(map[string]any)
	if !ok {
		return ""
	}
	creator, _ := sig["creator"].(string)
	desc := fmt.Sprint(sig["type"], " ", creator)
	u, err := url.Parse(creator)
	if err != nil || keys[u.Host] == nil {
		return desc + " unverified"
	}
	if err := ldVerify(doc, keys[u.Host]); err != nil {
		return desc + " bad"
	}
	return desc + " ok"
}

// instanceKeys reads the public keys of the instances in a scenario
// directory, by host.
func instanceKeys(dir string, names ...string) (map[string]*rsa.PublicKey, error) {
	keys := map[string]*rsa.PublicKey{}
	for _, n := range names {
		path := filepath.Join(dir, n, "data", "key.pem")
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("%s: not PEM", path)
		}
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		keys[n+".test"] = &k.PublicKey
	}
	return keys, nil
}

// canonical writes records as sorted JSON lines. Two recordings thus compare
// as multisets, whatever the order of deliveries from different senders.
// One more line for each sender and receiver lists the activities that the
// sender POSTed, in order, because each sender delivers one at a time.
// instances names the senders that are instances. Their retries count once.
func canonical(records []WireRecord, instances ...string) []string {
	var lines []string
	sequences := map[string][]string{}
	delivered := map[string]bool{}
	for _, r := range records {
		b := must(json.Marshal(r))
		if r.Method == http.MethodPost && slices.Contains(instances, r.From) {
			// An identical delivery that an instance sends again counts
			// once. The original stops its wait for a slow receiver after
			// httpx's default 5 s (app/activitypub.py:397), and tries
			// again. On a loaded machine, the receiver's late answer thus
			// leaves two recorded tries. Fakes never try again, so a
			// duplicate from a fake is on purpose.
			if delivered[string(b)] {
				continue
			}
			delivered[string(b)] = true
		}
		lines = append(lines, string(b))
		if body, ok := r.Body.(map[string]any); ok && r.Method == http.MethodPost {
			u := must(url.Parse(r.URL))
			key := r.From + " -> " + u.Host
			sequences[key] = append(sequences[key], fmt.Sprintf("%v %v", body["type"], body["id"]))
		}
	}
	for key, seq := range sequences {
		lines = append(lines, string(must(json.Marshal(map[string]any{"sequence": key, "posts": seq}))))
	}
	sort.Strings(lines)
	return lines
}

// diffWire returns the records that are only in want, and the records that
// are only in got.
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

// asJSON changes a response to a visitor (a browser or feed reader) into JSON
// text. It keeps what must not change:
//   - JSON stays as it is.
//   - An HTML page becomes its microformats (templates may differ, ADR 0003).
//   - An XML feed becomes its element tree.
//   - Other text becomes a string.
//   - Other content becomes its length and hash.
func asJSON(contentType, body string) string {
	if body == "" {
		return ""
	}
	var v any
	switch {
	case strings.Contains(contentType, "json"):
		return body
	case strings.Contains(contentType, "html"):
		v = microformats(body)
	case strings.Contains(contentType, "xml"):
		v = xmlTree(body)
	case strings.HasPrefix(contentType, "text/"):
		v = body
	default:
		sum := sha256.Sum256([]byte(body))
		v = map[string]any{"bytes": len(body), "sha256": hex.EncodeToString(sum[:])}
	}
	return string(must(json.Marshal(v)))
}

// microformats lists, in document order, each h-entry, h-card and h-feed of
// an HTML page, with its first u-url or u-uid. This is what the page
// publishes, whatever its template.
func microformats(page string) []string {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return []string{"<invalid HTML: " + err.Error() + ">"}
	}
	var out []string
	for n := range doc.Descendants() {
		for _, class := range classes(n) {
			if class == "h-entry" || class == "h-card" || class == "h-feed" {
				out = append(out, class+" "+firstURL(n))
			}
		}
	}
	return out
}

func classes(n *html.Node) []string {
	if n.Type != html.ElementNode {
		return nil
	}
	for _, a := range n.Attr {
		if a.Key == "class" {
			return strings.Fields(a.Val)
		}
	}
	return nil
}

// firstURL returns the href of the first u-url or u-uid in n, or of n itself.
// It skips microformats in n, such as an entry's author.
func firstURL(n *html.Node) string {
	if slices.Contains(classes(n), "u-url") || slices.Contains(classes(n), "u-uid") {
		for _, a := range n.Attr {
			if a.Key == "href" {
				return a.Val
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if slices.ContainsFunc(classes(c), func(class string) bool { return strings.HasPrefix(class, "h-") }) {
			continue
		}
		if url := firstURL(c); url != "" {
			return url
		}
	}
	return ""
}

// xmlTree parses an XML document into nested maps (name, attributes, text,
// children). Attribute order and insignificant whitespace thus don't count.
func xmlTree(body string) any {
	root := map[string]any{}
	stack := []map[string]any{root}
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return root["children"]
		}
		if err != nil {
			return "<invalid XML: " + err.Error() + ">"
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := map[string]any{"name": t.Name.Local}
			if len(t.Attr) > 0 {
				attrs := map[string]string{}
				for _, a := range t.Attr {
					attrs[strings.TrimPrefix(a.Name.Space+":"+a.Name.Local, ":")] = a.Value
				}
				n["attrs"] = attrs
			}
			children, _ := top["children"].([]any)
			top["children"] = append(children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if text := strings.TrimSpace(string(t)); text != "" {
				prev, _ := top["text"].(string)
				top["text"] = prev + text
			}
		}
	}
}
