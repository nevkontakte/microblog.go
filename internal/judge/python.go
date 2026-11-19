package judge

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
)

// Python starts instances of the reference implementation.
type Python struct {
	Venv string // virtualenv holding the reference's dependencies
	App  string // runnable copy of the reference, made by PreparePython
}

// PreparePython copies the reference implementation from src into dst,
// applies patches to the copy (used to validate the judge against
// deliberately broken code), and compiles its stylesheet, which the
// reference expects to find in app/. The reference checkout itself stays
// untouched.
func PreparePython(src, dst, venv string, patches ...string) (*Python, error) {
	dst, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dst, "data"), 0o755); err != nil {
		return nil, err
	}
	for _, f := range []string{"app", "alembic", "alembic.ini", "boussole.json"} {
		if out, err := exec.Command("cp", "-r", filepath.Join(src, f), dst).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("copying %s: %v: %s", f, err, out)
		}
	}
	for _, p := range patches {
		p, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		if out, err := exec.Command("patch", "-p1", "-d", dst, "-i", p).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("applying %s: %v: %s", p, err, out)
		}
	}
	theme := filepath.Join(dst, "data", "_theme.scss")
	if err := os.WriteFile(theme, []byte("// default theme\n"), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(venv, "bin", "boussole"), "compile")
	cmd.Dir = dst
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compiling CSS: %v: %s", err, out)
	}
	return &Python{Venv: venv, App: dst}, nil
}

type pythonInstance struct {
	name, data, addr string
	procs            []*process
}

func (i *pythonInstance) Name() string    { return i.name }
func (i *pythonInstance) DataDir() string { return i.data }
func (i *pythonInstance) Addr() string    { return i.addr }

func (i *pythonInstance) Stop() error {
	for _, p := range i.procs {
		p.stop()
	}
	return nil
}

// Start runs one instance the way misc/supervisord.conf does: a web server
// plus the incoming and outgoing activity workers.
func (py *Python) Start(ctx context.Context, s Spec) (Instance, error) {
	data, err := writeData(s)
	if err != nil {
		return nil, err
	}
	for _, f := range []string{"app", "alembic", "alembic.ini"} {
		if err := os.Symlink(filepath.Join(py.App, f), filepath.Join(s.Dir, f)); err != nil {
			return nil, err
		}
	}
	// A fixed hash seed: app/actor.py iterates over a set of URLs, and the
	// order of requests on the wire must not change from run to run.
	env := proxyEnv(s, "PYTHONHASHSEED=0")
	bin := func(name string) string { return filepath.Join(py.Venv, "bin", name) }

	migrate := exec.CommandContext(ctx, bin("alembic"), "upgrade", "head")
	migrate.Dir, migrate.Env = s.Dir, env
	if out, err := migrate.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("migrating %s: %v: %s", s.Name, err, out)
	}

	addr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(addr)
	inst := &pythonInstance{name: s.Name, data: data, addr: addr}
	start := func(log string, name string, args ...string) error {
		p, err := startProcess(s.Dir, filepath.Join(s.Dir, log), env, name, args...)
		if err == nil {
			inst.procs = append(inst.procs, p)
		}
		return err
	}
	worker := func(module string) string {
		return fmt.Sprintf("import asyncio; from app.%s import loop; asyncio.run(loop())", module)
	}
	if err := start("web.log", bin("uvicorn"), "app.main:app", "--no-server-header", "--host", host, "--port", port); err != nil {
		return nil, err
	}
	if err := start("incoming.log", bin("python"), "-c", worker("incoming_activities")); err != nil {
		inst.Stop()
		return nil, err
	}
	if err := start("outgoing.log", bin("python"), "-c", worker("outgoing_activities")); err != nil {
		inst.Stop()
		return nil, err
	}
	return inst, nil
}
