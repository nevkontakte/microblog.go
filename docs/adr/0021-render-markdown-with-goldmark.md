# 21. Render Markdown with goldmark

Date: 2026-11-22

## Status

Accepted

## Context

The admin writes posts in Markdown. `app/source.py` renders them with mistletoe 0.9 and a
custom renderer (`CustomRenderer`), which adds mentions, hashtags, automatic links and
code fences. The HTML goes into the post's `content`, which federates and which the
original stores. The judge compares that content, so the port must produce the same HTML
for the same input.

These are the options:

1. Port mistletoe's tokenizers and HTML renderer to Go. This is faithful by construction,
   but it adds a third-party library's code (about 2,000 lines) to the translation.
2. goldmark, a CommonMark library with extension points for custom inline syntax and
   custom renderers. mistletoe 0.9 also follows CommonMark for most constructs. goldmark
   can reproduce `CustomRenderer`'s tokens through inline parsers and node renderers.
3. Another Go Markdown library (blackfriday, gomarkdown). They follow CommonMark less
   closely than goldmark, and offer weaker extension points.

## Decision

The port renders Markdown with `github.com/yuin/goldmark`.

- The mentions, hashtags, automatic links and code fences of `CustomRenderer` become
  goldmark extensions in `internal/app/source.go`.
- The HTML output must equal mistletoe's, byte for byte, for the inputs that the judge
  and the stress test use. That includes newlines and attribute order.
- The gap inventory records each known difference between mistletoe 0.9 and goldmark.
  Where goldmark can't match, the BUG rule or a small post-processing step reproduces
  mistletoe's output, with a `BUG(port):` or `TODO(port):` marker.
- If the stress test (prompt 03) finds differences that goldmark's extension points can't
  close, this decision is revisited in favor of option 1.

## Consequences

- The port uses a maintained library instead of carrying a Markdown parser.
- Fidelity becomes a testing problem. The stress test needs a corpus of Markdown inputs,
  rendered by the original, to compare against.
- Rare inputs where the two libraries differ can still slip through to production. The
  difference then shows as slightly different HTML in a federated post. Receivers render
  either version, so this affects appearance, not federation.
