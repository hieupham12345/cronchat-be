# CronChat Backend — Frontend Integration Handoff

**Status:** Live on AWS (EC2 + RDS MySQL, ap-southeast-1). HTTP only (no TLS yet).

## Base URLs
| | |
|---|---|
| REST API | `http://56.10.41.218:5555` |
| WebSocket | `ws://56.10.41.218:5555/ws` |
| Static avatars | `http://56.10.41.218:5555/static/user_avatars/<file>` |
| Static chat images | `http://56.10.41.218:5555/static/chat_uploads/<file>` |

## Test account
- username: `admin`  ·  password: `<REDACTED — ask the backend owner>`  ·  role: `admin`

## Auth model
- **Login** returns an `accessToken` (JWT, **10 min** TTL) in the JSON body AND sets an
  **HttpOnly `refresh_token` cookie** (7-day TTL).
- Authenticated REST calls: header `Authorization: Bearer <accessToken>`.
- **Refresh:** `POST /auth/refresh` (sends the cookie) → new `accessToken`.
- **WebSocket auth uses the `refresh_token` COOKIE** (not a header/query param).

### ⚠️ CRITICAL cross-origin caveat (read this)
The refresh cookie is `SameSite=Lax; Secure=false`. Consequences:
1. **Bearer REST calls work from any origin** (CORS echoes the origin, allows
   credentials + `Authorization`). ✅
2. **`/auth/refresh` and `/ws` rely on the cookie**, and a `Lax` cookie is **NOT sent
   on cross-site** fetch/XHR/WebSocket. So if the FE is on a *different origin* than
   `56.10.41.218:5555`, refresh + realtime **will fail**. ❌
3. The backend is **HTTP** — an HTTPS frontend cannot call `http://`/`ws://` (mixed
   content blocked).

**Recommended FE setup during dev/test (pick one):**
- **Proxy approach (easiest):** run the FE dev server with a proxy so `/api/*` and
  `/ws` forward to `http://56.10.41.218:5555`. The browser then treats them as
  same-origin → the cookie flows, WS + refresh work. Serve the FE over **HTTP**.
- Or deploy FE behind the **same domain/reverse proxy** as the backend.

When a domain + HTTPS is added later, the backend cookie will switch to
`SameSite=None; Secure` and true cross-origin will work (ask backend to flip the flag).

## Endpoints

### Auth
| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/login` | `{username, password}` | → `{accessToken, id, username, full_name, ...}` + sets cookie |
| POST | `/auth/refresh` | – (cookie) | → `{accessToken}` |
| POST | `/logout` | – | clears cookie |

### User
| Method | Path | Body / Query | Auth |
|---|---|---|---|
| POST | `/create-user` | `{username, password(≥8), role("admin"\|"user"), full_name, email(valid), phone(0+9 digits)}` | none |
| GET | `/me` | – | Bearer |
| PUT | `/update-user` | `{password?, full_name?, email?, phone?, avatar_url?, is_active?}` | Bearer |
| PUT | `/update-password` | `{current_password, new_password}` | Bearer |
| GET | `/get-all-user-listing` | – | Bearer → `{id, username, full_name, avatar_url}[]` |
| GET | `/users/search` | `?q=<≥2 chars>&limit=` | Bearer |
| POST | `/users/avatar` | multipart `file=` | Bearer → `{avatar_url}` |
| GET | `/admin/get-all-user` | – | Bearer (admin) |

### Rooms
| Method | Path | Body / Query | Notes |
|---|---|---|---|
| GET | `/rooms` | – | my rooms (direct-room name = partner's name) |
| GET | `/rooms/messages/{roomID}` | `?limit=20&before_id=&before_at=<RFC3339>` | paginated; auto-marks seen |
| GET | `/rooms/direct/{targetUserID}` | – | **GET** creates/returns a 1-1 room |
| GET | `/rooms/direct-name/{roomID}` | – | partner full_name |
| POST | `/rooms/group` | `{name, member_ids:[...]}` | |
| POST | `/rooms/add-member` | `{room_id, user_ids:[...]}` | member-only |
| POST | `/rooms/read/{roomID}` | – | mark room read |
| GET | `/rooms/members/{roomID}` | – | |
| DELETE | `/rooms/{roomID}/members/{userID}` | – | owner only |
| DELETE | `/rooms/delete/{roomID}` | – | owner/member |
| POST | `/rooms/upload-image/{roomID}` | multipart `file=` | → `{media_url, mime, size}` |

### Messages / reactions / receipts
| Method | Path | Body / Query |
|---|---|---|
| POST | `/rooms/send-messages/{roomID}` | `{content, message_type:"text"\|"image"\|"file"\|"system", reply_to_message_id?}` |
| POST | `/messages/react/add` | `{message_id, reaction}` (toggle) |
| POST | `/messages/react/remove` | `{message_id, reaction?}` (empty reaction = remove all mine) |
| GET | `/messages/reactions/{messageID}` | – |
| POST | `/rooms/seen` | `{room_id, up_to_message_id}` |
| GET | `/rooms/last-seen/{roomID}` | – |
| GET | `/messages/seen/summary/{messageID}` | – |
| GET | `/messages/seen/users/{messageID}` | `?limit=50` |
| GET | `/rooms/unread-counts` | – → `{counts: {roomID: n}}` |
| GET | `/rooms/unread/{roomID}` | – |

### WebSocket `/ws`
- Connect with credentials (cookie must be present — see caveat above).
- Server pings every 25s; keep the socket alive.
- Inbound message envelope: `{ "type": string, "room_id": number, "data": any, "ts": number }`.
- Event `type` values the backend emits:
  `message_created`, `room_unread_update`, `reaction_updated`, `room_seen_update`,
  `rooms_sync`, `room.member_added`, `room.joined`, `room.member_removed`.

## Quick smoke test (from any machine)
```bash
BASE=http://56.10.41.218:5555
# login
curl -s -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<REDACTED>"}'
# use the accessToken:
curl -s $BASE/me -H "Authorization: Bearer <accessToken>"
```
