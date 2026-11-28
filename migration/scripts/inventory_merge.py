#!/usr/bin/env python3
"""Merges the reviewed inventory shards into migration/inventory.tsv (prompt 02).

Usage: python3 -I migration/scripts/inventory_merge.py

Reads every shard migration/inventory/shards/inventory.<batch>.tsv (the first 20
rows are the shard inventory.first-20.tsv). Renames the classifications that a
classifier spelled differently from GUIDE.md, drops exact duplicates on (file, symbol,
source_construct), sorts by file, then by line (rows without one first), and writes
migration/inventory.tsv. Fails if a row has the wrong column count, if one key has two
different rows, or if a classification or status is outside the guide's lists.
"""
import re
import sys
from pathlib import Path

MIGRATION = Path("migration")
HEADER = "file\tsymbol\tsource_construct\tclassification\ttarget_translation\tevidence\tstatus"

RENAMES = {
    "ORM_WRITE_THROUGH": "WRITE_THROUGH",
    "ORM_WRITE_VISIBLE": "WRITE_VISIBLE",
    "DICT_ORDER_MATTERS": "ORDER_MATTERS",
    "DICT_ORDER_IRRELEVANT": "ORDER_IRRELEVANT",
}

CLASSIFICATIONS = {
    "POINTER", "ZERO_VALUE", "OK_RESULT", "NULL_COLUMN", "DEFAULT_ARG",
    "ERROR_TYPE", "ERROR_MATCH", "CATCH_ALL", "HTTP_ERROR", "ASSERT_PANIC", "BUG_CRASH",
    "WRITE_THROUGH", "WRITE_VISIBLE", "SHARED_OBJECT", "NOT_A_ROW",
    "ROUTE_PLAIN", "ROUTE_IMPLICIT",
    "DATETIME_LAYOUT", "DATETIME_PARSE",
    "EXACT_PORT", "EXACT_LIBRARY", "EXACT_TOLERATED",
    "ASCII_BYTES", "RUNES",
    "ORDER_MATTERS", "ORDER_IRRELEVANT",
    "SESSION_PER_CALL", "SESSION_LONG_LIVED",
    "UNKNOWN",
}

STATUSES = {"confirmed", "unknown"}


def rows(path):
    lines = path.read_text().splitlines()
    if lines[0] != HEADER:
        sys.exit(f"{path}: unexpected header")
    for n, line in enumerate(lines[1:], start=2):
        fields = line.split("\t")
        if len(fields) != 7:
            sys.exit(f"{path}:{n}: {len(fields)} columns")
        fields[3] = RENAMES.get(fields[3], fields[3])
        if fields[3] not in CLASSIFICATIONS:
            sys.exit(f"{path}:{n}: classification {fields[3]!r}")
        if fields[6] not in STATUSES:
            sys.exit(f"{path}:{n}: status {fields[6]!r}")
        yield fields


def line_number(construct):
    """Returns the construct's line, or 0 for the first rows, which name none."""
    match = re.match(r"L(\d+):", construct)
    return int(match.group(1)) if match else 0


def main():
    paths = sorted((MIGRATION / "inventory" / "shards").glob("inventory.*.tsv"))
    merged = {}
    for path in paths:
        for fields in rows(path):
            key = tuple(fields[:3])
            if key in merged and merged[key] != fields:
                sys.exit(f"{path}: two different rows for {key}")
            merged[key] = fields
    ordered = sorted(merged.values(), key=lambda f: (f[0], line_number(f[2]), f[1], f[2]))
    text = HEADER + "\n" + "".join("\t".join(f) + "\n" for f in ordered)
    (MIGRATION / "inventory.tsv").write_text(text)
    print(f"read {len(paths)} files, wrote {len(ordered)} rows")


if __name__ == "__main__":
    main()
