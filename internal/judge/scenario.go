package judge

import (
	"context"
	"fmt"
	"runtime"
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

	impls  map[string]Impl // by instance name
	specs  map[string]Spec
	fakes  []*Fake
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

// run executes the scenario body and reports whether it passed.
func (t *T) run(f func(*T)) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Logf("PANIC: %v", r)
				t.failed = true
			}
		}()
		f(t)
	}()
	<-done
	return !t.failed
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
		EndpointScenarios,
		CLIScenarios,
	} {
		all = append(all, group...)
	}
	return all
}
