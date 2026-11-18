# 8. License the port under AGPL-3.0

Date: 2026-11-18

## Status

Accepted

## Context

microblog.pub is licensed AGPL-3.0. This repository is a translation of it: same
behavior, same structure, module by module. A translation is generally considered a
derivative work.

## Decision

The repository is licensed AGPL-3.0 (`LICENSE`, copied from microblog.pub). Vendored
third-party files keep their own licenses next to them, such as the ADR skill under
`.claude/skills/`, which is CC BY-NC-SA 4.0.

## Consequences

- Network users of a modified instance are entitled to its source, as with upstream.
- The port can't be relicensed more permissively without upstream's consent.
- The owner confirms this license by merging the PR that adds it. This record is not
  legal advice.
