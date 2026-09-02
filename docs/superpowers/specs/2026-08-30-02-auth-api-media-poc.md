# Spec 2 — 驗證授權 API 與影像傳送 POC

**狀態：** Spec 1 完成後可執行  
**前置依賴：** Spec 1 schema 與 indexer  
**完成後解鎖：** Spec 3–4

## 1. 目標

提供唯讀 catalogue 與 media endpoints，並證明 Firebase authentication、本地 authorization 與 Nginx internal media delivery 無法被繞過。

## 2. 範圍

包含 Firebase ID Token verification、UID/verified-email allowlist、`admin`/`member` roles、Categories/Albums/Photos/Timeline/Profile APIs、cursor pagination、admin manual scan、Nginx `X-Accel-Redirect`、CORS 與 private cache policy。

不包含 Photo mutation、Per-Album/Per-Photo ACL、公開分享、Cloudflare edge media caching 與 Admin Web UI。

## 3. Application-Owned Schema

```text
users(
  id INTEGER PRIMARY KEY,
  firebase_uid TEXT UNIQUE,
  email TEXT,
  normalized_email TEXT UNIQUE,
  role TEXT NOT NULL CHECK(role IN ('admin','member')),
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK(firebase_uid IS NOT NULL OR normalized_email IS NOT NULL)
)
```

規則：

- Firebase UID exact match 優先；
- Email match 要求 `email_verified = true`；
- Email normalization 只做 trim + lowercase；
- Disabled 或查無 allowlist entry 時 fail closed；
- 不儲存 password 或 Firebase token；
- 第一位 admin 由 local CLI 建立，不提供 unauthenticated bootstrap endpoint。

## 4. 身分驗證與授權流程

```text
Authorization: Bearer <Firebase ID token>
  → 驗證 signature、algorithm、issuer、audience、expiry、subject
  → 取得 UID/email/email_verified
  → 查詢 enabled allowlist
  → 將 role 放入 request context
  → 驗證 endpoint permission
```

- Token 缺少、格式錯誤、無效或過期：`401`。
- Firebase 身分有效但未在 allowlist：`403`。
- 通過授權後仍查無 resource：`404`。

除 health check 外，所有 `/api/v1/**` routes 都必須同時完成 authentication 與 allowlist authorization。

## 5. API 契約

```text
GET /api/v1/health/live
GET /api/v1/health/ready
GET /api/v1/me

GET /api/v1/categories
GET /api/v1/categories/{category_id}/albums
GET /api/v1/albums?cursor=&limit=
GET /api/v1/albums/{album_id}
GET /api/v1/albums/{album_id}/photos?cursor=&limit=
GET /api/v1/photos?cursor=&limit=
GET /api/v1/photos/timeline?cursor=&limit=
GET /api/v1/photos/{photo_id}

GET /api/v1/photos/{photo_id}/thumbnail/{thumbnail_key}
GET /api/v1/photos/{photo_id}/original

GET   /api/v1/admin/users
POST  /api/v1/admin/users
PATCH /api/v1/admin/users/{user_id}
POST  /api/v1/admin/index-runs
GET   /api/v1/admin/index-runs/{scan_id}
```

只有 admin 能使用 admin routes。Manual indexing 回傳帶有 scan ID 的 `202`；已有 scan 執行時回傳 `409`。

## 6. Query 與 Pagination 契約

- 預設 page size 50，最大 200。
- Timeline 僅包含 `taken_at IS NOT NULL`，排序為 `taken_at DESC, id DESC`。
- Album Photo 排序為 `taken_at DESC NULLS LAST, id DESC`。
- Cursor 是 opaque、帶版本的 ordering tuple encoding。
- 無效 cursor 回傳 `400`；client 不得提交 SQL field name。
- Album cover 使用該 Album 排序下第一張可用 thumbnail，不持久化為 application-owned state。

## 7. 影像授權流程

1. Browser 以 numeric photo ID 請求 media endpoint。
2. Backend 驗證 Firebase token 與 allowlist。
3. Backend 由 SQLite 解析 current relative path。
4. Backend 驗證 path 是 relative 且位於設定 root 內。
5. Thumbnail key 必須與 DB current key 相等。
6. Backend 只回 headers 與 `X-Accel-Redirect`，不回 image body。
7. Nginx 從 `internal` location 傳送 bytes。

```text
/internal-media/originals/  → read-only /srv/photos/
/internal-media/thumbnails/ → read-only /srv/thumbnails/
```

兩者都必須設定 `internal` 並關閉 directory listing。External client 不得提供 filesystem path。

## 8. 影像回應標頭

Thumbnail：

```text
Cache-Control: private, max-age=86400, immutable
ETag: <thumbnail-key>
X-Content-Type-Options: nosniff
```

Original：

```text
Cache-Control: private, max-age=3600
ETag: <size>-<mtime_ns>
X-Content-Type-Options: nosniff
```

Nginx 應支援 HEAD 與 Range request。Cloudflare 不得對這些 endpoints 使用 shared `Cache Everything` rule。

## 9. 信任邊界要求

- Backend 只 listen internal container network；只有 Nginx 可由 `cloudflared` 抵達。
- API process 對 original 與 thumbnail mounts 都是 read-only。
- 拒絕 absolute path、`..`、NUL byte、separator ambiguity 與 symlink escape。
- 不支援 SVG 或任意 active content。
- Logs 必須移除 Authorization header 與 token。
- CORS allow-origin 必須精確等於 production frontend origin；CORS 不視為 authorization。
- 限制 request body size；public catalogue/media endpoints 僅允許 GET/HEAD。

## 10. Error 契約

```json
{
  "error": {
    "code": "forbidden",
    "message": "Access is not allowed."
  }
}
```

Errors 不得洩漏 SQL、stack trace、Firebase internals、absolute path 或 internal redirect path。

## 11. 驗收條件

- Allowlisted member 可查詢所有 catalogue resources 與 media。
- 有效但未列入 allowlist 的 Firebase identity，在 media endpoint 也只能得到 `403`。
- Invalid/expired token 回傳 `401`；member 對 admin routes 得到 `403`。
- Admin 可新增/停用 allowlist entry，並觸發一次 scan。
- 直接請求 internal media location 無法取得 bytes。
- Encoded traversal 與 stale thumbnail key 失敗且不洩漏 path。
- Backend media handler 不回大型 image body，Nginx 可處理 Range delivery。
- Timeline 排除 null `taken_at` 並維持 stable pagination。
- Logs 含 request ID，但不含 bearer token。

## 12. 完成關卡

API integration tests 與真實 Nginx request 證明每次 media response 前皆完成 authorization，才算完成 Spec 2。Spec 3 可依賴本 API 契約，不需知道 NAS 實作細節。
