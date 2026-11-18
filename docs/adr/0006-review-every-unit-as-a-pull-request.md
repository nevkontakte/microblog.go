# 6. Review every unit of work as a pull request

Date: 2026-11-18

## Status

Accepted

## Context

The owner reviews all changes and wants a history that reads well later. Agents work in
parallel and must not damage each other's work or the upstream project. `tsileo/microblog.pub`
is a third-party repository.

## Decision

- Each self-contained unit of work is one PR against `main`, reviewed by the owner.
  `main` is protected and advances only by PR. GitHub's PR merge commits are fine.
- Branches are never merged into each other locally. PR branches are rebased onto `main`
  when it moves, and pushed with `--force-with-lease`.
- Review feedback is addressed by amending the commits it concerns, not with fixup
  commits.
- Nothing is ever committed, pushed or filed against `tsileo/microblog.pub`.
- These rules are mirrored in `CLAUDE.md` and partly enforced by `.claude/settings.json`,
  which denies direct pushes to `main`, plain force pushes, `git merge`, and edits under
  `reference/microblog.pub/`.

## Consequences

- Every change has a reviewed, readable trail. Gates are presented as PRs.
- Work that depends on an unmerged PR is stacked on its branch and rebased onto `main`
  after the base merges. It is never merged across branches.
- The orchestrating session needs commit/push rights, so the kit's blanket git denies
  are relaxed. Loop agents use isolated worktrees instead.
