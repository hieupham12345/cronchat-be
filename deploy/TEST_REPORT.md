# CronChat Backend — Test Report

Date: 2026-07-02 · Target: `http://56.10.41.218:5555` (AWS EC2 + RDS, ap-southeast-1)

## 1. Code-level tests (`go test -race -cover ./...`)
All packages pass, no data races.

| Package | Result | Coverage |
|---|---|---|
| internal/httpserver | ok | 7.2% |
| internal/chat | ok | 18.9% |
| internal/room | ok | 7.4% |
| internal/user | ok | 28.9% |

Covered: JWT round-trip, password hashing, path/ID parsing (incl. the `getIDFromURL`
regression), input validation, mime helpers, reply-preview/pickName, and repo
scan-logic via sqlmock. `go build`, `go vet`, `gofmt` all clean. Coverage is low
because most statements are DB/HTTP-bound and need a live MySQL — those are covered
by the live E2E suite below.

## 2. Live end-to-end tests (against deployed backend)
**Result: 15 / 16 checks pass. 1 minor bug found (non-blocking).**

| # | Check | Result |
|---|---|---|
| 1 | Login → accessToken + refresh cookie | ✅ |
| 2 | GET /me profile | ✅ |
| 3 | Auth guard (bad token → 401) | ✅ |
| 4 | Create user (new) | ✅ |
| 5 | User listing / search | ✅ |
| 6 | Create/get direct room | ✅ |
| 7 | Send message | ✅ |
| 8 | Get room messages (pagination) | ✅ |
| 9 | Toggle reaction | ✅ |
| 10 | Reaction summary `/messages/reactions/{id}` | ✅ |
| 11 | Mark seen `/rooms/seen` (affected:1) | ✅ |
| 12 | Unread counts | ✅ |
| 13 | **Regression** `/messages/seen/summary/{id}` (was broken pre-fix) → 200 | ✅ |
| 14 | Refresh access token via cookie | ✅ |
| 15 | WebSocket auth guard (no cookie → 401) | ✅ |
| 16 | Create **duplicate** user → should be 409 | ❌ returns 500 |

## 3. Bugs found & fixed DURING deployment testing
- **Missing system user (id=99999)** — the day-separator logic in
  `sp_send_message_with_day_sep` inserts rows with `sender_id=99999`; on a fresh DB
  this violated the `messages.sender_id → users.id` FK, so **every send-message
  failed** with "db error". Fixed: seeded the system user in RDS and added the seed
  to `database.sql`. Send-message now works. ✅
- Verified live that the two refactor bug-fixes hold in production:
  `/messages/seen/summary/{id}` returns 200 (was broken), and the seen→unread
  wiring updates correctly.

## 4. Open issue (minor, non-blocking)
- **Duplicate username returns HTTP 500 instead of 409.** The handler detects
  duplicates by matching the substring `"UNIQUE"` (SQLite wording), but MySQL reports
  `Error 1062 ... Duplicate entry`. Data integrity is fine (the unique key still
  blocks the insert) — only the status code/message are wrong. Fix: also match MySQL
  duplicate errors (e.g. `strings.Contains(err, "Duplicate entry")` or check error
  number 1062). ~1 line + redeploy.

## 5. Deployment health
- Container `cronchat-api-1`: Up, `MySQL connected`, listening on `:5555`.
- Reachable publicly over HTTP; RDS private (EC2 SG only).
- Cost: within AWS free tier (~$0/mo for 12 months).

## 6. Pre-production reminders (see FRONTEND_HANDOFF.md / README.md)
- **HTTP only** — login passwords travel unencrypted; add HTTPS (domain) before real
  external users.
- Refresh cookie is `SameSite=Lax; Secure=false` → cross-origin `/auth/refresh` and
  `/ws` won't receive it. FE must be same-origin (proxy) until HTTPS + `SameSite=None`.
- CORS currently open to all origins.
- RDS automated backups are OFF (free-plan restriction).
