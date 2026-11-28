#!/usr/bin/env python3
"""Collects the refuted rows of a review group, for the one revision (prompt 02).

Usage: python3 -I migration/scripts/inventory_refuted.py GROUP SHARD...

Reads migration/inventory/reviews/GROUP.r1.tsv and GROUP.r2.tsv, and writes, for each
shard, migration/inventory/reviews/SHARD.refuted.tsv: one line for each row that at
least one reviewer refuted, with both reviewers' verdicts and findings. Prints the
counts.
"""
import re
import sys
from pathlib import Path

REVIEWS = Path("migration/inventory/reviews")


def verdicts(path):
    out = {}
    for line in path.read_text().splitlines()[1:]:
        f = line.split("\t")
        if len(f) >= 4:
            out[(f[0], f[1])] = (f[2], f[3], f[4] if len(f) > 4 else "")
    return out


def main():
    group, shards = sys.argv[1], sys.argv[2:]
    r1 = verdicts(REVIEWS / f"{group}.r1.tsv")
    r2 = verdicts(REVIEWS / f"{group}.r2.tsv")
    for shard in shards:
        rows = Path(f"migration/inventory.{shard}.tsv").read_text().splitlines()[1:]
        out = ["file\tkey\tr1_verdict\tr1_cite\tr1_finding\tr2_verdict\tr2_cite\tr2_finding"]
        both = one = missing = 0
        for row in rows:
            f = row.split("\t")
            key = (f[0], re.match(r"(L\d+)", f[2]).group(1))
            a, b = r1.get(key), r2.get(key)
            if a is None or b is None:
                missing += 1
            refuted = [v for v in (a, b) if v and v[0] == "REFUTE"]
            if refuted:
                both += len(refuted) == 2
                one += len(refuted) == 1
                a, b = a or ("-", "", ""), b or ("-", "", "")
                out.append("\t".join([key[0], key[1], *a, *b]))
        (REVIEWS / f"{shard}.refuted.tsv").write_text("\n".join(out) + "\n")
        print(f"{shard}: rows={len(rows)} refuted_by_both={both} refuted_by_one={one} missing_verdicts={missing}")


if __name__ == "__main__":
    main()
