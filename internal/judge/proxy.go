// Package judge runs scenario suites against pairs of microblog.pub
// implementations (the Python original and the Go port) that federate with
// each other on a fake network, and records everything they say.
//
// See docs/adr/0010-judge-federation-with-paired-instances.md.
package judge

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// An Exchange is one HTTP request and its response, as seen by the proxy.
type Exchange struct {
	From       string      `json:"from"` // sender: an instance name or "judge"
	Method     string      `json:"method"`
	URL        string      `json:"url"`
	ReqHeader  http.Header `json:"reqHeader"`
	ReqBody    string      `json:"reqBody,omitempty"`
	Status     int         `json:"status"`
	RespHeader http.Header `json:"respHeader"`
	RespBody   string      `json:"respBody,omitempty"`
}

// Proxy is a forward HTTP proxy for the fake *.test network. Instances use it
// as their HTTPS_PROXY, so they reach each other by hostname without DNS. It
// records every exchange, tagged with the listener it arrived on.
//
// Every host is served over https, like a production instance behind a
// TLS-terminating reverse proxy: the proxy terminates TLS with certificates
// from a per-run authority that instances are told to trust (WriteCA), and
// forwards plain http with X-Forwarded-Proto: https. Besides the *.test hosts,
// it serves the JSON-LD contexts that LD signatures need.
type Proxy struct {
	ca       *certAuthority
	contexts map[string]http.Handler // JSON-LD context hosts, fixed at construction

	mu        sync.Mutex
	hosts     map[string]http.Handler
	exchanges []Exchange
	servers   []*http.Server
	tunnels   map[net.Conn]bool // hijacked, so the servers don't close them
	tunneling sync.WaitGroup
}

func NewProxy() (*Proxy, error) {
	ca, err := newCertAuthority()
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		ca:       ca,
		contexts: make(map[string]http.Handler),
		hosts:    make(map[string]http.Handler),
		tunnels:  make(map[net.Conn]bool),
	}
	for host, paths := range jsonLDContexts {
		p.contexts[host] = contextServer(paths)
	}
	return p, nil
}

// WriteCA writes the certificate instances must trust for https.
func (p *Proxy) WriteCA(path string) error { return p.ca.writePEM(path) }

// CertPool returns a pool holding the certificate to trust for https.
func (p *Proxy) CertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(p.ca.cert)
	return pool
}

// Handle serves requests for host with h.
func (p *Proxy) Handle(host string, h http.Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hosts[host] = h
}

// Route forwards requests for host to the HTTP server listening on addr.
func (p *Proxy) Route(host, addr string) {
	p.Handle(host, forwarder(addr))
}

// Listen starts a proxy listener for one sender and returns its address.
func (p *Proxy) Listen(from string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: p.serve(from)}
	p.mu.Lock()
	p.servers = append(p.servers, srv)
	p.mu.Unlock()
	go srv.Serve(l)
	return l.Addr().String(), nil
}

// Exchanges returns everything recorded so far, in arrival order.
func (p *Proxy) Exchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Exchange(nil), p.exchanges...)
}

// Close stops the listeners, ends open tunnels and waits for them to finish.
func (p *Proxy) Close() {
	p.mu.Lock()
	for _, s := range p.servers {
		s.Close()
	}
	for c := range p.tunnels {
		c.Close()
	}
	p.mu.Unlock()
	p.tunneling.Wait()
}

func (p *Proxy) serve(from string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Hostname()
		if r.Method == http.MethodConnect || host == "" {
			host, _, _ = strings.Cut(r.Host, ":")
		}
		h := p.handler(host)
		if h == nil {
			p.record(Exchange{From: from, Method: r.Method, URL: r.Host, Status: http.StatusBadGateway})
			http.Error(w, "unknown host "+host, http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodConnect {
			p.tunnel(from, host, w, h)
			return
		}
		rec := p.exchange(from, r, h)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	})
}

func (p *Proxy) handler(host string) http.Handler {
	if h := p.contexts[host]; h != nil {
		return h
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hosts[host]
}

// exchange serves r with h and records both.
func (p *Proxy) exchange(from string, r *http.Request, h http.Handler) *httptest.ResponseRecorder {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	p.record(Exchange{
		From:       from,
		Method:     r.Method,
		URL:        r.URL.String(),
		ReqHeader:  r.Header.Clone(),
		ReqBody:    string(body),
		Status:     rec.Code,
		RespHeader: rec.Header().Clone(),
		RespBody:   rec.Body.String(),
	})
	return rec
}

// tunnel answers a CONNECT to an https host: it terminates TLS with a
// certificate from the judge CA and serves the requests inside with h.
func (p *Proxy) tunnel(from, host string, w http.ResponseWriter, h http.Handler) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	p.mu.Lock()
	p.tunnels[conn] = true
	p.tunneling.Add(1)
	p.mu.Unlock()
	defer func() {
		conn.Close()
		p.mu.Lock()
		delete(p.tunnels, conn)
		p.mu.Unlock()
		p.tunneling.Done()
	}()
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	tlsConn := tls.Server(conn, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return p.ca.leaf(host)
	}})
	in := bufio.NewReader(tlsConn)
	for {
		r, err := http.ReadRequest(in)
		if err != nil {
			return
		}
		r.URL.Scheme, r.URL.Host = "https", host
		r.Header.Set("X-Forwarded-Proto", "https") // uvicorn trusts it from localhost
		rec := p.exchange(from, r, h)
		resp := rec.Result()
		resp.ContentLength = int64(rec.Body.Len())
		if err := resp.Write(tlsConn); err != nil {
			return
		}
	}
}

func (p *Proxy) record(e Exchange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exchanges = append(p.exchanges, e)
}

// forwarder relays requests to addr unchanged, keeping the Host header the
// sender used, since HTTP signatures cover it.
func forwarder(addr string) http.Handler {
	transport := &http.Transport{Proxy: nil}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.URL.Scheme = "http"
		out.URL.Host = addr
		out.Header.Del("Proxy-Connection")
		resp, err := transport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
}
