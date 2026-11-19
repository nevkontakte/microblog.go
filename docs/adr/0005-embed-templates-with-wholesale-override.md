# 5. Embed templates with a wholesale on-disk override

Date: 2026-11-18

## Status

Accepted

## Context

microblog.pub renders Jinja2 templates and lets users override any single template by
placing a file in `data/templates/` (`app/templates.py:42`). Go's `html/template` can't
read Jinja2, so existing overrides can't carry over in any case. The owner wants a
single self-contained binary.

## Decision

- Templates and static assets are embedded in the binary (`embed.FS`).
- A CLI subcommand dumps **all** embedded templates and static assets to a directory.
- A flag makes the server load **all** templates and static assets from that directory
  instead of the embedded copy.
- This is a wholesale switch: no per-file fallback to the embedded copy, and
  `data/templates/` is not read.

## Consequences

- The default deployment is one binary with no asset files to ship.
- Customizers own a full copy and must re-dump and merge it themselves after upgrades.
- A partial or stale directory fails visibly instead of silently mixing versions.
- Existing Jinja overrides must be rewritten by hand in Go template syntax.
