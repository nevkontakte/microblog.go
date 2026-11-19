package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	Work string // scratch directory for instances, logs and recordings
	// Golden is the directory of golden wire recordings. With UpdateGolden,
	// the run writes them instead of comparing against them.
	Golden       string
	UpdateGolden bool
}

// Run plays each scenario on a fresh pair. A scenario passes if its body
// does and its wire recording matches the golden one. Each scenario gets a
// directory under Work holding both instances, their logs, every recorded
// exchange (exchanges.json) and the normalized wire recording (wire.jsonl).
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
		passed := t.run(sc.Run) && t.run(func(t *T) { t.settle(sc.IncomingErrors) })
		t.A.Stop()
		t.B.Stop()
		t.Proxy.Close()
		exchanges := t.Proxy.Exchanges()
		if err := writeJSON(filepath.Join(dir, "exchanges.json"), exchanges); err != nil {
			t.Logf("writing exchanges: %v", err)
		}
		records := normalize(exchanges, instanceKeys(dir, "a", "b"))
		if sc.Unstable != nil {
			records = slices.DeleteFunc(records, sc.Unstable)
		}
		wire := canonical(records, "a", "b")
		if err := writeLines(filepath.Join(dir, "wire.jsonl"), wire); err != nil {
			t.Logf("writing wire recording: %v", err)
		}
		if passed {
			passed = t.checkWire(opts, sc.Name, wire)
		}
		results = append(results, Result{Name: sc.Name, Passed: passed, Log: t.log, Duration: time.Since(start)})
	}
	return results
}

// checkWire compares a scenario's wire recording to the golden one, or
// replaces the golden one when updating.
func (t *T) checkWire(opts Options, name string, wire []string) bool {
	golden := filepath.Join(opts.Golden, name+".jsonl")
	if opts.UpdateGolden {
		if err := writeLines(golden, wire); err != nil {
			t.Logf("FAIL: writing golden recording: %v", err)
			return false
		}
		return true
	}
	b, err := os.ReadFile(golden)
	if err != nil {
		t.Logf("FAIL: no golden wire recording: %v", err)
		return false
	}
	var want []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			want = append(want, l)
		}
	}
	missing, extra := diffWire(want, wire)
	if len(missing) == 0 && len(extra) == 0 {
		return true
	}
	t.Logf("FAIL: wire differs from %s (full recording: wire.jsonl)", golden)
	for _, l := range missing {
		t.Logf("  - %s", truncate(l))
	}
	for _, l := range extra {
		t.Logf("  + %s", truncate(l))
	}
	return false
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// setUp starts a fresh pair for sc in dir. If anything fails, it stops what
// it started.
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

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeLines(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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

// settle waits until the pair is quiet, so the wire recording holds
// everything the scenario caused, however fast the workers run: every
// delivery sent (or given up on), every incoming activity tried, no fake
// sending. No scenario expects a delivery to fail: one that fails keeps the
// pair unsettled until its retry succeeds, and the extra attempt shows on
// the wire. Incoming activities that fail must number exactly what the
// scenario expects (Scenario.IncomingErrors).
//
// The instances and fakes can't be read at one instant: a delivery can land
// on a after a was read and be marked sent before b is. So the pair counts
// as quiet only when two polls in a row find nothing pending and the same
// state (the newest queue rows, how many are done, how many Accepts the
// fakes started): anything that moved in between changes it.
func (t *T) settle(expectedErrors int) {
	var last string
	t.Eventually("pending deliveries and incoming activities", func() error {
		var state strings.Builder
		for _, i := range []Instance{t.A, t.B} {
			row, err := Query(i, `SELECT
				(SELECT count(*) FROM outgoing_activity WHERE NOT is_sent AND NOT is_errored) AS outgoing,
				(SELECT count(*) FROM incoming_activity WHERE NOT is_processed AND NOT is_errored AND error IS NULL) AS incoming,
				(SELECT coalesce(max(id), 0) FROM outgoing_activity) AS last_outgoing,
				(SELECT coalesce(max(id), 0) FROM incoming_activity) AS last_incoming,
				(SELECT count(*) FROM outgoing_activity WHERE is_sent OR is_errored) AS sent,
				(SELECT count(*) FROM incoming_activity WHERE is_processed OR is_errored OR error IS NOT NULL) AS tried`)
			if err != nil {
				return err
			}
			r := row[0]
			if r["outgoing"] != int64(0) || r["incoming"] != int64(0) {
				last = ""
				return fmt.Errorf("%s has %v deliveries and %v incoming activities pending", i.Name(), r["outgoing"], r["incoming"])
			}
			fmt.Fprintf(&state, "%s:%v/%v/%v/%v ", i.Name(), r["last_outgoing"], r["last_incoming"], r["sent"], r["tried"])
		}
		for _, f := range t.fakes {
			if n := f.sending.Load(); n > 0 {
				last = ""
				return fmt.Errorf("%s is sending %d Accepts", f.Name, n)
			}
			fmt.Fprintf(&state, "%s:%d ", f.Name, f.accepts.Load())
		}
		if state.String() != last {
			last = state.String()
			return fmt.Errorf("not quiet yet: %s", last)
		}
		return nil
	})
	var errors int
	for _, i := range []Instance{t.A, t.B} {
		errors += len(t.rows(i, `SELECT 1 FROM incoming_activity WHERE error IS NOT NULL`))
	}
	if errors != expectedErrors {
		t.Fatalf("%d incoming activities failed, want %d (see the instances' incoming.log)", errors, expectedErrors)
	}
}
