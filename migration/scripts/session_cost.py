#!/usr/bin/env python3
"""Computes the cost-log columns of each gate from a Claude Code session transcript.

Usage:
  python3 -I migration/scripts/session_cost.py ~/.claude/projects/<project>/<session-id> \\
      migration/cost-log.tsv

The first argument is the transcript without ".jsonl". Its subagent transcripts are in
the directory of the same name. The script reads the gate names and times from the cost
log, and prints for each gate: active minutes, tokens, subagents spawned, and the cost
in USD. A gate's phase starts at the previous gate's time.

- Tokens: input, output, cache reads and cache writes of every API response in the
  phase, the subagents' responses included.
- Active minutes: the sum of the pauses between transcript events that are at most
  15 minutes long. A longer pause is idle time, for example a wait for the owner.
- Cost: the tokens at the RATES below. The Opus rates reproduce the client's own cost
  tracker ("cost-state" records) exactly. The Sonnet and Haiku rates assume the same
  ratios.
"""
import json, sys, glob
from datetime import datetime, timezone
D = sys.argv[1]
GAP = 15 * 60  # a pause longer than this is idle time
RATES = {  # USD per token: input, output, cache read, cache write 5m, cache write 1h
    "opus": (4e-6, 20e-6, 0.2e-6, 5e-6, 8e-6),
    "sonnet": (2e-6, 10e-6, 0.1e-6, 2.5e-6, 4e-6),
    "haiku": (1e-6, 5e-6, 0.1e-6, 1.25e-6, 2e-6),
}
GATES = [tuple(line.split("\t")[:2]) for line in open(sys.argv[2]).read().splitlines()[1:] if line]
ts = lambda s: datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
ends = [(name, ts(t)) for name, t in GATES]

def phase(t):
    for name, end in ends:
        if t <= end + 59:  # gate times have minute precision
            return name
    return None

def messages(path):
    seen = {}
    times = []
    for line in open(path):
        d = json.loads(line)
        if "timestamp" in d:
            times.append(ts(d["timestamp"]))
        m = d.get("message") if d.get("type") == "assistant" else None
        if m and m.get("usage") and m.get("id"):
            seen[m["id"]] = (ts(d["timestamp"]), m.get("model", ""), m["usage"])
    return list(seen.values()), times

tot = {n: dict(tokens=0, cost=0.0, agents=0) for n, _ in GATES}
times = []
main_msgs, t = messages(D + ".jsonl"); times += t
all_msgs = list(main_msgs)
for p in sorted(glob.glob(D + "/subagents/agent-*.jsonl")):
    msgs, t = messages(p); times += t
    all_msgs += msgs
    if t and phase(min(t)):
        tot[phase(min(t))]["agents"] += 1
for t0, model, u in all_msgs:
    n = phase(t0)
    if not n:
        continue
    cc = u.get("cache_creation") or {}
    w5, w1 = cc.get("ephemeral_5m_input_tokens", 0), cc.get("ephemeral_1h_input_tokens", 0)
    if not cc:
        w5 = u.get("cache_creation_input_tokens", 0)
    r = next(v for k, v in RATES.items() if k in model) if any(k in model for k in RATES) else RATES["opus"]
    counts = (u.get("input_tokens", 0), u.get("output_tokens", 0), u.get("cache_read_input_tokens", 0), w5, w1)
    tot[n]["tokens"] += sum(counts)
    tot[n]["cost"] += sum(a * b for a, b in zip(counts, r))
# Active time: the union of the pauses between events that are at most GAP long.
times.sort()
active = {n: 0.0 for n in tot}
for a, b in zip(times, times[1:]):
    if b - a <= GAP and phase(b):
        active[phase(b)] += b - a
for n, _ in GATES:
    v = tot[n]
    print(f"{n}\t{round(active[n]/60)}\t{v['tokens']}\t{v['agents']}\t${v['cost']:.2f}")
