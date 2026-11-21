package judge

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"

	"golang.org/x/net/html"
)

// Client is the judge's own HTTP client. It sends requests to instances
// through the proxy, so the proxy records them too, with the tag "judge".
//
// Admin actions use the same HTTP endpoints and form fields as the admin
// UI. Thus these endpoints are part of the interface that the judge compares
// between the two implementations.
type Client struct {
	http *http.Client
}

func NewClient(proxyAddr string, roots *x509.CertPool) *Client {
	return &Client{http: &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(must(url.Parse("http://" + proxyAddr))),
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Jar:     must(cookiejar.New(nil)),
		Timeout: Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // redirects hold results, for example a new object's URL
		},
	}}
}

// csrfToken fetches a new CSRF token from a page that holds one:
//   - Before the login, the login page.
//   - After the login, the page for new posts, because the login page then
//     redirects.
func (c *Client) csrfToken(ctx context.Context, i Instance, page string) (string, error) {
	url := BaseURL(i) + page
	resp, err := c.Get(ctx, url, "")
	if err != nil {
		return "", err
	}
	if token := inputValue(resp.Body, "csrf_token"); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no csrf_token on %s (status %d)", url, resp.Status)
}

// inputValue returns the value of the named <input> in page. The port can
// render admin pages differently (ADR 0003). Thus only the field is
// important, not its markup.
func inputValue(page []byte, name string) string {
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != "input" {
				continue
			}
			attrs := map[string]string{}
			for _, a := range tok.Attr {
				attrs[a.Key] = a.Val
			}
			if attrs["name"] == name {
				return attrs["value"]
			}
		}
	}
}

// Login starts an admin session on the instance.
func (c *Client) Login(ctx context.Context, i Instance) error {
	token, err := c.csrfToken(ctx, i, "/admin/login")
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, i, "/admin/login", token, url.Values{"password": {AdminPassword}})
	if err != nil {
		return err
	}
	if resp.Status != http.StatusFound {
		return fmt.Errorf("login to %s: status %d", BaseURL(i), resp.Status)
	}
	return nil
}

// A File is a file upload in a form.
type File struct {
	Field, Name string
	ContentType string // application/octet-stream if empty
	Data        []byte
}

// Act does an admin action, and returns the redirect target. For example:
// Act(ctx, a, "follow", url.Values{"ap_actor_id": ...}).
func (c *Client) Act(ctx context.Context, i Instance, action string, form url.Values, files ...File) (string, error) {
	if form.Get("redirect_url") == "" {
		form.Set("redirect_url", BaseURL(i)+"/admin")
	}
	resp, err := c.Post(ctx, i, "/admin/actions/"+action, form, files...)
	if err != nil {
		return "", err
	}
	if resp.Status != http.StatusFound {
		return "", fmt.Errorf("%s on %s: status %d: %s", action, BaseURL(i), resp.Status, resp.Body)
	}
	return resp.Header.Get("Location"), nil
}

// A Response is an HTTP response that the client read completely.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Get fetches url. A non-empty accept sets the Accept header.
func (c *Client) Get(ctx context.Context, url, accept string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return c.do(req)
}

// GetAP fetches an ActivityPub object and decodes it.
func (c *Client) GetAP(ctx context.Context, url string) (map[string]any, error) {
	resp, err := c.Get(ctx, url, "application/activity+json")
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.Status)
	}
	var obj map[string]any
	if err := json.Unmarshal(resp.Body, &obj); err != nil {
		return nil, fmt.Errorf("GET %s: %v", url, err)
	}
	return obj, nil
}

// Post sends a multipart form, with a new CSRF token, to path on i. The judge
// must have an admin session on i.
func (c *Client) Post(ctx context.Context, i Instance, path string, form url.Values, files ...File) (*Response, error) {
	token, err := c.csrfToken(ctx, i, "/admin/new")
	if err != nil {
		return nil, err
	}
	return c.post(ctx, i, path, token, form, files...)
}

func (c *Client) post(ctx context.Context, i Instance, path, token string, form url.Values, files ...File) (*Response, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf_token", token); err != nil {
		return nil, err
	}
	for k, vs := range form {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, f.Field, f.Name))
		h.Set("Content-Type", cmp.Or(f.ContentType, "application/octet-stream"))
		fw, err := w.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL(i)+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req)
}

// Send makes a request with the given headers, as an API client does.
func (c *Client) Send(ctx context.Context, method, url string, header http.Header, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	maps.Copy(req.Header, header)
	return c.do(req)
}

func (c *Client) do(req *http.Request) (_ *Response, err error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	body, err := io.ReadAll(resp.Body)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, err
}
