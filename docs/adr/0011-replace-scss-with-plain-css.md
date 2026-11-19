# 11. Replace SCSS with plain CSS

Date: 2026-11-19

## Status

Accepted

## Context

microblog.pub styles are written in SCSS and compiled with boussole/libsass
(`tasks.py:77`). Users theme an instance through `data/_theme.scss` (`tasks.py:87`). Go has no
maintained pure-Go Sass compiler, so keeping SCSS would mean shipping or requiring an external
`sass` binary. Modern CSS has custom properties, `calc()` and nesting, which cover what these
stylesheets use Sass for.

## Decision

- Port the stylesheets to plain CSS once, during the template work. Sass variables become CSS
  custom properties, and mixins and nesting become plain rules.
- The CSS is an embedded static asset like the others. Theming goes through the wholesale
  template and asset override of [0005](0005-embed-templates-with-wholesale-override.md):
  dump the assets and edit the custom properties.
- There is no Sass toolchain at build time or at runtime.

## Consequences

- One major dependency and one build step fewer. The binary stays self-contained.
- An existing `data/_theme.scss` is not read. Its variable overrides must be converted by hand
  into custom-property overrides in a dumped stylesheet. It is a one-time conversion, and
  the owner allowed for that.
- The rendered CSS may differ in detail from the compiled SCSS. Templates and styles only
  need to keep the general design ([0003](0003-keep-wire-and-disk-compatibility.md)).
