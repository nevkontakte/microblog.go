package judge

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MastodonImage is the Mastodon release that the recorder records the replay
// set from (docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md).
const MastodonImage = "ghcr.io/mastodon/mastodon:v4.7.6"

// Mastodon is a real Mastodon server that runs in containers. It joins the
// fake network like an instance. The proxy routes its host to its web
// server, and it sends through its own proxy listener, with trust in the
// judge CA. Its only use is to record what Mastodon sends
// (mastodon_record.go).
type Mastodon struct {
	name       string
	addr       string            // the web server's address
	web        string            // the web server's container, which runs Rails code
	containers []string          // the containers that Stop removes, in order
	tokens     map[string]string // the API access token of each user
	dir        string            // the directory for the containers' logs
}

func (m *Mastodon) Name() string { return m.name }
func (m *Mastodon) Host() string { return m.name + ".test" }
func (m *Mastodon) Addr() string { return m.addr }

// mastodonSetup creates local users and prints an API token for each user
// (mastodon_setup.rb).
//
//go:embed mastodon_setup.rb
var mastodonSetup []byte

// mastodonPatch lets Mastodon fetch microblog.pub's actor (mastodon_patch.rb).
//
//go:embed mastodon_patch.rb
var mastodonPatch []byte

// StartMastodon starts a Mastodon server for s with the given local users,
// each with an API token. It needs a running Docker daemon. The containers
// use the host's network, so they can reach the proxy's listeners, and the
// proxy can reach the web server.
func StartMastodon(ctx context.Context, s Spec, users ...string) (_ *Mastodon, err error) {
	m := &Mastodon{name: s.Name, dir: s.Dir}
	defer func() {
		if err != nil {
			err = errors.Join(err, m.Stop())
		}
	}()
	ports, err := freePorts(3)
	if err != nil {
		return nil, err
	}
	pg, redis, web := ports[0], ports[1], ports[2]
	m.addr = fmt.Sprintf("127.0.0.1:%d", web)
	prefix := fmt.Sprintf("judge-%s-%d-", s.Name, os.Getpid())

	if err := m.startContainer(ctx, prefix+"postgres", "-e", "POSTGRES_USER=mastodon", "-e", "POSTGRES_PASSWORD=judge",
		"-e", fmt.Sprintf("PGPORT=%d", pg), "postgres:16-alpine"); err != nil {
		return nil, err
	}
	if err := m.startContainer(ctx, prefix+"redis", "redis:7-alpine", "redis-server", "--port", fmt.Sprint(redis)); err != nil {
		return nil, err
	}
	env, err := mastodonEnv(s, pg, redis, web)
	if err != nil {
		return nil, err
	}
	envFile := filepath.Join(s.Dir, "mastodon.env")
	if err := os.WriteFile(envFile, []byte(strings.Join(env, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	setupFile := filepath.Join(s.Dir, "setup.rb")
	if err := os.WriteFile(setupFile, mastodonSetup, 0o644); err != nil {
		return nil, err
	}
	// TODO(port): since v4.4.27, Mastodon can't resolve a microblog.pub
	// account that it didn't meet before, because the account's id is a bare
	// origin. This patch lets the recording run. The investigation and the
	// options are in migration/post-parity/mastodon-bare-origin.md.
	patchFile := filepath.Join(s.Dir, "judge_patch.rb")
	if err := os.WriteFile(patchFile, mastodonPatch, 0o644); err != nil {
		return nil, err
	}
	// The containers see the files at the same paths as the host does.
	mastodon := []string{"--env-file", envFile, "-v", s.Dir + ":" + s.Dir + ":ro", "-v", s.CAFile + ":" + s.CAFile + ":ro",
		"-v", patchFile + ":/opt/mastodon/config/initializers/judge_patch.rb:ro", MastodonImage}

	if err := waitFor(ctx, "postgres", func() error {
		return exec.CommandContext(ctx, "docker", "exec", prefix+"postgres", "pg_isready", "-p", fmt.Sprint(pg)).Run()
	}); err != nil {
		return nil, err
	}
	if _, err := docker(ctx, append([]string{"run", "--rm", "--network", "host"}, append(mastodon, "bin/rails", "db:setup")...)...); err != nil {
		return nil, fmt.Errorf("setting up the database: %w", err)
	}
	out, err := docker(ctx, append([]string{"run", "--rm", "--network", "host"}, append(mastodon, append([]string{"bin/rails", "runner", setupFile}, users...)...)...)...)
	if err != nil {
		return nil, fmt.Errorf("creating users: %w", err)
	}
	if m.tokens, err = parseTokens(out, users); err != nil {
		return nil, err
	}
	m.web = prefix + "web"
	if err := m.startContainer(ctx, m.web, append(mastodon, "bundle", "exec", "puma", "-C", "config/puma.rb")...); err != nil {
		return nil, err
	}
	if err := m.startContainer(ctx, prefix+"sidekiq", append(mastodon, "bundle", "exec", "sidekiq")...); err != nil {
		return nil, err
	}
	health := "http://" + m.addr + "/health"
	return m, waitFor(ctx, "Mastodon's web server", func() error {
		resp, err := http.Get(health)
		if err != nil {
			return err
		}
		if err := resp.Body.Close(); err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", health, resp.Status)
		}
		return nil
	})
}

// mastodonEnv is the configuration of a production Mastodon server for s,
// with new secrets. The server sends through s's proxy listener and trusts
// the judge CA.
func mastodonEnv(s Spec, pg, redis, web int) ([]string, error) {
	secret := func(bytes int) (string, error) {
		b := make([]byte, bytes)
		_, err := rand.Read(b)
		return hex.EncodeToString(b), err
	}
	env := []string{
		"RAILS_ENV=production", "NODE_ENV=production", "RAILS_LOG_LEVEL=info",
		"LOCAL_DOMAIN=" + s.Name + ".test", "LOCAL_HTTPS=true",
		"DB_HOST=127.0.0.1", fmt.Sprintf("DB_PORT=%d", pg), "DB_USER=mastodon", "DB_PASS=judge", "DB_NAME=mastodon",
		"REDIS_HOST=127.0.0.1", fmt.Sprintf("REDIS_PORT=%d", redis),
		"BIND=127.0.0.1", fmt.Sprintf("PORT=%d", web),
		// Mastodon sends every request through http_proxy
		// (config/initializers/http_client_proxy.rb), and its HTTP client
		// trusts the authorities in SSL_CERT_FILE.
		"http_proxy=http://" + s.ProxyAddr, "SSL_CERT_FILE=" + s.CAFile,
	}
	for name, bytes := range map[string]int{"SECRET_KEY_BASE": 64, "OTP_SECRET": 64,
		"ACTIVE_RECORD_ENCRYPTION_DETERMINISTIC_KEY": 16, "ACTIVE_RECORD_ENCRYPTION_KEY_DERIVATION_SALT": 16,
		"ACTIVE_RECORD_ENCRYPTION_PRIMARY_KEY": 16} {
		v, err := secret(bytes)
		if err != nil {
			return nil, err
		}
		env = append(env, name+"="+v)
	}
	return env, nil
}

// parseTokens reads the "name=token" lines that mastodon_setup.rb prints.
// The output also holds other lines that Rails logs.
func parseTokens(out string, users []string) (map[string]string, error) {
	tokens := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for _, u := range users {
			if token, ok := strings.CutPrefix(line, u+"="); ok {
				tokens[u] = token
			}
		}
	}
	if len(tokens) != len(users) {
		return nil, fmt.Errorf("tokens for %d of %d users in: %s", len(tokens), len(users), out)
	}
	return tokens, nil
}

// API calls Mastodon's REST API as user, through the judge's client, and
// decodes the JSON answer.
func (m *Mastodon) API(ctx context.Context, c *Client, user, method, path string, params url.Values) (any, error) {
	header := http.Header{"Authorization": {"Bearer " + m.tokens[user]}}
	var body []byte
	if params != nil {
		header.Set("Content-Type", "application/x-www-form-urlencoded")
		body = []byte(params.Encode())
	}
	resp, err := c.Send(ctx, method, "https://"+m.Host()+path, header, body)
	if err != nil {
		return nil, err
	}
	if resp.Status/100 != 2 {
		return nil, fmt.Errorf("%s %s: %d: %.300s", method, path, resp.Status, resp.Body)
	}
	var v any
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return v, nil
}

// Upload uploads an image as user and returns its media attachment's id.
func (m *Mastodon) Upload(ctx context.Context, c *Client, user string, f File) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", f.Name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(f.Data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	header := http.Header{"Authorization": {"Bearer " + m.tokens[user]}, "Content-Type": {w.FormDataContentType()}}
	resp, err := c.Send(ctx, http.MethodPost, "https://"+m.Host()+"/api/v1/media", header, body.Bytes())
	if err != nil {
		return "", err
	}
	if resp.Status != http.StatusOK {
		return "", fmt.Errorf("uploading %s: %d: %.300s", f.Name, resp.Status, resp.Body)
	}
	var media struct{ ID string }
	return media.ID, json.Unmarshal(resp.Body, &media)
}

// RunRails runs Ruby code in the web server's Rails environment. Use it for
// actions that Mastodon's API doesn't have.
func (m *Mastodon) RunRails(ctx context.Context, code string) (string, error) {
	return docker(ctx, "exec", m.web, "bin/rails", "runner", code)
}

// Stop saves each container's log in the server's directory and removes the
// containers.
func (m *Mastodon) Stop() error {
	var errs []error
	for _, c := range m.containers {
		out, err := exec.Command("docker", "logs", c).CombinedOutput()
		errs = append(errs, err, os.WriteFile(filepath.Join(m.dir, c+".log"), out, 0o644))
		if out, err := exec.Command("docker", "rm", "-f", c).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w: %s", c, err, out))
		}
	}
	m.containers = nil
	return errors.Join(errs...)
}

// startContainer starts a container on the host's network. m.Stop removes
// it.
func (m *Mastodon) startContainer(ctx context.Context, name string, args ...string) error {
	_, err := docker(ctx, append([]string{"run", "-d", "--name", name, "--network", "host"}, args...)...)
	if err == nil {
		m.containers = append(m.containers, name)
	}
	return err
}

// docker runs a docker command and returns its output.
func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, out)
	}
	return string(out), nil
}

// waitFor calls ready every second until it succeeds, for five minutes at
// most. A cold Mastodon needs much time to start.
func waitFor(ctx context.Context, what string, ready func() error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		err := ready()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w (last: %v)", what, ctx.Err(), err)
		case <-time.After(time.Second):
		}
	}
}

// freePorts returns n ports that no process listens on. Use it for servers
// that can't receive a socket.
func freePorts(n int) (_ []int, err error) {
	var ports []int
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		// freePorts holds each port until it chose all of them, so the n
		// ports are different.
		defer func() { err = errors.Join(err, l.Close()) }()
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}
