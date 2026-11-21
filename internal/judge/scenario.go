package judge

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"time"
)

// A Scenario is one interaction between two instances, "a" and "b". Each
// scenario starts a new pair.
type Scenario struct {
	Name string
	// Config holds more profile.toml lines for each instance name.
	Config map[string]string
	// IncomingErrors is the number of incoming activities that must fail in
	// processing, on the two instances in total.
	IncomingErrors int
	// Unstable reports the wire records that the comparison ignores. These
	// are records whose content depends on timing in the original. Each
	// scenario that uses Unstable tells why.
	Unstable func(WireRecord) bool
	Run      func(t *T)
}

// T gives a scenario access to the running pair, as testing.T does for a
// test.
type T struct {
	Ctx    context.Context
	A, B   Instance
	Client *Client
	Proxy  *Proxy

	// Visitor is an anonymous browser or feed reader, without a session or
	// a signature. Its exchanges are on the wire with the tag "visitor". The
	// comparison uses the part of their bodies that asJSON keeps.
	Visitor *Client

	impls map[string]Impl // by instance name
	specs map[string]Spec
	fakes []*Fake

	mu     sync.Mutex // protects failed and log, because fakes also write them from their goroutines
	failed bool
	log    []string
}

func (t *T) Logf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = append(t.log, fmt.Sprintf(format, args...))
}

// Errorf records a failure, and the scenario continues. Unlike Fatalf, all
// goroutines can call it.
func (t *T) Errorf(format string, args ...any) {
	t.Logf("FAIL: "+format, args...)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
}

// Fatalf records a failure and stops the scenario.
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

// Failed reports whether the scenario failed.
func (t *T) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed
}

// Check stops the scenario if err is not nil.
func (t *T) Check(err error) {
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// Timeout is the maximum time of each wait:
//   - Eventually.
//   - The start of an instance.
//   - Each HTTP request of the judge.
//
// Timeout stops a run that can't continue. It doesn't tell how fast a
// correct run is. Scenarios wait for a state, never for a time, and a busy
// CI machine can be much slower than a laptop.
var Timeout = 2 * time.Minute

// Eventually calls check again and again until check returns nil. If Timeout
// passes first, Eventually fails the scenario with the last error.
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

// run runs the scenario body and reports whether it passed. The body runs in
// its own goroutine, because Fatalf stops the goroutine with
// runtime.Goexit.
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

// All returns all scenarios, in a fixed order.
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
		SwapScenarios,
		PublicScenarios,
		IndieAuthScenarios,
		MicropubScenarios,
		WebmentionScenarios,
		C2SScenarios,
		MastodonScenarios,
	} {
		all = append(all, group...)
	}
	return all
}
