# microblog.go

A Go port of microblog.pub (Python).

## Project rules (from the owner; these override convenience)

1. **Record decisions as ADRs.** Any architecturally significant decision (one that
   affects structure, external interfaces, compatibility, licensing, process, or is
   expensive to reverse) gets a record in `docs/adr/` using `docs/adr/template.md`. Use
   the `architecture-decision-record-skill` (`.claude/skills/`). The record ships in the
   same PR as the change it decides, already marked *Accepted*: the owner's merge
   accepts it, and closing the PR rejects it.
   Accepted records are never edited except for their Status line, which the
   superseding PR sets to *Superseded by …* or *Deprecated*. The ledger and
   its conventions are defined in `docs/adr/0001-record-architecture-decisions.md`.
