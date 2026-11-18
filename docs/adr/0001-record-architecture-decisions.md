# 1. Record architecture decisions

Date: 2026-11-18

## Status

Accepted

## Context

This repository is a full rewrite of microblog.pub in Go, done in many steps and many
PRs, by agents working in separate sessions. Decisions get made in PR threads, chat and
gate reviews. Unless they are written down in one place, future sessions and future
readers can't tell what was decided, when, or why.

The migration kit already keeps a rulebook (`migration/RULEBOOK.md`). It decides
*how each construct translates*: storage formats, naming, error handling. It is not
meant to hold project-level choices such as licensing, the git workflow or which features
are dropped.

## Decision

We keep an Architecture Decision Record ledger in `docs/adr/`, using
[Michael Nygard's template](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions)
(`docs/adr/template.md`).

- Files are numbered and named `NNNN-present-tense-imperative.md`, and listed in
  `docs/adr/README.md`.
- One decision per record.
- Accepted records are immutable, with one exception: the **Status** line. When a
  decision changes, a new record supersedes the old one. In that same PR, the old record's
  Status becomes `Superseded by [NNNN](NNNN-title.md)` (or `Deprecated`), the new record
  says `Supersedes [NNNN](NNNN-title.md)`, and the index is updated. Nothing else in the
  old record changes, so a reader who lands on it still sees that it is out of date.
- A record is added in the same PR as the change it decides, with the status it will
  have once merged (normally **Accepted**). It is reviewed on the PR: merging the PR
  accepts it, and closing the PR rejects it. This means no follow-up edit is needed to
  flip a status after merge.
- The rulebook may cite ADRs; ADRs never contain translation rules.
- Sessions use the vendored `architecture-decision-record-skill`
  (`.claude/skills/`) when writing records.

## Consequences

- The "why" behind the port survives the sessions that made it.
- Every architecturally significant PR carries a little extra writing.
- Decisions made before this record (the gate-0 sign-off and the settings PR) are recorded
  in the step-0 PR, alongside the changes they decide, dated when they were made.
