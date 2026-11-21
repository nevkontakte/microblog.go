// Command judge runs the parity scenarios against a pair of microblog.pub
// implementations and reports which pass.
//
// Usage:
//
//	go run ./cmd/judge -venv $(cd reference/microblog.pub && poetry env info -p)
//
// The judge is a program, not a `go test` suite, for two reasons:
//   - It controls external processes that run for a long time, and flags
//     select these processes.
//   - `go test` is only for the port's unit tests.
//
// See docs/adr/0013-run-the-judge-as-a-program.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/nevkontakte/microblog.go/internal/judge"
)

func main() {
	var (
		reference = flag.String("reference", "reference/microblog.pub", "checkout of the Python original")
		venv      = flag.String("venv", os.Getenv("MICROBLOGPUB_VENV"), "virtualenv with the original's dependencies (default $MICROBLOGPUB_VENV)")
		work      = flag.String("work", "_judge", "directory for instances, logs and recordings")
		run       = flag.String("run", "", "run only the scenarios that match this regexp")
		golden    = flag.String("golden", "internal/judge/testdata/golden", "directory of golden recordings")
		update    = flag.Bool("update-golden", false, "write the golden recordings, and don't compare with them")
		parallel  = flag.Int("parallel", runtime.NumCPU(), "the number of scenarios that play at the same time")
		patch     = flag.String("patch", "", "apply this patch to the copy of the Python original, to check that the judge finds it")
	)
	flag.DurationVar(&judge.Timeout, "timeout", judge.Timeout, "maximum time for each wait. Increase it on slow machines")
	flag.Parse()
	if *venv == "" {
		fatalf("-venv is required: run `poetry env info -p` in %s", *reference)
	}
	filter, err := regexp.Compile(*run)
	if err != nil {
		fatalf("-run: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var patches []string
	if *patch != "" {
		patches = append(patches, *patch)
	}
	py, err := judge.PreparePython(*reference, filepath.Join(*work, "python-app"), *venv, patches...)
	if err != nil {
		fatalf("preparing the Python original: %v", err)
	}
	var scenarios []judge.Scenario
	for _, s := range judge.All() {
		if filter.MatchString(s.Name) {
			scenarios = append(scenarios, s)
		}
	}
	results := judge.Run(ctx, judge.Options{
		A: py, B: py, Work: filepath.Join(*work, "py-py"),
		Golden: *golden, UpdateGolden: *update, Parallel: *parallel,
	}, scenarios)
	summary, ok := judge.Summary(results)
	fmt.Print(summary)
	if !ok {
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "judge: "+format+"\n", args...)
	os.Exit(2)
}
