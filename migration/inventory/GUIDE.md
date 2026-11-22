# Gap inventory: guide for classifiers and reviewers

The inventory (`migration/inventory.tsv`) has one row for each site in
`migration/inventory/sites.tsv`. Implementers grep it before they translate a file. Read
`migration/RULEBOOK.md` first: a row records what the rulebook's defaults don't decide, or
confirms that a default applies to this site.

## Row format

Tab-separated, seven columns, no tabs or newlines inside a field:

| Column | Content |
|---|---|
| `file` | the source file, as in `sites.tsv` |
| `symbol` | the enclosing function or class, as in `sites.tsv` |
| `source_construct` | `L<line>: ` and the construct, for example `L196: raise ObjectIsGoneError(url, resp)`. The line makes the key unique. |
| `classification` | one value from the category's list below, or `UNKNOWN` |
| `target_translation` | the Go translation that an implementer types, specific to this site |
| `evidence` | `file:line` citations: where the value is created, where it changes, where it escapes. For a rulebook default, the line that shows the default applies. |
| `status` | `proposed` (classifier), then `confirmed`, `revised` or `unknown` |

Paths in `evidence` are relative to `reference/microblog.pub`. Cite lines that you opened.
Write plain, short English.

## Categories

### NONE: a value that can be `None`

- `POINTER`: Go `*T`, `nil` for `None`. Use it when the value is a struct, or when a
  scalar's absence differs from its zero value at some use.
- `ZERO_VALUE`: Go `T`, with the zero value standing for `None`. Only when every use
  treats `None` and the zero value the same (cite the uses).
- `OK_RESULT`: a function that returns `None` for "not found": Go `(T, bool)` or `*T`.
  Say which.
- `NULL_COLUMN`: a nullable model column: `*T` in the row struct (rulebook §2).
- `DEFAULT_ARG`: a parameter that defaults to `None`: name what the callers pass.

### EXCEPT: an `except` clause or a `raise`

- `ERROR_TYPE`: a raise of a named exception. Name the Go error type and its parent
  (rulebook §2, the error rows).
- `ERROR_MATCH`: an `except X`: the `errors.As`/`errors.Is` test, and what each caught
  type covers.
- `CATCH_ALL`: `except Exception`: list what the `try` block can raise in Python that Go
  returns as an error, and what can panic (type assertion, nil map, index). The Go code
  checks each.
- `HTTP_ERROR`: `raise HTTPException`: the status and detail, written by the handler
  wrapper.
- `ASSERT_PANIC`: `raise ValueError("Should never happen")` and other programming errors
  that the rulebook maps to `panic`.
- `BUG_CRASH`: a raise or a missing check that gives a 500 or a crash in the original on
  bad input: reproduce it, `BUG(port):`.

### ORM_WRITE: an attribute assignment on a row, or `session.add`

- `WRITE_THROUGH`: the rulebook default (§2, write-through): `session.Update` or
  `session.Insert` at this point. Name the columns.
- `WRITE_VISIBLE`: as `WRITE_THROUGH`, and a later query in the same session reads the
  change (cite it).
- `SHARED_OBJECT`: the same row is reached through another reference in the session
  (the identity map): say which struct to pass along.
- `NOT_A_ROW`: the assignment is on a plain object, not a database row.

### ROUTE: an HTTP route

- `ROUTE_PLAIN`: the rulebook defaults cover it. Name the method, path, middleware and
  the explicit calls.
- `ROUTE_IMPLICIT`: FastAPI does something that the Go code must write out: a 422 body
  for a missing or empty form or query field, a type conversion of a path parameter, a
  dependency order, a redirect quoting, a response class. Name each.

### DATETIME_FORMAT: a datetime formatted as text, or parsed from text

- `DATETIME_LAYOUT`: the exact Go layout, from Python's output (note: `isoformat` omits
  the fraction when the microseconds are 0, and naive DB values have no offset).
- `DATETIME_PARSE`: the forms that this caller receives (see the existing
  `parse_isoformat` row).

### EXACT_OUTPUT: library output that federates or is stored

- `EXACT_PORT`, `EXACT_LIBRARY`, `EXACT_TOLERATED` (only with an ADR, such as 0022).

### TEXT_INDEX: a slice of a value that can be text

- `ASCII_BYTES`: the text is ASCII by construction (IDs, hashes, URLs, prefixes): slice
  bytes.
- `RUNES`: the text can be any Unicode and the slice counts characters: use `[]rune`.

### DICT_ORDER: iteration over a dict

- `ORDER_MATTERS`: the order reaches output: name the Go structure that keeps it.
- `ORDER_IRRELEVANT`: cite why the order can't be seen.

### SESSION: a place that opens a database session

- `SESSION_PER_CALL` or `SESSION_LONG_LIVED`: the lifetime, the commit and rollback
  points, and any stale-object effect.

## UNKNOWN

When the source doesn't decide a row, classify it `UNKNOWN`. Give the most conservative
Go translation, with `TODO(port):` and the question, so that an implementer can still
port it.
