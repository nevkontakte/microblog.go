package judge

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Python starts instances of the reference implementation.
type Python struct {
	Venv string // the virtualenv with the reference's dependencies
	App  string // the copy of the reference that PreparePython makes, ready to run

	// baselineDB is a database that PreparePython migrates one time. Each new
	// instance starts from a copy of it. A migration takes approximately one
	// second for each instance, and all new migrated databases are the same.
	baselineDB string
}

// PreparePython copies the reference implementation from src into dst, and
// then does these steps on the copy:
//  1. It applies patches. The patches break the code on purpose, to test the
//     judge.
//  2. It compiles the stylesheet into app/, where the reference reads it.
//
// PreparePython doesn't change the reference checkout.
func PreparePython(src, dst, venv string, patches ...string) (*Python, error) {
	// Child processes in other directories use the two paths, so they must
	// be absolute.
	dst, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	if venv, err = filepath.Abs(venv); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dst, "data"), 0o755); err != nil {
		return nil, err
	}
	for _, f := range []string{"app", "alembic", "alembic.ini", "boussole.json", "tasks.py"} {
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
	py := &Python{Venv: venv, App: dst}
	baseline := filepath.Join(dst, "baseline")
	if err := os.MkdirAll(baseline, 0o755); err != nil {
		return nil, err
	}
	if err := py.link(baseline); err != nil {
		return nil, err
	}
	if _, err := writeData(Spec{Name: "baseline", Dir: baseline}); err != nil {
		return nil, err
	}
	if err := py.migrate(context.Background(), baseline, os.Environ()); err != nil {
		return nil, err
	}
	py.baselineDB = filepath.Join(baseline, "data", "microblogpub.db")
	return py, nil
}

// link makes dir ready to run as an instance of the copy. All instances use
// the same code, but each dir has its own data.
func (py *Python) link(dir string) error {
	for _, f := range []string{"app", "alembic", "alembic.ini", "tasks.py"} {
		err := os.Symlink(filepath.Join(py.App, f), filepath.Join(dir, f))
		if err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// migrate applies all migrations to the database in dir/data.
func (py *Python) migrate(ctx context.Context, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, filepath.Join(py.Venv, "bin", "alembic"), "upgrade", "head")
	cmd.Dir, cmd.Env = dir, env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("migrating %s: %v: %s", dir, err, out)
	}
	return nil
}

type pythonInstance struct {
	name, dir, data, addr string
	env                   []string
	inv                   string // the original's command-line tool
	procs                 []*process
}

func (i *pythonInstance) Name() string    { return i.name }
func (i *pythonInstance) DataDir() string { return i.data }
func (i *pythonInstance) Addr() string    { return i.addr }

func (i *pythonInstance) Command(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, i.inv, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = i.dir, i.env, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (i *pythonInstance) Stop() error {
	var errs []error
	for _, p := range i.procs {
		errs = append(errs, p.stop())
	}
	return errors.Join(errs...)
}

// Start runs one instance as misc/supervisord.conf does: a web server, and the
// workers for incoming and outgoing activities. A second Start on the same
// directory restarts the instance with its current data.
func (py *Python) Start(ctx context.Context, s Spec) (_ Instance, err error) {
	data, err := writeData(s)
	if err != nil {
		return nil, err
	}
	if err := py.link(s.Dir); err != nil {
		return nil, err
	}
	// The hash seed is fixed, because app/actor.py loops over a set of URLs,
	// and the order of requests on the wire must be the same in all runs.
	env := proxyEnv(s, "PYTHONHASHSEED=0")
	bin := func(name string) string { return filepath.Join(py.Venv, "bin", name) }

	// A new instance's database is a copy of the baseline. Start migrates the
	// database of a restarted instance, as an upgrade does.
	db := filepath.Join(data, "microblogpub.db")
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) {
		baseline, err := os.ReadFile(py.baselineDB)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(db, baseline, 0o644); err != nil {
			return nil, err
		}
	} else if err := py.migrate(ctx, s.Dir, env); err != nil {
		return nil, err
	}

	socket, addr, err := listen()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, socket.Close()) }() // uvicorn has its own copy
	inst := &pythonInstance{name: s.Name, dir: s.Dir, data: data, addr: addr, env: env, inv: bin("inv")}
	start := func(log string, files []*os.File, name string, args ...string) error {
		p, err := startProcess(s.Dir, filepath.Join(s.Dir, log), env, files, name, args...)
		if err == nil {
			inst.procs = append(inst.procs, p)
		}
		return err
	}
	worker := func(module string) string {
		return fmt.Sprintf("import asyncio; from app.%s import loop; asyncio.run(loop())", module)
	}
	if err := start("web.log", []*os.File{socket}, bin("uvicorn"), "app.main:app", "--no-server-header", "--fd", "3"); err != nil {
		return nil, err
	}
	if err := start("incoming.log", nil, bin("python"), "-c", worker("incoming_activities")); err != nil {
		return nil, errors.Join(err, inst.Stop())
	}
	if err := start("outgoing.log", nil, bin("python"), "-c", worker("outgoing_activities")); err != nil {
		return nil, errors.Join(err, inst.Stop())
	}
	return inst, nil
}
