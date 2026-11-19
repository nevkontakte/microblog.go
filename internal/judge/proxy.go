// Package judge runs scenario suites against pairs of microblog.pub
// implementations (the Python original and the Go port) that federate with
// each other on a fake network, and records everything they say.
//
// See docs/adr/0010-judge-federation-with-paired-instances.md.
package judge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	ca *certAuthority // issues the certificate each https host is served with

	// contexts serves the JSON-LD context hosts (jsonld.go). It is fixed at
	// construction, so it is read without the lock.
	contexts map[string]http.Handler

	// ctx is the context of every request served inside a tunnel; Close
	// cancels it, so a request stuck in a handler ends.
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex // guards the fields below

	// closing is set by Close; a tunnel opened after that is closed at once.
	closing bool

	// hosts maps a hostname on the fake network (a.test, f.test) to what
	// serves it: a forwarder to an instance's web server (Route), or a fake
	// actor (Handle). A host can be routed again, as when an instance
	// restarts on another address.
	hosts map[string]http.Handler

	// exchanges records every request the proxy served and its response.
	exchanges []Exchange

	// servers has one HTTP server per sender (Listen). Each sender gets its
	// own listener because that is how the proxy tells who sent a request:
	// the instances' outgoing requests carry nothing else that names them.
	servers []*http.Server

	// tunnels holds the senders' connections to the proxy that a CONNECT
	// turned into a TLS tunnel. Once hijacked, they no longer belong to an
	// http.Server, so Close closes them itself and waits on tunneling.
	tunnels   map[net.Conn]bool
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
	p.ctx, p.cancel = context.WithCancel(context.Background())
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

// Listen starts a proxy listener for one sender and returns its address,
// which that sender must use as its HTTP and HTTPS proxy. from names the
// sender in the recorded exchanges: an instance's name ("a", "b"), a fake
// actor's ("f"), or "judge" for the judge's own requests. Names are short
// lowercase words, as they also appear in golden recordings.
func (p *Proxy) Listen(from string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: p.serve(from)}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing { // Close wouldn't see this server
		l.Close()
		return "", errors.New("proxy is closed")
	}
	p.servers = append(p.servers, srv)
	go srv.Serve(l)
	return l.Addr().String(), nil
}

// Exchanges returns everything recorded so far, in the order the responses
// completed. A request that causes others (an inbox delivery whose receiver
// fetches the sender's key) comes after them.
func (p *Proxy) Exchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Exchange(nil), p.exchanges...)
}

// Close stops the listeners, cancels requests in flight, ends open tunnels
// and waits for them to finish.
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closing = true
	p.cancel()
	for _, s := range p.servers {
		s.Close()
	}
	for c := range p.tunnels {
		c.Close()
	}
	p.mu.Unlock()
	p.tunneling.Wait()
}

// serve handles what a sender asks of its proxy listener. Almost everything
// is https, which arrives as a CONNECT and continues inside a TLS tunnel.
// Plain http arrives as an ordinary request for an absolute http:// URL: the
// original falls back to http for WebFinger when https gives no answer
// (app/webfinger.py:68-77), and tries both for host-meta (:12-40). Both are
// recorded and must match on the wire like the rest.
func (p *Proxy) serve(from string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Hostname()
		if r.Method == http.MethodConnect {
			host, _, _ = strings.Cut(r.Host, ":")
		}
		h := p.handler(host)
		if h == nil {
			p.record(Exchange{From: from, Method: r.Method, URL: r.Host, Status: http.StatusBadGateway})
			http.Error(w, "unknown host "+host, http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodConnect {
			p.tunnel(from, host, w)
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

// exchange serves r with h and records both. The recorded request headers
// include Host, which HTTP signatures cover.
func (p *Proxy) exchange(from string, r *http.Request, h http.Handler) *httptest.ResponseRecorder {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	header := r.Header.Clone()
	header.Set("Host", r.Host)
	p.record(Exchange{
		From:       from,
		Method:     r.Method,
		URL:        r.URL.String(),
		ReqHeader:  header,
		ReqBody:    string(body),
		Status:     rec.Code,
		RespHeader: rec.Header().Clone(),
		RespBody:   rec.Body.String(),
	})
	return rec
}

// tunnel answers a CONNECT to an https host: it terminates TLS with a
// certificate from the judge CA and serves each request inside with the
// host's current handler, which changes when an instance restarts.
func (p *Proxy) tunnel(from, host string, w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		conn.Close()
		return
	}
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
		r = r.WithContext(p.ctx)
		r.URL.Scheme, r.URL.Host = "https", host
		r.Header.Set("X-Forwarded-Proto", "https") // uvicorn trusts it from localhost
		rec := p.exchange(from, r, p.handler(host))
		resp := rec.Result()
		resp.Request = r // so a HEAD response is written without a body
		if r.Method != http.MethodHead {
			resp.ContentLength = int64(rec.Body.Len())
		}
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
