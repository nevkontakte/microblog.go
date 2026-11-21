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

// An Instance is a running microblog.pub server on the fake network, at
// http://<Name>.test.
type Instance interface {
	Name() string    // the instance's name, which is also the local actor's username
	DataDir() string // the instance's data/ directory
	Addr() string    // the address of the web server, where the proxy sends requests
	Stop() error
	// Command runs the implementation's command-line tool on the instance's
	// data, with stdin as input, and returns its combined output. The
	// arguments are the original's arguments: `inv` task names and flags.
	Command(ctx context.Context, stdin string, args ...string) (string, error)
}

// Host returns the instance's hostname on the fake network.
func Host(i Instance) string { return i.Name() + ".test" }

// BaseURL returns the instance's ID, for example https://a.test.
func BaseURL(i Instance) string { return "https://" + Host(i) }

// Spec describes an instance to start.
type Spec struct {
	Name      string
	Dir       string // the working directory, which holds data/
	ProxyAddr string // the proxy listener that the instance sends through
	CAFile    string // the only certificate authority that the instance trusts for https
	Config    string // profile.toml lines, which replace the defaults with the same key
}

// An Impl starts instances of one implementation.
type Impl interface {
	Start(ctx context.Context, s Spec) (Instance, error)
}

// writeData makes the data/ directory that both implementations read: the
// config, the RSA key and the upload directories. On a restart, it writes the
// config again, and keeps the key and all other files.
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
	// PKCS#1 PEM is the format that pycryptodome's exportKey("PEM") writes.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return data, os.WriteFile(keyFile, keyPEM, 0o600)
}

// profile returns the instance's profile.toml. It starts with the defaults.
// For each key that s.Config sets, the last line of s.Config for that key
// replaces the default.
func profile(s Spec, passwordHash []byte) string {
	defaults := [][2]string{
		{"domain", fmt.Sprintf("%q", s.Name+".test")},
		{"username", fmt.Sprintf("%q", s.Name)},
		{"admin_password", fmt.Sprintf("%q", passwordHash)},
		{"name", fmt.Sprintf("%q", "Instance "+s.Name)},
		{"summary", fmt.Sprintf("%q", "<p>Judge instance "+s.Name+"</p>")},
		{"https", "true"},
		{"secret", fmt.Sprintf("%q", configSecret(s.Name))},
		{"debug", "true"},
	}
	lines := strings.Split(s.Config, "\n")
	last := map[string]int{} // the index of each key's last line in s.Config
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

// configSecret is the secret in the profile.toml of the instance name. The
// instance signs sessions and media proxy URLs with it.
func configSecret(name string) string { return "judge-secret-" + name + "-0123456789abcdef" }

// proxyEnv returns the environment for an instance process. It is the
// parent's environment with these changes:
//   - All proxy variables point to the judge proxy.
//   - For https, the process trusts only the judge CA.
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

// listen binds a localhost port for an instance's web server. It returns the
// socket as a file, which the server process inherits, and its address. The
// judge gives the bound socket to the server, not a port number. Thus no
// other process can take the port before the server binds it.
func listen() (_ *os.File, _ string, err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, l.Close()) }() // the file is a copy of the socket
	f, err := l.(*net.TCPListener).File()
	return f, l.Addr().String(), err
}

// process is a child process in its own process group. It writes its log to
// a file.
type process struct {
	cmd  *exec.Cmd
	done chan struct{} // closed after the process exits and its log closes
	// The fields that follow are valid after done closes. They hold the
	// reason why the process exited, and the error from the log's close, if
	// there is one.
	exitErr, logErr error
}

// startProcess starts name with args. It gives files to the process as
// descriptors 3 and higher.
func startProcess(dir, logPath string, env []string, files []*os.File, name string, args ...string) (*process, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) // a restart adds to the end of the log
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
// period, and waits for the process to exit. If the process exited before
// stop, stop returns an error, because instances must run until the judge
// stops them. After stop, the exit status of the process doesn't matter.
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
			break // no process of the group is left
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

// signalGroup sends sig to a process group. The group may have exited
// already.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("sending %v to process group %d: %w", sig, -pgid, err)
	}
	return nil
}
