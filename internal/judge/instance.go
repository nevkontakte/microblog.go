package judge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
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
	// Command runs the implementation's command-line tool on the instance's
	// data, with stdin as input, and returns its combined output. The
	// arguments are the original's: `inv` task names and flags.
	Command(ctx context.Context, stdin string, args ...string) (string, error)
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
// RSA key and upload directories. On a restart it rewrites the config and
// keeps the key and everything else.
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

	keyFile := filepath.Join(data, "key.pem")
	if _, err := os.Stat(keyFile); err == nil {
		return data, nil
	}
	// PKCS#1 PEM, the format pycryptodome's exportKey("PEM") writes.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return data, os.WriteFile(keyFile, keyPEM, 0o600)
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
func listen() (_ *os.File, _ string, err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, l.Close()) }() // the file is a duplicate of the socket
	f, err := l.(*net.TCPListener).File()
	return f, l.Addr().String(), err
}

// process is a child process in its own process group, logging to a file.
type process struct {
	cmd  *exec.Cmd
	done chan struct{} // closed once the process has exited and its log is closed
	// Valid once done is closed: why the process exited, and the error, if
	// any, from closing its log.
	exitErr, logErr error
}

// startProcess starts name with args, passing it files as descriptors 3 and up.
func startProcess(dir, logPath string, env []string, files []*os.File, name string, args ...string) (*process, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) // a restart appends
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
		return nil, errors.Join(err, log.Close())
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.exitErr = cmd.Wait()
		p.logErr = log.Close()
		close(p.done)
	}()
	return p, nil
}

// stop sends SIGTERM to the process group, then SIGKILL after a grace
// period, and waits for the process to exit. A process that had exited
// before it was stopped is an error: instances run until the judge stops
// them. Once stopped, how it exited doesn't matter.
func (p *process) stop() error {
	select {
	case <-p.done:
		return errors.Join(fmt.Errorf("%s exited by itself: %v", p.cmd, p.exitErr), p.logErr)
	default:
	}
	pgid := -p.cmd.Process.Pid
	if err := signalGroup(pgid, syscall.SIGTERM); err != nil {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		err := syscall.Kill(pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break // the whole group is gone
		}
		if err != nil {
			return err
		}
	}
	if err := signalGroup(pgid, syscall.SIGKILL); err != nil {
		return err
	}
	<-p.done
	return p.logErr
}

// signalGroup sends sig to a process group that may have exited already.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("sending %v to process group %d: %w", sig, -pgid, err)
	}
	return nil
}
