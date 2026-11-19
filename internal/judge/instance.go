package judge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AdminPassword is the admin password of every judge instance.
const AdminPassword = "judge"

// An Instance is a running microblog.pub server on the fake network,
// reachable at http://<Name>.test.
type Instance interface {
	Name() string    // also the local actor's username
	DataDir() string // the instance's data/ directory
	Addr() string    // where its web server listens, for the proxy to route to
	Stop() error
}

// Host returns the instance's hostname on the fake network.
func Host(i Instance) string { return i.Name() + ".test" }

// BaseURL returns the instance's ID, e.g. https://a.test.
func BaseURL(i Instance) string { return "https://" + Host(i) }

// Spec describes an instance to start.
type Spec struct {
	Name      string
	Dir       string // working directory; data/ is created inside it
	ProxyAddr string // the proxy listener this instance sends through
	CAFile    string // the only certificate authority to trust for https
	Config    string // extra profile.toml lines
}

// An Impl starts instances of one implementation.
type Impl interface {
	Start(ctx context.Context, s Spec) (Instance, error)
}

// writeData creates the data/ directory both implementations read: config,
// RSA key and upload directories.
func writeData(s Spec) (string, error) {
	data := filepath.Join(s.Dir, "data")
	for _, d := range []string{"uploads", "custom_emoji"} {
		if err := os.MkdirAll(filepath.Join(data, d), 0o755); err != nil {
			return "", err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(AdminPassword), bcrypt.MinCost)
	if err != nil {
		return "", err
	}
	config := fmt.Sprintf(`domain = %q
username = %q
admin_password = %q
name = "Instance %s"
summary = "<p>Judge instance %s</p>"
https = true
secret = "judge-secret-%s-0123456789abcdef"
debug = true
%s
`, s.Name+".test", s.Name, hash, s.Name, s.Name, s.Name, s.Config)
	if err := os.WriteFile(filepath.Join(data, "profile.toml"), []byte(config), 0o644); err != nil {
		return "", err
	}

	// PKCS#1 PEM, the format pycryptodome's exportKey("PEM") writes.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return data, os.WriteFile(filepath.Join(data, "key.pem"), keyPEM, 0o600)
}

// proxyEnv returns the environment for an instance process: the parent's,
// with every proxy variable pointing at the judge proxy and https trusting
// only the judge CA.
func proxyEnv(s Spec, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE":
			continue
		}
		env = append(env, kv)
	}
	proxy := "http://" + s.ProxyAddr
	env = append(env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "http_proxy="+proxy, "https_proxy="+proxy,
		"SSL_CERT_FILE="+s.CAFile, "REQUESTS_CA_BUNDLE="+s.CAFile)
	return append(env, extra...)
}

// freeAddr returns a localhost address with a port that is free right now.
func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

// process is a child process in its own process group, logging to a file.
type process struct{ cmd *exec.Cmd }

func startProcess(dir, logPath string, env []string, name string, args ...string) (*process, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	go func() { cmd.Wait(); log.Close() }()
	return &process{cmd}, nil
}

// stop sends SIGTERM to the process group, then SIGKILL after a grace period.
func (p *process) stop() {
	pgid := -p.cmd.Process.Pid
	syscall.Kill(pgid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pgid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pgid, syscall.SIGKILL)
}
