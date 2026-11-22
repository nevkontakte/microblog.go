# 18. Serve HTTP with the standard library

Date: 2026-11-22

## Status

Accepted

## Context

The original's HTTP server is FastAPI on Starlette and uvicorn (`app/main.py`,
`app/admin.py`, `app/indieauth.py`, `app/micropub.py`, …). Its HTTP client is httpx, with
HTTP/2 enabled. Every public URL, status code, header and body that these produce or send
is part of the wire format ([0003](0003-keep-wire-and-disk-compatibility.md)).

Since Go 1.22, `http.ServeMux` matches methods and path patterns with wildcards
(`GET /o/{public_id}`). That covers the original's routes. The routes use FastAPI's
dependency injection (`Depends`) for the database session, the admin check, the CSRF
check and the HTTP signature check. They also use its form and JSON parsing.

A Go web framework (chi, echo, gin) adds routing features that the original doesn't need,
and middleware conventions that differ from FastAPI's. A translation would map FastAPI's
behavior onto a second framework's behavior, and a reviewer would need to know both.

## Decision

- **Server:** `net/http` with `http.ServeMux`.
  - Each route is a method on `*web.Server`, registered in `internal/web/main.go` with
    the original's method and path.
  - Cross-cutting behavior follows the standard library's middleware pattern: a
    function wraps one `http.Handler` in another, for example
    `middleware.Cached(server.ServeImage)`. A dependency that only lets a request
    through or refuses it (the admin check, the CSRF check, the HTTP signature check)
    becomes middleware. A dependency that gives the handler a value (the database
    session) becomes an explicit call at the top of the handler. The rulebook lists each
    dependency.
  - Errors return an `HTTPError`, and one wrapper writes the status and body that FastAPI
    writes for it.
  - FastAPI's automatic behavior that clients can see, such as the 422 body for a missing
    form field and the 405 for a wrong method, is reproduced where the judge or a client
    can reach it. The gap inventory lists each case.
- **Client:** one `net/http` client, built in `internal/app/activitypub.go`.
  - Its timeouts and redirect policy equal the original's httpx settings at each call
    site.
  - HTTP/2 comes from the standard library's TLS transport.
- No web framework, router library or middleware library. The middleware is
  hand-written, in `internal/web/middleware`.

## Consequences

- The routing code reads as a list of `mux.HandleFunc` calls, one for each FastAPI route.
- FastAPI's implicit behavior becomes explicit code. That is more code, but each piece is
  visible and reviewable.
- Path matching rules differ between Starlette and `ServeMux`, for example on trailing
  slashes and on encoded characters. The inventory records each route where they could
  differ, and the judge's public-surface scenarios check them.
