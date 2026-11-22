#!/usr/bin/env python3
# Copyright 2026 Anthropic PBC
# SPDX-License-Identifier: Apache-2.0
"""Deterministic dependency mapper for Python codebases (stdlib only).

Implements the kit's dependency-map contract (prompts/01-dependency-map.md):
  - parses imports via ast (never regex, never agent judgment)
  - in-repo edges only: stdlib and third-party imports are neither edges nor misses
  - emits edges.tsv (one edge per line), order.txt (topological migration order,
    SCC-condensed), cycles.txt (every strongly connected component > 1 file)
  - all output is repo-relative FILE PATHS (e.g. toml/decoder.py), matching
    depmap_ts.mjs / depmap_c.py and the queue/manifest contract; dotted module
    names are used internally for import matching only

Usage:
  python3 depmap_python.py --root path/to/repo --out path/to/migration/depmap

microblog.go changes to the kit's copy (migration-kit/scripts/depmap_python.py):
  - --include limits the walk to the files that the port translates. The
    other files are out of scope (docs/adr/0016-define-the-port-scope-and-layout.md):
    they are neither nodes nor edges.
  - String literals that name an in-scope file ("scripts/config_wizard.py")
    or an entry point "module:attribute" ("app.main:app") are edges too. The
    original's tasks start these through a subprocess (tasks.py:100, 174),
    not through an import.
  - A pytest test file (test_*.py) depends on each conftest.py in its
    directory and the directories above it. pytest loads those fixtures
    without an import.
  - --layout names the Go file of each mapped file (migration/depmap/layout.tsv,
    "-" for an empty file). The script condenses the file graph to the Go
    packages, the directories of those Go files, and writes package_edges.tsv
    and package_cycles.txt, because the package graph is the level the Go
    compiler enforces. A *_test.go file is in its own node, "<dir> (test)",
    because it can be an external test package.
  - --drop lists file edges that the port doesn't keep (migration/depmap/dropped.tsv,
    with the reason for each). They stay in edges.tsv, which describes the
    source, but the package graph leaves them out.

Usage in this repository (from the repository root):
  python3 -I migration/scripts/depmap_python.py --root reference/microblog.pub \
      --out migration/depmap --layout migration/depmap/layout.tsv \
      --drop migration/depmap/dropped.tsv \
      $(cut -f1 migration/depmap/layout.tsv | tail -n +2 | sed 's/^/--include /')

Test: python3 scripts/depmap_python.py --root fixtures/python --out /tmp/depmap-py && diff /tmp/depmap-py/edges.tsv fixtures/python/expected_edges.tsv && diff /tmp/depmap-py/cycles.txt fixtures/python/expected_cycles.txt && diff /tmp/depmap-py/order.txt fixtures/python/expected_order.txt

Determinism: files walked in sorted order, edges and outputs sorted. Two runs
on the same tree produce byte-identical output.
"""

import argparse
import ast
import re
import sys
from pathlib import Path

# Directories never walked. Any path component in this set — or starting with
# "." — is excluded. Kept visible at the top per CONTRIBUTING's requirement
# that the exclusion list be inspectable without reading the walker.
SKIP_DIRS = {"venv", "node_modules", "__pycache__", "generated"}


def find_py_files(root: Path, include):
    """Returns the .py files under root. With include, only the files whose
    repo-relative path equals an include entry or is inside one."""
    def included(rel: str) -> bool:
        return not include or any(rel == i or rel.startswith(i.rstrip("/") + "/") for i in include)
    return sorted(
        p for p in root.rglob("*.py")
        if not any(part.startswith(".") or part in SKIP_DIRS
                   for part in p.relative_to(root).parts)
        and included(p.relative_to(root).as_posix())
    )


def package_of(path: str, layout):
    """Returns the Go package of a mapped file: the directory of its Go file,
    with " (test)" for a test file, or None for an empty file. Fails if the
    layout doesn't name the file."""
    if path not in layout:
        sys.exit(f"ERROR: the layout has no row for {path}")
    target = layout[path]
    if target == "-":
        return None
    pkg = target.rpartition("/")[0]
    return pkg + " (test)" if target.endswith("_test.go") else pkg


def condense(edges, nodes, layout, dropped):
    """Returns the package nodes and package edges of the file graph, without
    the dropped edges, the edges inside one package, and the empty files."""
    pkg_nodes = sorted({p for p in (package_of(n, layout) for n in nodes) if p})
    pkg_edges = set()
    for a, b in edges:
        pa, pb = package_of(a, layout), package_of(b, layout)
        if (a, b) not in dropped and pa and pb and pa != pb:
            pkg_edges.add((pa, pb))
    return pkg_nodes, sorted(pkg_edges)


def read_tsv(path):
    """Returns the rows of a TSV file with a header line, as lists of fields."""
    lines = Path(path).read_text(encoding="utf-8").splitlines()[1:]
    return [line.split("\t") for line in lines if line]


def module_name(path: Path, root: Path) -> str:
    rel = path.relative_to(root).with_suffix("")
    parts = list(rel.parts)
    if parts[-1] == "__init__":
        parts = parts[:-1]
    return ".".join(parts) if parts else "__init__"


def build_module_index(files, root):
    return {module_name(p, root): p for p in files}


def resolve_import(node, current_mod, is_pkg, index):
    """Yield in-repo module names imported by this ast node. Unknown names are
    silently skipped — third-party is neither an edge nor a miss."""
    if isinstance(node, ast.Import):
        for alias in node.names:
            name = alias.name
            while name:
                if name in index:
                    yield name
                    break
                name = name.rpartition(".")[0]
    elif isinstance(node, ast.ImportFrom):
        if node.level:  # relative import
            # Anchor package: for pkg/__init__.py the module IS the package,
            # so level 1 means pkg itself; for pkg/mod.py drop the module name
            # first so level 1 also lands on pkg.
            base = current_mod.split(".")
            if not is_pkg:
                base = base[:-1]
            base = base[: max(0, len(base) - (node.level - 1))]
            mod = ".".join(base + ([node.module] if node.module else []))
        else:
            mod = node.module or ""
        candidates = [mod] + [f"{mod}.{alias.name}" for alias in node.names if mod]
        for cand in candidates:
            name = cand
            while name:
                if name in index:
                    yield name
                    break
                name = name.rpartition(".")[0]


# A token in a string literal that can name a file or an entry point.
STRING_REF = re.compile(r"[A-Za-z0-9_./:]+")


def resolve_string(value, index, paths):
    """Yield in-repo module names that a string literal names: a repo-relative
    file path, or an entry point written as "module:attribute"."""
    for token in STRING_REF.findall(value):
        target = token.split(":")[0]
        if target in paths:
            yield paths[target]
        elif ":" in token and target in index:
            yield target


def extract_edges(files, root):
    """Return (sorted path edges, sorted path nodes). Import matching happens
    on dotted module names; edges are mapped back to repo-relative paths."""
    index = build_module_index(files, root)
    rel = {p: p.relative_to(root).as_posix() for p in files}
    paths = {rel[p]: module_name(p, root) for p in files}
    edges = set()
    for path in files:
        src_mod = module_name(path, root)
        is_pkg = path.name == "__init__.py"
        try:
            tree = ast.parse(path.read_text(encoding="utf-8", errors="replace"))
        except SyntaxError as e:
            print(f"WARN unparseable, no edges emitted: {path} ({e})", file=sys.stderr)
            continue
        for node in ast.walk(tree):
            deps = []
            if isinstance(node, (ast.Import, ast.ImportFrom)):
                deps = resolve_import(node, src_mod, is_pkg, index)
            elif isinstance(node, ast.Constant) and isinstance(node.value, str):
                deps = resolve_string(node.value, index, paths)
            for dep_mod in deps:
                if dep_mod != src_mod:
                    edges.add((rel[path], rel[index[dep_mod]]))
        if path.name.startswith("test_"):
            for parent in path.relative_to(root).parents:
                conftest = (parent / "conftest.py").as_posix()
                if conftest in paths:
                    edges.add((rel[path], conftest))
    return sorted(edges), sorted(rel.values())


def tarjan_scc(nodes, edges):
    """Iterative Tarjan. Returns list of SCCs (each a sorted list of nodes),
    in reverse topological order of the condensation."""
    adj = {n: [] for n in nodes}
    for a, b in edges:
        adj[a].append(b)
    for n in adj:
        adj[n].sort()
    index_counter = [0]
    stack, on_stack = [], set()
    index, lowlink = {}, {}
    sccs = []

    for start in sorted(nodes):
        if start in index:
            continue
        work = [(start, 0)]
        while work:
            node, pi = work[-1]
            if pi == 0:
                index[node] = lowlink[node] = index_counter[0]
                index_counter[0] += 1
                stack.append(node)
                on_stack.add(node)
            recurse = False
            for i in range(pi, len(adj[node])):
                succ = adj[node][i]
                if succ not in index:
                    work[-1] = (node, i + 1)
                    work.append((succ, 0))
                    recurse = True
                    break
                elif succ in on_stack:
                    lowlink[node] = min(lowlink[node], index[succ])
            if recurse:
                continue
            if lowlink[node] == index[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                sccs.append(sorted(scc))
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
    return sccs


def migration_order(nodes, edges):
    """Topological order over the SCC condensation: dependencies first.
    Returns (ordered list of SCCs, cycles)."""
    sccs = tarjan_scc(nodes, edges)
    # Tarjan emits SCCs in reverse topological order of the condensation
    # (a component is finished only after everything it depends on);
    # dependencies-first = the order Tarjan finished them.
    cycles = [s for s in sccs if len(s) > 1]
    return sccs, cycles


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--include", action="append", default=[],
                    help="repo-relative file or directory to map, repeatable (default: all)")
    ap.add_argument("--layout", type=Path,
                    help="TSV of source file and Go file, '-' for an empty file")
    ap.add_argument("--drop", type=Path,
                    help="TSV of file edges (from, to, reason) that the package graph leaves out")
    args = ap.parse_args()

    layout = {row[0]: row[1] for row in read_tsv(args.layout)} if args.layout else {}
    dropped = {(row[0], row[1]) for row in read_tsv(args.drop)} if args.drop else set()

    root = args.root.resolve()
    files = find_py_files(root, args.include)
    edges, nodes = extract_edges(files, root)
    order, cycles = migration_order(nodes, edges)

    args.out.mkdir(parents=True, exist_ok=True)
    with open(args.out / "edges.tsv", "w") as f:
        f.write("from\tto\n")
        for a, b in edges:
            f.write(f"{a}\t{b}\n")
    with open(args.out / "order.txt", "w") as f:
        f.write("# migration order: one batch per line, dependencies first\n")
        for scc in order:
            f.write("\t".join(scc) + "\n")
    with open(args.out / "cycles.txt", "w") as f:
        f.write(f"# strongly connected components > 1 file: {len(cycles)}\n")
        for scc in cycles:
            f.write("\t".join(scc) + "\n")

    print(f"files={len(files)} edges={len(edges)} batches={len(order)} cycles={len(cycles)}")
    if layout:
        unknown = sorted(dropped - set(edges))
        if unknown:
            sys.exit(f"ERROR: dropped edges that aren't in the map: {unknown}")
        pkg_nodes, pkg_edges = condense(edges, nodes, layout, dropped)
        _, pkg_cycles = migration_order(pkg_nodes, pkg_edges)
        with open(args.out / "package_edges.tsv", "w") as f:
            f.write("from\tto\n")
            for a, b in pkg_edges:
                f.write(f"{a}\t{b}\n")
        with open(args.out / "package_cycles.txt", "w") as f:
            f.write(f"# strongly connected components > 1 package: {len(pkg_cycles)}\n")
            for scc in pkg_cycles:
                f.write("\t".join(scc) + "\n")
        print(f"packages={len(pkg_nodes)} package_edges={len(pkg_edges)} package_cycles={len(pkg_cycles)}")
    print(f"wrote {args.out}/edges.tsv, order.txt, cycles.txt")
    if len(files) > 1 and not edges:
        print(
            "WARN: 0 edges across multiple files. If these files import each "
            "other, --root is probably pointing at the package directory "
            "instead of the repo root (imports like 'pkg.mod' can't resolve "
            "when the module index says 'mod').",
            file=sys.stderr,
        )


if __name__ == "__main__":
    main()
