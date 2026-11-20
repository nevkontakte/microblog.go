package judge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A Result is the outcome of one scenario.
type Result struct {
	Name     string
	Passed   bool
	Log      []string
	Duration time.Duration
}

// Options configure a run.
type Options struct {
	A, B Impl   // implementations for instances "a" and "b"
	Work string // scratch directory for instances and their logs
}

// Run plays each scenario on a fresh pair. Each scenario gets a directory
// under Work holding both instances and their logs.
func Run(ctx context.Context, opts Options, scenarios []Scenario) []Result {
	work, err := filepath.Abs(opts.Work) // instances run in their own directories
	if err != nil {
		return []Result{{Name: "setup", Log: []string{err.Error()}}}
	}
	var results []Result
	for _, sc := range scenarios {
		start := time.Now()
		dir := filepath.Join(work, sc.Name)
		t, err := setUp(ctx, opts.A, opts.B, dir, sc)
		if err != nil {
			results = append(results, Result{Name: sc.Name, Log: []string{"SETUP: " + err.Error()}})
			continue
		}
		passed := t.run(sc.Run)
		t.A.Stop()
		t.B.Stop()
		t.Proxy.Close()
		results = append(results, Result{Name: sc.Name, Passed: passed, Log: t.log, Duration: time.Since(start)})
	}
	return results
}

func setUp(ctx context.Context, implA, implB Impl, dir string, sc Scenario) (_ *T, err error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	proxy, err := NewProxy()
	if err != nil {
		return nil, err
	}
	t := &T{Ctx: ctx, Proxy: proxy, impls: map[string]Impl{}, specs: map[string]Spec{}}
	defer func() {
		if err != nil {
			for _, i := range []Instance{t.A, t.B} {
				if i != nil {
					i.Stop()
				}
			}
			proxy.Close()
		}
	}()
	caFile := filepath.Join(dir, "ca.pem")
	if err := proxy.WriteCA(caFile); err != nil {
		return nil, err
	}
	judgeAddr, err := proxy.Listen("judge")
	if err != nil {
		return nil, err
	}
	t.Client = NewClient(judgeAddr, proxy.CertPool())

	start := func(impl Impl, name string) (Instance, error) {
		addr, err := proxy.Listen(name)
		if err != nil {
			return nil, err
		}
		spec := Spec{Name: name, Dir: filepath.Join(dir, name), ProxyAddr: addr, CAFile: caFile, Config: sc.Config[name]}
		if err := os.MkdirAll(spec.Dir, 0o755); err != nil {
			return nil, err
		}
		t.impls[name], t.specs[name] = impl, spec
		return t.start(name)
	}
	if t.A, err = start(implA, "a"); err != nil {
		return nil, err
	}
	if t.B, err = start(implB, "b"); err != nil {
		return nil, err
	}
	for _, i := range []Instance{t.A, t.B} {
		if err := t.waitReady(i); err != nil {
			return nil, err
		}
		if err := t.Client.Login(ctx, i); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// start starts the named instance from its spec and routes its host to it.
func (t *T) start(name string) (Instance, error) {
	inst, err := t.impls[name].Start(t.Ctx, t.specs[name])
	if err != nil {
		return nil, err
	}
	t.Proxy.Route(Host(inst), inst.Addr())
	return inst, nil
}

func (t *T) waitReady(i Instance) error {
	deadline := time.Now().Add(Timeout)
	for time.Now().Before(deadline) {
		resp, err := t.Client.Get(t.Ctx, BaseURL(i)+"/.well-known/nodeinfo", "")
		if err == nil && resp.Status == 200 {
			return nil
		}
		select {
		case <-t.Ctx.Done():
			return t.Ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s did not become ready", BaseURL(i))
}

// Summary formats results for the terminal.
func Summary(results []Result) (string, bool) {
	var b strings.Builder
	ok := true
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status, ok = "FAIL", false
		}
		fmt.Fprintf(&b, "%s %s (%.1fs)\n", status, r.Name, r.Duration.Seconds())
		if !r.Passed {
			for _, l := range r.Log {
				fmt.Fprintf(&b, "    %s\n", l)
			}
		}
	}
	return b.String(), ok
}
