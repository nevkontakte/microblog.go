# 9. Translate one-to-one before refactoring

Date: 2026-11-19

## Status

Accepted

## Context

The port must behave exactly like microblog.pub ([0003](0003-keep-wire-and-disk-compatibility.md)),
and the judge compares the two implementations. When the Go code has the same shape as the
Python code, a divergence can be traced to a specific function. Redesigning while
translating makes every difference ambiguous: is it a bug, or an intended change? The
owner also wants clean architecture eventually, and that is far easier to reach from a
working, tested port than during the translation.

## Decision

- Translate one-to-one wherever possible. Each Python module becomes one Go file, each
  function becomes one Go function, and names and control flow stay recognizable, even
  where a Go-native design would be structured differently.
- Deviate only where Go or the libraries force it: the seams named in the feasibility
  report (ORM to SQL, Jinja to `html/template`, FastAPI to `net/http`), and the rules the
  rulebook decides. Mark each deviation in place.
- One-to-one applies to *structure*, not to *style*. Function bodies are written as clear,
  plain Go ([0012](0012-write-code-for-human-readers.md)); Python idioms are not mimicked
  where Go has a direct, simpler equivalent.
- Architectural cleanup is deferred until after the parity gate. Each cleanup is its own
  PR, with its own ADR where significant, and proved by the judge.

## Consequences

- Review and debugging map Go to Python line by line. Judge failures point at one function.
- The first version of the Go code will have some Python-shaped architecture (for example,
  a single package and module-level state), and that is intentional for now.
- A refactoring backlog builds up. It is tracked with `TODO(port)` markers and handled by
  kit prompt `06-post-parity.md`.
