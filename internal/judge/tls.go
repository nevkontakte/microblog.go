package judge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"
)

// A certAuthority issues certificates for the few https hosts the judge
// network serves. Instances are told to trust only it.
type certAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func newCertAuthority() (*certAuthority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "microblog.go judge CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return &certAuthority{cert: cert, key: key, leaves: make(map[string]*tls.Certificate)}, err
}

// writePEM writes the CA certificate, for SSL_CERT_FILE and the like.
func (ca *certAuthority) writePEM(path string) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o644)
}

func (ca *certAuthority) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c := ca.leaves[host]; c != nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ca.leaves[host] = c
	return c, nil
}

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
}

// contextServer serves the captured documents of one host.
func contextServer(paths map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, _ := jsonld.ReadFile("testdata/jsonld/" + name)
		w.Header().Set("Content-Type", "application/ld+json")
		w.Write(b)
	})
}
