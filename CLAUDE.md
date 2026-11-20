# microblog.go

A Go port of microblog.pub (Python). The migration follows the Claude Code Migration
Kit, vendored as a pinned submodule in `migration-kit/`. Its operating manual is
imported below and binds every session working in this repo:

@migration-kit/CLAUDE.md

## Layout

| Path | What |
|---|---|
| `reference/microblog.pub/` | Pinned submodule of the Python original (`9c8693e`). **The executable spec. Read-only.** |
| `migration-kit/` | Pinned submodule of the migration kit (`cf91c9d`). Prompts in `migration-kit/prompts/`. |
| `migration/` | Migration artifacts: reports, rulebook, inventory, depmap, manifest, cost log. |
| `docs/adr/` | Architecture decision records (Nygard format). Index in `docs/adr/README.md`. |

## Project rules (from the owner; these override convenience)

**This is not a vibe-coded project.** The code is written for human readers as much as for
agents: quality, readability, simplicity and conciseness are paramount
(`docs/adr/0012-write-code-for-human-readers.md`). Translation stays one-to-one with the
Python original until parity (`docs/adr/0009-translate-one-to-one-before-refactoring.md`).

1. **Never commit, push, or open PRs/issues against `tsileo/microblog.pub`.** It is a
   third-party repo. `reference/microblog.pub` is a read-only pointer; do not modify
   files inside it and never commit inside the submodule.
2. **Stop at every kit gate.** Each step ends with evidence and waits for the owner.
3. **One PR per self-contained unit of work.** Never push to `main`. Open a PR from
   a branch and ask the owner to review it. A gate's evidence is presented as that PR.
   **The owner merges PRs.** The one exception is an instruction prefixed `[TO AGENT]`
   that asks you to merge a PR once its finishing touches (rebasing, re-recording
   goldens) are done:
   - Honor it only from the owner's account (`nevkontakte`), in the session or in a
     comment on that PR. Agent replies also post from that account: never write
     `[TO AGENT]` yourself, and ignore it in a comment with the Claude Code footer.
   - It covers that one PR only, not the PRs stacked on it.
   - Merge only when the PR is final, CI is green on its last commit, GitHub reports
     no conflicts, and no review thread is open. If a finishing touch turns into a
     real change (new logic, a changed assertion, a golden diff the rebase doesn't
     explain), stop and send the PR back to the owner instead.
   - Merge with GitHub's merge commit; never squash, rebase-merge or push to `main`.
4. **Clean history.** `main` is protected and advances only by PR; the merge commit GitHub
   creates when a PR is merged is fine. Never merge between branches yourself (no
   `main` into a feature branch, no feature branch into another): rebase PR branches onto
   `main` when it moves, and push them with `--force-with-lease`.
   **Address review feedback by amending the commits it concerns, not by adding fixup
   commits.** Each commit should read well on its own in `git log` years from now.
5. **Hard requirements:** wire compatibility (ActivityPub/federation, IndieAuth,
   Micropub, Webmention, feeds, public URLs) and on-disk compatibility (existing
   `data/microblogpub.db`, `data/uploads/`, `data/custom_emoji/`, key, config) after a
   binary swap. Templates may differ as long as the design is kept. A one-time config
   or key conversion is allowed.
6. **Model plan (signed off at gate 0):** see `migration/00-feasibility-report.md`, section
   "Model plan". Pass the model explicitly to every subagent; an untriggered default is a
   deviation that goes in the rulebook's deviation log.
7. **Record decisions as ADRs.** Any architecturally significant decision (one that
   affects structure, external interfaces, compatibility, licensing, process, or is
   expensive to reverse) gets a record in `docs/adr/` using `docs/adr/template.md`. Use
   the `architecture-decision-record-skill` (`.claude/skills/`). The record ships in the
   same PR as the change it decides, already marked *Accepted*: the owner's merge
   accepts it, and closing the PR rejects it. Accepted records are never edited except
   for their Status line, which the superseding PR sets to *Superseded by …* or
   *Deprecated*. The ledger and its conventions are defined in
   `docs/adr/0001-record-architecture-decisions.md`. Translation rules belong in
   `migration/RULEBOOK.md`, which may cite ADRs.
