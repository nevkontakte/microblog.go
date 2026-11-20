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
	Config    string // profile.toml lines, overriding the defaults by key
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
	if err := os.WriteFile(filepath.Join(data, "profile.toml"), []byte(profile(s, hash)), 0o644); err != nil {
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

// profile returns the instance's profile.toml: the defaults, with any key
// that s.Config sets replaced by its last line for that key.
func profile(s Spec, passwordHash []byte) string {
	defaults := [][2]string{
		{"domain", fmt.Sprintf("%q", s.Name+".test")},
		{"username", fmt.Sprintf("%q", s.Name)},
		{"admin_password", fmt.Sprintf("%q", passwordHash)},
		{"name", fmt.Sprintf("%q", "Instance "+s.Name)},
		{"summary", fmt.Sprintf("%q", "<p>Judge instance "+s.Name+"</p>")},
		{"https", "true"},
		{"secret", fmt.Sprintf("%q", "judge-secret-"+s.Name+"-0123456789abcdef")},
		{"debug", "true"},
	}
	lines := strings.Split(s.Config, "\n")
	last := map[string]int{} // key -> index of its last line in s.Config
	for n, line := range lines {
		if key, _, ok := strings.Cut(line, "="); ok {
			last[strings.TrimSpace(key)] = n
		}
	}
	var b strings.Builder
	for _, kv := range defaults {
		if _, set := last[kv[0]]; !set {
			fmt.Fprintf(&b, "%s = %s\n", kv[0], kv[1])
		}
	}
	for n, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if strings.TrimSpace(line) != "" && (!ok || last[strings.TrimSpace(key)] == n) {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
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

// listen binds a localhost port for an instance's web server, and returns
// the socket as a file for the server process to inherit and its address.
// Handing over the bound socket, rather than a port number to bind, leaves no
// moment in which another connection can take the port.
func listen() (*os.File, string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	defer l.Close() // the file is a duplicate of the socket
	f, err := l.(*net.TCPListener).File()
	return f, l.Addr().String(), err
}

// process is a child process in its own process group, logging to a file.
type process struct{ cmd *exec.Cmd }

// startProcess starts name with args, passing it files as descriptors 3 and up.
func startProcess(dir, logPath string, env []string, files []*os.File, name string, args ...string) (*process, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.ExtraFiles = files
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
