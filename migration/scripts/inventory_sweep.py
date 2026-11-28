#!/usr/bin/env python3
"""Lists every site of the Python → Go gaps in the translated files (step 1, prompt 02).

Usage (from the repository root):
  python3 -I migration/scripts/inventory_sweep.py > migration/inventory/sites.tsv

The script reads migration/depmap/layout.tsv for the translated files, parses each one
with ast, and prints one TSV row for each site: file, line, category, symbol (the
enclosing function or class), and the source text of the construct. The output is
deterministic. The categories are the gaps of migration/RULEBOOK.md §5:

  NONE         a function signature or model column that can hold None
  EXCEPT       an except clause, or a raise of a named exception
  DICT_ORDER   iteration over a dict (.items(), .keys(), .values())
  ORM_WRITE    an attribute assignment on a database row, or session.add()
  TEXT_INDEX   a slice of a value that can be text
  EXACT_OUTPUT a call to a library whose output must equal the original's byte for byte
  DATETIME_FORMAT  a datetime formatted as text or parsed from text
  ROUTE        an HTTP route, for FastAPI's implicit behavior
  SESSION      a place that opens a database session, for its lifetime and the
               identity map's stale objects
"""
import ast
import sys
from pathlib import Path

ROOT = Path("reference/microblog.pub")

# Calls to libraries whose output reaches the wire or the disk, and must be exact
# (RULEBOOK §1). Each entry is the call's text up to "(": a receiver and a name, or a
# bare name.
EXACT_CALLS = {
    "markdown", "markdownify", "Document", "renderer.render",  # mistletoe
    "get_lexer", "get_lexer_by_name",  # Pygments lexer aliases
    "blurhash.encode",
    "FeedGenerator", "fg.rss_str", "fg.atom_str",  # feedgen
    "session_serializer.dumps", "session_serializer.loads",  # itsdangerous
    "csrf_serializer.dumps", "csrf_serializer.loads",
    "hmac_sha256", "hmac.new",
}
DATETIME_CALLS = {"isoformat", "strftime", "strptime", "isoparse", "parse_isoformat", "fromisoformat"}
ROUTE_DECORATORS = {"get", "post", "put", "delete", "api_route", "head"}


def translated_files():
    rows = Path("migration/depmap/layout.tsv").read_text().splitlines()[1:]
    return [row.split("\t")[0] for row in rows if not row.endswith("\t-")]


def model_columns():
    """Returns the column names of the models in app/models.py."""
    tree = ast.parse((ROOT / "app/models.py").read_text())
    cols = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Assign, ast.AnnAssign)):
            value = node.value
            if isinstance(value, ast.Call) and getattr(value.func, "id", "") == "Column":
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                cols.update(t.id for t in targets if isinstance(t, ast.Name))
    return cols


def enclosing(parents, node):
    """Returns the dotted name of the functions and classes around node."""
    names = []
    while node in parents:
        node = parents[node]
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.append(node.name)
    return ".".join(reversed(names)) or "<module>"


def mentions_none(annotation):
    """Reports whether an annotation allows None as a value. A bare `-> None` means
    that the function returns nothing, so it doesn't count."""
    if annotation is None:
        return False
    text = ast.unparse(annotation)
    if text == "None":
        return False
    return "None" in text or "Optional" in text


def sites(path, columns):
    source = (ROOT / path).read_text()
    tree = ast.parse(source)
    parents = {child: node for node in ast.walk(tree) for child in ast.iter_child_nodes(node)}

    def row(node, category, text):
        return (path, node.lineno, category, enclosing(parents, node), " ".join(text.split())[:160])

    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            optional = [a.arg for a in node.args.args + node.args.kwonlyargs if mentions_none(a.annotation)]
            if mentions_none(node.returns) or optional:
                text = f"{node.name}({', '.join(optional)})" + (" -> None-able" if mentions_none(node.returns) else "")
                yield (path, node.lineno, "NONE", enclosing(parents, node) + "." + node.name if enclosing(parents, node) != "<module>" else node.name, text)
            for deco in node.decorator_list:
                call = deco.func if isinstance(deco, ast.Call) else deco
                if isinstance(call, ast.Attribute) and call.attr in ROUTE_DECORATORS:
                    yield (path, node.lineno, "ROUTE", node.name, ast.unparse(deco))
        elif isinstance(node, ast.Call) and getattr(node.func, "id", "") == "Column" and path == "app/models.py":
            if any(k.arg == "nullable" and getattr(k.value, "value", None) is True for k in node.keywords):
                yield row(node, "NONE", ast.unparse(node))
        elif isinstance(node, ast.withitem) and "async_session()" in ast.unparse(node.context_expr):
            yield row(node.context_expr, "SESSION", "async with " + ast.unparse(node))
        elif isinstance(node, ast.ExceptHandler):
            yield row(node, "EXCEPT", "except " + (ast.unparse(node.type) if node.type else ""))
        elif isinstance(node, ast.Raise) and node.exc is not None:
            yield row(node, "EXCEPT", "raise " + ast.unparse(node.exc))
        elif isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
            attr = node.func.attr
            if attr in ("items", "keys", "values") and not node.args and not node.keywords:
                yield row(node, "DICT_ORDER", ast.unparse(node))
            elif attr == "add" and ast.unparse(node.func.value).endswith("db_session"):
                yield row(node, "ORM_WRITE", ast.unparse(node))
            elif ast.unparse(node.func).split(".")[-2:] and ".".join(ast.unparse(node.func).split(".")[-2:]) in EXACT_CALLS:
                yield row(node, "EXACT_OUTPUT", ast.unparse(node))
            elif attr in DATETIME_CALLS:
                yield row(node, "DATETIME_FORMAT", ast.unparse(node))
        elif isinstance(node, ast.Call) and getattr(node.func, "id", "") in EXACT_CALLS:
            yield row(node, "EXACT_OUTPUT", ast.unparse(node))
        elif isinstance(node, ast.Call) and getattr(node.func, "id", "") in DATETIME_CALLS:
            yield row(node, "DATETIME_FORMAT", ast.unparse(node))
        elif isinstance(node, ast.Subscript) and isinstance(node.slice, ast.Slice):
            yield row(node, "TEXT_INDEX", ast.unparse(node))
        elif isinstance(node, (ast.Assign, ast.AugAssign)):
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            for target in targets:
                if isinstance(target, ast.Attribute) and target.attr in columns \
                        and not ast.unparse(target.value).startswith("self"):
                    yield row(node, "ORM_WRITE", ast.unparse(node))


def main():
    columns = model_columns()
    out = []
    for path in translated_files():
        out.extend(sites(path, columns))
    out = sorted(set(out), key=lambda r: (r[0], r[1], r[2], r[3], r[4]))
    w = sys.stdout.write
    w("file\tline\tcategory\tsymbol\tconstruct\n")
    for r in out:
        w("\t".join(str(x) for x in r) + "\n")
    counts = {}
    for r in out:
        counts[r[2]] = counts.get(r[2], 0) + 1
    print(" ".join(f"{k}={v}" for k, v in sorted(counts.items())) + f" total={len(out)}", file=sys.stderr)


if __name__ == "__main__":
    main()
