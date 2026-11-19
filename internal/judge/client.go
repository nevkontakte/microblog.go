package judge

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"golang.org/x/net/html"
)

// Client is the judge's own HTTP client. It reaches instances through the
// proxy, so its requests are recorded too, tagged "judge".
//
// Admin actions go through the same HTTP endpoints and form fields as the
// admin UI. Those endpoints are therefore part of the surface the judge
// holds both implementations to.
type Client struct {
	http *http.Client
}

func NewClient(proxyAddr string, roots *x509.CertPool) *Client {
	proxy, _ := url.Parse("http://" + proxyAddr)
	jar, _ := cookiejar.New(nil)
	return &Client{http: &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}},
		Jar:       jar,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // redirects carry results, e.g. a new object's URL
		},
	}}
}

// csrfToken fetches a fresh CSRF token from a page that embeds one: the login
// page before logging in, the new-post page after (the login page then
// redirects).
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

// inputValue returns the value of the named <input> in page. Admin pages may
// be rendered differently by the port (ADR 0013), so only the field counts,
// not its markup.
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
	Data        []byte
}

// Act performs an admin action, e.g. Act(ctx, a, "follow", url.Values{"ap_actor_id": ...}),
// and returns the redirect target.
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

// A Response is a fully read HTTP response.
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

// Post submits a multipart form, with a fresh CSRF token, to path on i,
// which the judge must be logged in to.
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
	w.WriteField("csrf_token", token)
	for k, vs := range form {
		for _, v := range vs {
			w.WriteField(k, v)
		}
	}
	for _, f := range files {
		fw, err := w.CreateFormFile(f.Field, f.Name)
		if err != nil {
			return nil, err
		}
		fw.Write(f.Data)
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL(i)+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req)
}

func (c *Client) do(req *http.Request) (*Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, err
}
