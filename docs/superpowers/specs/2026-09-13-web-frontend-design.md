# Spec 3 技術設計 — Web Frontend POC

**狀態：** 已定稿，準備進 implementation plan
**產品 spec：** `docs/superpowers/specs/2026-08-30-03-web-frontend-poc.md`
**前置依賴：** Spec 2（已 merge 到本地 main，`7ca2003`）
**部署細節（Cloudflare Pages、Firebase authorized domain 等）：** 留給 Spec 4

## 1. Stack

| 面向 | 選擇 | 備註 |
|---|---|---|
| Framework | React 18 + Vite 5 + TypeScript 5 | Firebase SDK 生態最完整、TS 對 API contract 型別安全 |
| Routing | React Router v6 | Nested route + lazy load + protected route pattern |
| Data layer | TanStack Query v5 | `useInfiniteQuery` 直接對應 cursor pagination |
| Styling | Tailwind CSS v3 | Mobile-first 內建、JIT purge、無 runtime cost |
| Auth SDK | Firebase Web SDK v10（modular），Google provider | POC 只啟用一個 provider |
| Auth adapter | Build-time 切換（VITE_AUTH_MODE） | Dev → testauth，Prod → Firebase |
| Testing | Vitest + React Testing Library；Playwright | Colocated `*.test.ts`；e2e 走 docker-compose stack |
| Package manager | npm | 沒有 monorepo 需求 |
| Node | 20 LTS（`.nvmrc` pin） | Cloudflare Pages 支援 |
| Bundle target | ES2020，ESM | 家用瀏覽器都支援 |

## 2. Repo Layout

```
photo-browser/
├── web/                              # 新增
│   ├── package.json
│   ├── vite.config.ts                # dev proxy /api /media → http://localhost:8081
│   ├── tsconfig.json
│   ├── tailwind.config.ts
│   ├── postcss.config.js
│   ├── index.html
│   ├── .env.example                  # VITE_API_BASE_URL, VITE_AUTH_MODE, VITE_FIREBASE_*
│   ├── .nvmrc                        # 20
│   ├── playwright.config.ts
│   ├── public/
│   ├── src/
│   │   ├── main.tsx
│   │   ├── App.tsx                   # QueryClientProvider + AuthProvider + RouterProvider
│   │   ├── routes/                   # 一個 route 一個檔，對應 URL contract
│   │   │   ├── login.tsx
│   │   │   ├── photos.tsx
│   │   │   ├── timeline.tsx
│   │   │   ├── albums.tsx
│   │   │   ├── album-detail.tsx
│   │   │   ├── categories.tsx
│   │   │   ├── category-detail.tsx
│   │   │   ├── viewer.tsx
│   │   │   ├── profile.tsx
│   │   │   ├── forbidden.tsx
│   │   │   └── protected.tsx         # loader gate: 等 auth init → 呼 /me → allow/redirect
│   │   ├── auth/
│   │   │   ├── provider.ts           # AuthProvider interface
│   │   │   ├── firebase.ts           # prod adapter
│   │   │   ├── testauth.ts           # dev adapter
│   │   │   ├── context.tsx           # React context + useAuth hook
│   │   │   └── select.ts             # build-time picks by VITE_AUTH_MODE
│   │   ├── api/
│   │   │   ├── client.ts             # fetch wrapper: bearer + 401 retry + envelope + request-id
│   │   │   ├── queries.ts            # useCategories / useAlbums / useAlbum / usePhotos / useTimeline / useMe
│   │   │   ├── admin.ts              # 不用；POC 前端不呼 admin endpoint
│   │   │   └── types.ts              # 對齊 internal/httpapi 的 response shape
│   │   ├── media/
│   │   │   ├── blob-cache.ts         # LRU + concurrency limit + revoke
│   │   │   └── hooks.ts              # useAuthedImage(url) → { objectUrl, state }
│   │   ├── components/
│   │   │   ├── layout/               # AppShell, BottomNav, Header
│   │   │   ├── grid/                 # PhotoGrid, PhotoTile
│   │   │   ├── viewer/               # PhotoViewer, ViewerNav
│   │   │   ├── states/               # Loading, Empty, ErrorPanel, RetryButton, ForbiddenNotice
│   │   │   └── ui/                   # Button, Tabs, IconButton, VisuallyHidden
│   │   ├── hooks/                    # useIntersection (lazy load), useDebounce
│   │   ├── lib/                      # cursor.ts, fmt.ts (date grouping), env.ts
│   │   └── styles/
│   │       └── globals.css           # tailwind base + resets
│   ├── tests/e2e/                    # Playwright specs
│   └── README.md
├── Makefile                          # 加 dev-web / test-web / e2e-web / build-web
├── deploy/compose/
│   └── docker-compose.dev.yml        # 前端本機開發用的常駐 stack
└── ...（既有 backend 檔案不動）
```

**原則：**

- 路由檔一個 route 一個檔，跟 spec §3 的 URL contract 一對一。
- `auth/` `api/` `media/` `components/` `hooks/` `lib/` 分層清楚，每個模組單一責任、可獨立 Vitest。
- Unit test colocated（`foo.ts` 旁邊放 `foo.test.ts`）；Playwright 集中在 `tests/e2e/`。
- **前端不呼 admin endpoint**。Admin 操作繼續走 `photo-app admin` CLI。

## 3. Auth Adapter

### Interface

```ts
// src/auth/provider.ts
export type AuthState =
  | { kind: "initializing" }
  | { kind: "signed-out" }
  | { kind: "signed-in"; uid: string; email: string | null; displayName: string | null };

export interface AuthProvider {
  init(): Promise<void>;
  onChange(cb: (s: AuthState) => void): () => void;
  signIn(): Promise<void>;
  signOut(): Promise<void>;
  getIdToken(forceRefresh?: boolean): Promise<string | null>;
}
```

### Build-time 選擇

```ts
// src/auth/select.ts
import type { AuthProvider } from "./provider";
import { FirebaseAuthProvider } from "./firebase";
import { TestAuthProvider } from "./testauth";

export function makeAuthProvider(): AuthProvider {
  const mode = import.meta.env.VITE_AUTH_MODE;
  if (mode === "firebase") return new FirebaseAuthProvider();
  if (mode === "testauth") return new TestAuthProvider();
  throw new Error(`unknown VITE_AUTH_MODE: ${mode}`);
}
```

Vite 的 tree-shaking + build-time env 會讓 `testauth` 這條 branch 在 prod bundle 消失。CI 檢查 prod bundle 不含 `testauth` 字串。

### FirebaseAuthProvider

- 用 modular SDK：`initializeApp` + `getAuth` + `GoogleAuthProvider` + `signInWithPopup`（或 `signInWithRedirect`；POC 先用 popup，桌機/手機都通）。
- `onIdTokenChanged` 為 auth 唯一 source of truth。
- `getIdToken(true)` 用來強制 refresh。
- Firebase config 從 `import.meta.env.VITE_FIREBASE_*` 讀（public config，可上前端）。

### TestAuthProvider

- **登入畫面**：一個下拉選單，列出 `.env.local` 定義的 dev user（`uid`, `email`, `role`），加一個「Sign in」按鈕。
- **`signIn`**：呼叫 `${VITE_TESTAUTH_URL}/mint?sub=<uid>&email=<email>&verified=1`，拿 JWT 存到 in-memory + `sessionStorage`。
- **`getIdToken(force)`**：token 過期或 `force = true` 時重新 mint。
- **不依賴任何 Firebase SDK code**，`FirebaseAuthProvider` 完全不 import。

## 4. API Client

### 契約

- Base URL：`import.meta.env.VITE_API_BASE_URL`（dev = 空字串走 Vite proxy；prod = `https://photos-api.example.com/api/v1`）。
- 每個 request 加 `Authorization: Bearer <ID token>`；token 從 auth adapter 拿。
- Response envelope：成功回 JSON body；錯誤回 `{ code, message, request_id }` + HTTP status。
- 401 一次 retry：`getIdToken(true)` 後重打；仍 401 → 觸發 auth adapter `signOut`、導 `/login`。
- 403 → 導 `/forbidden`，不 retry。
- 5xx / network error → 交由 TanStack Query 的 retry policy（bounded、指數退避）。

### 骨架

```ts
// src/api/client.ts
export class ApiError extends Error {
  constructor(public status: number, public code: string, public requestId: string | null, message: string) {
    super(message);
  }
}

export function makeApi(auth: AuthProvider) {
  async function request(path: string, init: RequestInit = {}): Promise<Response> {
    const doFetch = async (forceToken = false) => {
      const token = await auth.getIdToken(forceToken);
      const headers = new Headers(init.headers);
      if (token) headers.set("Authorization", `Bearer ${token}`);
      return fetch(`${API_BASE}${path}`, { ...init, headers });
    };
    let res = await doFetch(false);
    if (res.status === 401) res = await doFetch(true);
    if (!res.ok) throw await toError(res);
    return res;
  }
  return { request, getJson<T>(p: string) { return request(p).then(r => r.json() as Promise<T>); } };
}
```

### Query hooks（TanStack Query）

- `useMe()` — `queryKey: ["me"]`
- `useCategories()` — cursor infinite query
- `useAlbums({ categoryId?: string })`
- `useAlbum(albumId)`
- `usePhotos({ albumId?, categoryId? })` — cursor infinite
- `useTimeline()` — cursor infinite，year/month 分組交給 UI 層做

**Cursor 處理**：`getNextPageParam: (last) => last.next_cursor ?? undefined`。

### Backend API 缺口（Spec 3 附帶修正）

Spec 2 的 album DTO 目前只回 `cover_thumbnail_key`，缺對應的 `cover_photo_id`；縮圖路由 `/photos/{photo_id}/thumbnail/{thumbnail_key}` 需要兩者。

**修正**：把 `internal/httpapi/catalog.go` album DTO 加 `cover_photo_id int64 json:"cover_photo_id,omitempty"`，並讓 `internal/catalog/query.go:AlbumCoverThumbnail` 一併回傳 `photo_id`（改名 `AlbumCover(albumID) → (photoID, key, ok, err)`）。這是 Spec 3 的第一個 implementation task；不做前端會需要多打一次 `/albums/{id}/photos?limit=1`。

## 5. Media Blob 策略

`<img src="protected-url">` 無法附 Authorization header，所以：

1. 拿到 photo id 後呼 `GET /api/v1/photos/:id/thumbnail/:key`（或 `/original`），加 Bearer。
2. Backend 回 HTTP 200 OK + 空 body + `X-Accel-Redirect: /internal-media/...` header。Nginx 在把 response 送給 client 之前攔到這個 header，改成從 internal location（`internal;` 只允許 subrequest）讀檔案，把二進位內容替換進 response body。Browser 只看到一個正常的 200 OK 帶 image bytes，全程同 origin、不 follow redirect。
3. `response.blob()` → `URL.createObjectURL(blob)` → `<img src={objectUrl}>`。
4. Tile/Viewer unmount 或 blob-cache eviction 時 `URL.revokeObjectURL`。

### Blob cache

```ts
// src/media/blob-cache.ts
- LRU，容量 50 個 blob（可調），超過就 revoke + evict。
- In-flight dedup：同一個 URL 同時被多個 tile 要，只發一個 fetch。
- 全域 concurrent fetch 限制 = 4（避免手機同時開一大格 grid 塞爆網路）。
- 錯誤（404/403/network）也記在 cache，避免無限重打。
```

```ts
// src/media/hooks.ts
export function useAuthedImage(url: string):
  | { state: "loading" }
  | { state: "ready"; objectUrl: string }
  | { state: "error"; kind: "not-found" | "forbidden" | "network" }
```

## 6. Routing + Protected Route

### URL contract（同 spec §3）

```
/login
/photos          (預設 tab: All Photos)
/photos/timeline
/albums
/albums/:albumId
/categories
/categories/:categoryId
/viewer/:photoId
/profile
/forbidden
```

### Layout

```
<Root>
  <AuthProvider>
    <QueryClientProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/forbidden" element={<Forbidden />} />
          <Route element={<ProtectedShell />}>    {/* Outlet + BottomNav */}
            <Route index element={<Navigate to="/photos" />} />
            <Route path="photos" element={<Photos />} />
            <Route path="photos/timeline" element={<Timeline />} />
            <Route path="albums" element={<Albums />} />
            <Route path="albums/:albumId" element={<AlbumDetail />} />
            <Route path="categories" element={<Categories />} />
            <Route path="categories/:categoryId" element={<CategoryDetail />} />
            <Route path="viewer/:photoId" element={<Viewer />} />
            <Route path="profile" element={<Profile />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </AuthProvider>
</Root>
```

### ProtectedShell

- 讀 `useAuth()`：`initializing` → loading；`signed-out` → `<Navigate to="/login" />`。
- 讀 `useMe()`：pending → loading；`403` → `<Navigate to="/forbidden" />`；success → 渲染 `<Outlet />` + `<BottomNav />`。
- **重要**：Firebase 成功 ≠ 有權限。UI 一定要等 `/me` 200 才顯示照片區。

## 7. State / Data 邊界

- **Auth**：React context，唯一來源。
- **Query cache**：TanStack Query。
- **URL search params**：分頁 cursor state 由 TanStack Query 管；tab 切換 + viewer nav 用 URL param（可分享、可 back）。
- **Local state**：component `useState`。
- **不引入 Redux/Zustand/Jotai 等 global state library**。

## 8. Component / 畫面規格

### AppShell / BottomNav

- 底部 4 個 tab：Photos / Albums / Categories / Profile。
- Icon-only 用 `aria-label`；active 用顏色 + underline。
- Touch target ≥ 44 × 44。

### Photos

- 上方 segmented control：All / Timeline。
- All: 3-column grid（手機）→ 5-6 col（tablet）→ 8+ col（desktop）via Tailwind `grid-cols-*` breakpoint。
- 每張 tile 用 `useAuthedImage(thumbnail_url)`；`useIntersection` 只 fetch 進 viewport 的 tile。
- 點 tile → `/viewer/:photoId?from=all` 帶 context。

### Timeline

- 只呼 `/photos/timeline`。
- UI 層依 local `taken_at` 分 Year/Month heading；不重排 backend 順序。
- 缺 `taken_at` 的照片：`/timeline` 不會回傳，UI 不用處理。

### Albums / AlbumDetail / Categories / CategoryDetail

- Album card：cover thumbnail + album name + category name + photo count。
- 無 cover 用中性 placeholder（不 fetch）。
- Category card：name + album count；沒有 edit/create/delete。

### Viewer

- Full-screen；上方顯示 filename + `taken_at`；close (X) 回上一頁。
- 左右滑（或箭頭鍵）在**當前 collection 已載入的 photos** 內移動；沒 loaded 的不 fetch 到 viewer 就顯示。
- Escape 關閉。
- 404 / 403 → 顯示「Photo unavailable」+ close。
- **沒有 download / delete / rename / share / EXIF panel**。

### Profile

- 顯示 Firebase displayName / email / avatar（能拿到就顯示）+ backend role from `/me`。
- Logout button → `signOut()` → `/login`。

### 錯誤 / 空狀態（對應 spec §9）

`ErrorPanel` 一個 component 吃 `{ kind, retry? }`，涵蓋 empty / thumbnail-404 / api-unavailable / pagination-fail / forbidden / not-found / offline。

## 9. A11y 與響應式

- 全部 interactive 元件用 `<button>` / `<a>` 原生 tag，keyboard 天生可用。
- Focus ring 保留（不做 `outline: none`）。
- `<img alt="">` 對純裝飾；有 filename 的 tile 給 `alt={filename}`。
- Viewer 支援 Escape、方向鍵；`role="dialog"` + `aria-modal="true"`。
- Layout 從 360 px 起可用；`min-w-0` + `overflow-hidden` 避免文字撐破格子。
- `prefers-reduced-motion` → 停 loader spin animation。
- Skeleton loader 用固定尺寸，避免 layout shift。

## 10. Dev 工作流

### 環境變數（`web/.env.example`）

```
# dev
VITE_API_BASE_URL=/api/v1
VITE_AUTH_MODE=testauth
VITE_TESTAUTH_URL=http://localhost:8090
VITE_DEV_USERS='[{"uid":"admin-1","email":"admin@example.com","role":"admin"},{"uid":"member-1","email":"member@example.com","role":"member"},{"uid":"denied-1","email":"denied@example.com","role":"member"}]'

# prod（Cloudflare Pages 環境變數，Spec 4 才會設）
# VITE_API_BASE_URL=https://photos-api.example.com/api/v1
# VITE_AUTH_MODE=firebase
# VITE_FIREBASE_API_KEY=...
# VITE_FIREBASE_AUTH_DOMAIN=...
# VITE_FIREBASE_PROJECT_ID=...
# VITE_FIREBASE_APP_ID=...
```

### Vite proxy

```ts
// vite.config.ts
server: {
  port: 5173,
  proxy: {
    "/api": { target: "http://localhost:8081", changeOrigin: true },
    "/media": { target: "http://localhost:8081", changeOrigin: true },
  },
},
```

### docker-compose.dev.yml

跟 acceptance 幾乎一樣，差別：

- fixture 直接 mount `test-photos/`；
- `photo-data` 用 named volume（跨 dev session 保留）；
- backend 起來自動先跑一次 `photo-app index`（entrypoint script），確保 SQLite 有資料；
- backend `ALLOWED_ORIGINS=http://localhost:5173`；
- nginx 綁 `:8081`（同 acceptance）；
- testauth 綁 `:8090`。

### Makefile 新增

```
dev-web:            起 docker-compose.dev.yml + 前景 vite dev
dev-web-stack:      只起 docker-compose.dev.yml
test-web:           cd web && npm run test        (Vitest)
e2e-web:            docker-compose up + npm run e2e (Playwright)
build-web:          cd web && npm ci && npm run build → web/dist/
```

### First-time setup

```
nvm use             # 20
cd web && npm ci
make dev-web-stack  # 另一個 terminal
cd web && npm run dev
```

Bootstrap allowlist 由 dev-stack entrypoint 自動塞（用 `dev-users` fixture 呼 `photo-app admin add-user`）。

## 11. 測試策略

### Vitest（unit / integration）

- `auth/testauth.test.ts`：mock fetch，驗 `getIdToken` 過期會 re-mint。
- `auth/firebase.test.ts`：mock `onIdTokenChanged`，驗 `AuthState` 轉換。
- `api/client.test.ts`：驗 401 → forceRefresh retry 一次；envelope 錯誤解析；request-id capture。
- `media/blob-cache.test.ts`：LRU eviction 會 `revokeObjectURL`；in-flight dedup；concurrent limit。
- `components/**/*.test.tsx`：Photos grid empty / loading / error；Viewer keyboard nav；ProtectedShell 三段狀態。
- `lib/fmt.test.ts`：year/month 分組邊界（12/1、跨年、閏年）。

**目標覆蓋率不設數字**；照 Spec 3 §9 錯誤表逐條寫。

### Playwright（e2e）

跑在 `docker-compose.dev.yml` + `npm run build && npm run preview`（生產 build，不是 dev server）：

- `login-and-browse.spec.ts`：admin user → grid → tile → viewer → back → logout。
- `member-flow.spec.ts`：member user 走完 Timeline 分組。
- `forbidden.spec.ts`：`denied` user → /forbidden。
- `thumb-404.spec.ts`：故意刪掉一張 thumbnail file → placeholder + grid 仍可用。
- `token-refresh.spec.ts`：mint 短 exp token（`?exp=3`），等待到期後操作 grid → 應自動 refresh、不掉登入。

Playwright 用 `mobile viewport (Pixel 5)`，覆蓋 360-px 需求。

## 12. Build / 產物

- `npm run build` → `web/dist/`（HTML + hashed JS/CSS chunks + assets）。
- Vite 已內建：ESM、code splitting per route（`React.lazy`）、asset hash、preload。
- **Prod bundle guard**：CI 加一步 `grep -r "TestAuthProvider\|/mint" web/dist && exit 1`。
- Sourcemap：`build.sourcemap: "hidden"`（產出但不 link，方便 Sentry 之類，POC 先產出就好）。

## 13. 留給 Spec 4

- Cloudflare Pages project 設定、build hook、環境變數注入。
- Firebase project 建立、Google provider 啟用、Authorized Domain。
- Production API base URL、CORS `ALLOWED_ORIGINS`。
- Cloudflare Tunnel、production `nginx.conf`。
- Firebase Admin bootstrap（第一個 admin allowlist entry）。

## 14. 完成關卡（對齊 Spec 3 §10, §11）

- [ ] 360 px browser 完成 login → browse → viewer → logout（Playwright e2e 綠燈）
- [ ] `/me` 授權完成前不顯示照片
- [ ] URL 對得上 spec §3 的 route contract
- [ ] Timeline 只顯示 non-null `taken_at`
- [ ] Album / Category drill-down 正確
- [ ] Media 帶 Authorization、object URL 有被 revoke（Vitest 覆蓋）
- [ ] Pagination 不重載、不常駐整個 library
- [ ] 401 / 403 / empty / thumbnail-404 / api-down 各有 UI 覆蓋（Vitest + Playwright）
- [ ] 沒有 upload / delete / rename / move / admin control
- [ ] Prod bundle 不含 testauth 程式碼（CI grep guard）
- [ ] `make dev-web` 一鍵起 stack + Vite；`make test-web` `make e2e-web` `make build-web` 全綠

## 15. Non-Goals（POC 不做）

- PWA、offline library、Service Worker。
- 圖片編輯、EXIF panel、地圖、tag、search。
- Slideshow、gesture zoom。
- 多語 i18n（POC 用中英夾雜的直接字串）。
- Cloudflare Pages 設定（Spec 4）。
- Firebase Admin SDK 呼叫（backend 只 verify token）。
- Global state library。
- SSR / SSG。
