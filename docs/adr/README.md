# Architecture decision records

Decision ledger for microblog.go, in [Michael Nygard's format](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions).
How the ledger works is itself a decision: see [0001](0001-record-architecture-decisions.md).
To add one, copy [template.md](template.md) to the next number and add it to this list.

| # | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-follow-the-migration-kit-process.md) | Follow the migration kit process | Accepted |
| [0003](0003-keep-wire-and-disk-compatibility.md) | Keep wire and on-disk compatibility with microblog.pub | Accepted |
| [0004](0004-drop-python-customization-hooks.md) | Drop Python customization hooks | Accepted |
| [0005](0005-embed-templates-with-wholesale-override.md) | Embed templates with a wholesale on-disk override | Accepted |
| [0006](0006-review-every-unit-as-a-pull-request.md) | Review every unit of work as a pull request | Accepted |
| [0007](0007-use-the-gate-0-model-plan.md) | Use the gate-0 model plan | Accepted |
| [0008](0008-license-under-agpl-3.0.md) | License the port under AGPL-3.0 | Accepted |
| [0009](0009-translate-one-to-one-before-refactoring.md) | Translate one-to-one before refactoring | Accepted |
| [0010](0010-judge-federation-with-paired-instances.md) | Judge federation with paired instances | Accepted |
| [0011](0011-replace-scss-with-plain-css.md) | Replace SCSS with plain CSS | Accepted |
| [0012](0012-write-code-for-human-readers.md) | Write code for human readers | Accepted |
| [0013](0013-run-the-judge-as-a-program.md) | Run the judge as a program | Accepted |
| [0014](0014-record-the-mastodon-replay-set-from-a-real-server.md) | Record the Mastodon replay set from a real server | Accepted |
| [0015](0015-use-json-gold-for-ld-signatures.md) | Use json-gold for LD signatures | Accepted |
| [0016](0016-define-the-port-scope-and-layout.md) | Define the port's scope and package layout | Accepted |
| [0017](0017-access-sqlite-through-database-sql.md) | Access SQLite through database/sql | Accepted |
| [0018](0018-serve-http-with-the-standard-library.md) | Serve HTTP with the standard library | Accepted |
| [0019](0019-run-the-server-and-the-workers-in-one-process.md) | Run the server and the workers in one process | Accepted |
| [0020](0020-adopt-the-go-dependency-set.md) | Adopt the Go dependency set | Accepted |
| [0021](0021-render-markdown-with-goldmark.md) | Render Markdown with goldmark | Accepted |
| [0022](0022-allow-lossy-image-re-encodes-to-differ.md) | Allow lossy image re-encodes to differ | Accepted |
| [0023](0023-store-go-error-text-instead-of-tracebacks.md) | Store Go error text instead of tracebacks | Accepted |
