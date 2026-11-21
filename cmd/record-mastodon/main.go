// Command record-mastodon records the Mastodon replay set: what a real
// Mastodon server sends to a microblog.pub instance. The judge replays the
// set to the two implementations
// (docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md).
//
// Usage:
//
//	go run ./cmd/record-mastodon -venv $(cd reference/microblog.pub && poetry env info -p)
//
// It needs a running Docker daemon. The replay set is in the repository.
// Run the command again only to record a new Mastodon release.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/nevkontakte/microblog.go/internal/judge"
)

func main() {
	var (
		reference = flag.String("reference", "reference/microblog.pub", "checkout of the Python original")
		venv      = flag.String("venv", os.Getenv("MICROBLOGPUB_VENV"), "virtualenv with the original's dependencies (default $MICROBLOGPUB_VENV)")
		work      = flag.String("work", "_judge", "directory for the instances and their logs")
		out       = flag.String("out", "internal/judge/testdata/mastodon.json", "the replay set to write")
	)
	flag.DurationVar(&judge.Timeout, "timeout", judge.Timeout, "maximum time for each wait")
	flag.Parse()
	if *venv == "" {
		fatalf("-venv is required: run `poetry env info -p` in %s", *reference)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	py, err := judge.PreparePython(*reference, filepath.Join(*work, "python-app"), *venv)
	if err != nil {
		fatalf("preparing the Python original: %v", err)
	}
	err = judge.RecordMastodon(ctx, judge.RecordOptions{Impl: py, Work: filepath.Join(*work, "mastodon"), Out: *out})
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "record-mastodon: "+format+"\n", args...)
	os.Exit(2)
}
