# JSON-LD contexts

Captured 2026-11-19 so that LD signatures are computed from identical bytes on
every run, offline (see the verdict in `migration/00-feasibility-report.md`).
The judge proxy serves them at the URLs the Python original fetches
(`app/ldsig.py:21-36`):

| File | Served at | Fetched from |
|---|---|---|
| `activitystreams.jsonld` | `https://www.w3.org/ns/activitystreams` | same |
| `security-v1.jsonld` | `https://w3id.org/security/v1` | redirect to `https://w3c-ccg.github.io/security-vocab/contexts/security-v1.jsonld` |
| `identity-v1.jsonld` | `https://w3id.org/identity/v1` and `https://raw.githubusercontent.com/web-payments/web-payments.org/master/contexts/identity-v1.jsonld` | the latter (the original rewrites the former to it) |
