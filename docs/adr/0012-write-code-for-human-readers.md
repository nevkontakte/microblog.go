# 12. Write code for human readers

Date: 2026-11-19

## Status

Accepted

## Context

Most of the code in this repository will be written by agents, but the owner will read,
maintain and extend it by hand for years. That is the reason for moving to Go in the first
place. Code that merely works but is verbose, clever or inconsistent defeats that purpose,
even if every test passes.

## Decision

This is not a vibe-coded project. Code is written for human readers first:

- **Quality:** correct and tested, errors handled explicitly, no dead code, no speculative
  abstractions.
- **Readability:** plain Go and standard-library patterns, clear names, comments that explain
  *why*, not *what*.
- **Simplicity:** the simplest construct that does the job, and no new dependency where the
  standard library suffices.
- **Conciseness:** no boilerplate, no repetition that a small helper would remove, and no
  generated filler.

Reviewers apply this bar to every PR alongside correctness. Within the structure fixed by
[0009](0009-translate-one-to-one-before-refactoring.md), each function body must meet it.

## Consequences

- Agent output is reviewed for readability as well as behavior, which adds review time and
  occasional rework.
- Code that transliterates Python idioms into awkward Go is a review finding, even when it is
  behaviorally correct.
