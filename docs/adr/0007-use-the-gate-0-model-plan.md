# 7. Use the gate-0 model plan

Date: 2026-11-18

## Status

Accepted

## Context

Token spend concentrates in the translation loops. Errors in the rulebook or the judge
replicate into every translated file or verdict. The kit makes the model plan a human
decision at the feasibility gate.

## Decision

- **`claude-opus-5-5`:** rulebook authorship and amendments, the judge build (`00b`) and
  its reviewers, and skeptic reviewers for protocol and storage code (`httpsig`, `ldsig`,
  `models`, `boxes`, `indieauth`).
- **`claude-sonnet-5-5`:** other reviewers, translation implementers and fixers.
- Every subagent is given its model explicitly. Running on an untriggered default is a
  deviation and is logged.

## Consequences

- Most volume runs on the cheaper tier, and the expensive tier is spent where a mistake
  would multiply.
- Changing the plan needs a new record that supersedes this one.
