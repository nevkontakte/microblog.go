// Command judge runs the parity scenarios against a pair of microblog.pub
// implementations and reports which pass.
//
// Usage:
//
//	go run ./cmd/judge -venv $(cd reference/microblog.pub && poetry env info -p)
//
// It is a program rather than a `go test` suite because it drives long-lived
// external processes chosen by flags, and because `go test` is reserved for
// the port's unit tests (see docs/adr/0013-run-the-judge-as-a-program.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"

	"github.com/nevkontakte/microblog.go/internal/judge"
)

func main() {
	var (
		reference = flag.String("reference", "reference/microblog.pub", "checkout of the Python original")
		venv      = flag.String("venv", os.Getenv("MICROBLOGPUB_VENV"), "virtualenv with the original's dependencies (default $MICROBLOGPUB_VENV)")
		work      = flag.String("work", "_judge", "directory for instances and their logs")
		run       = flag.String("run", "", "run only scenarios matching this regexp")
	)
	flag.DurationVar(&judge.Timeout, "timeout", judge.Timeout, "ceiling for each wait; raise it on slow machines")
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

	py, err := judge.PreparePython(*reference, filepath.Join(*work, "python-app"), *venv)
	if err != nil {
		fatalf("preparing the Python original: %v", err)
	}
	var scenarios []judge.Scenario
	for _, s := range judge.All() {
		if filter.MatchString(s.Name) {
			scenarios = append(scenarios, s)
		}
	}
	results := judge.Run(ctx, judge.Options{A: py, B: py, Work: filepath.Join(*work, "py-py")}, scenarios)
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
