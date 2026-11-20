package judge

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"time"
)

// A Scenario is one interaction between two instances, "a" and "b", each
// started fresh for it.
type Scenario struct {
	Name string
	// Config holds extra profile.toml lines per instance name.
	Config map[string]string
	// IncomingErrors is how many incoming activities are expected to fail
	// processing, across both instances.
	IncomingErrors int
	// Unstable reports the wire records left out of the comparison: those
	// whose content depends on timing in the original. Each use says why.
	Unstable func(WireRecord) bool
	Run      func(t *T)
}

// T is a scenario's handle on the running pair, in the style of testing.T.
type T struct {
	Ctx    context.Context
	A, B   Instance
	Client *Client
	Proxy  *Proxy

	impls map[string]Impl // by instance name
	specs map[string]Spec
	fakes []*Fake

	mu     sync.Mutex // guards failed and log, which fakes also write from their goroutines
	failed bool
	log    []string
}

func (t *T) Logf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = append(t.log, fmt.Sprintf(format, args...))
}

// Errorf records a failure and lets the scenario go on. Unlike Fatalf, any
// goroutine may call it.
func (t *T) Errorf(format string, args ...any) {
	t.Logf("FAIL: "+format, args...)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
}

// Fatalf records a failure and ends the scenario.
func (t *T) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	runtime.Goexit()
}

// Log returns what the scenario logged.
func (t *T) Log() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.log)
}

// Failed reports whether the scenario has failed.
func (t *T) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed
}

// Check ends the scenario if err is not nil.
func (t *T) Check(err error) {
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// Timeout bounds every wait: Eventually, an instance becoming ready, and
// each HTTP request the judge makes. It is a ceiling for a stuck run, not an
// expectation of how fast things are: scenarios wait on state, never on time,
// and a busy CI machine can be many times slower than a laptop.
var Timeout = 2 * time.Minute

// Eventually polls check until it returns nil, and fails the scenario with the
// last error if that doesn't happen in time.
func (t *T) Eventually(what string, check func() error) {
	deadline := time.Now().Add(Timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		select {
		case <-t.Ctx.Done():
			t.Fatalf("%s: %v", what, t.Ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// run executes the scenario body and reports whether it passed. The body
// runs in its own goroutine, because Fatalf ends it with runtime.Goexit.
func (t *T) run(f func(*T)) bool {
	var wg sync.WaitGroup
	wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		f(t)
	})
	wg.Wait()
	return !t.Failed()
}

// All returns every scenario, in a stable order.
func All() []Scenario {
	var all []Scenario
	for _, group := range [][]Scenario{
		FollowScenarios,
		InboxScenarios,
		OutboxScenarios,
		FederationScenarios,
		LookupScenarios,
		InboxEdgeScenarios,
	} {
		all = append(all, group...)
	}
	return all
}
