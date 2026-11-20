package judge

import (
	"context"
	"fmt"
	"runtime"
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
	Run            func(t *T)
}

// T is a scenario's handle on the running pair, in the style of testing.T.
type T struct {
	Ctx    context.Context
	A, B   Instance
	Client *Client
	Proxy  *Proxy

	impls  map[string]Impl // by instance name
	specs  map[string]Spec
	failed bool
	log    []string
}

func (t *T) Logf(format string, args ...any) {
	t.log = append(t.log, fmt.Sprintf(format, args...))
}

// Fatalf records a failure and ends the scenario.
func (t *T) Fatalf(format string, args ...any) {
	t.Logf("FAIL: "+format, args...)
	t.failed = true
	runtime.Goexit()
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
				t.Logf("PANIC: %v", r)
				t.failed = true
			}
		}()
		f(t)
	})
	wg.Wait()
	return !t.failed
}

// All returns every scenario, in a stable order.
func All() []Scenario {
	var all []Scenario
	for _, group := range [][]Scenario{
		FollowScenarios,
	} {
		all = append(all, group...)
	}
	return all
}
