# 4. Drop Python customization hooks

Date: 2026-11-18

## Status

Accepted

## Context

microblog.pub loads user Python code from the data directory: `data/custom_routes.py`
(`app/config.py:231`, extra HTTP handlers and pages) and `data/stream.py`
(`app/config.py:283`, a custom stream visibility callback). A Go binary can't execute
these files. The only equivalents would be an embedded interpreter or a plugin system,
and neither is worth its cost for a single-user server.

## Decision

The Go port supports neither hook. Their presence in `data/` is ignored. The stream
always uses the default visibility rules.

## Consequences

- An instance that relies on either file loses that behavior after the swap. This is the
  only intended incompatibility in `data/` besides template overrides
  ([0005](0005-embed-templates-with-wholesale-override.md)).
- The port has no plugin API to design, secure or keep stable.
