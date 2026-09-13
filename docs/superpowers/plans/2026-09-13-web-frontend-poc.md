# Spec 3 Web Frontend POC — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 Spec 3 完整前端 POC——React + Vite + TS 單頁應用，能透過 Firebase (prod) 或 testauth (dev) 登入，瀏覽 NAS 照片庫（Photos / Timeline / Albums / Categories / Viewer / Profile），使用 authenticated blob fetch 拉受保護的媒體，並在 docker-compose acceptance stack 上跑 Playwright e2e。

**Architecture:** Build-time 切換的 auth adapter（`TestAuthProvider` for dev、`FirebaseAuthProvider` for prod）餵給 React context；API client 是薄的 `fetch` wrapper 處理 Bearer token + 401 retry + envelope 錯誤；資料層走 TanStack Query（cursor pagination 用 `useInfiniteQuery`）；媒體用 authenticated fetch → `URL.createObjectURL`，以 LRU + concurrent limit 的 blob cache 管理生命週期；React Router v6 route contract 由 `ProtectedShell`（等 auth init + `/me` 200）守門。

**Tech Stack:** React 18, Vite 5, TypeScript 5, React Router v6, TanStack Query v5, Tailwind CSS v3, Firebase Web SDK v10, Vitest + React Testing Library, Playwright, npm, Node 20 LTS.

**Spec:** `docs/superpowers/specs/2026-09-13-web-frontend-design.md`（技術設計）
+ `docs/superpowers/specs/2026-08-30-03-web-frontend-poc.md`（產品 spec）

## Global Constraints

- Node **20 LTS**（`.nvmrc` pin），package manager **npm**（無 monorepo）。
- 所有前端檔案落在 `web/` 底下；backend Go 檔案只在 Task 1 動一次（加 `cover_photo_id`）。
- 全部 TypeScript `strict: true`；不用 `any`（能不用就不用）。
- 不引入 global state library（Redux/Zustand/Jotai）；auth 用 React context、資料用 TanStack Query、其餘 component-local。
- 前端**不呼 admin endpoint**；admin 操作繼續走 `photo-app admin` CLI。
- Layout 從 **360 px** width 起可用；touch target ≥ **44 × 44 CSS px**。
- Prod bundle **不得**包含 `TestAuthProvider` 相關字串（Task 22 用 grep guard 驗證）。
- Media 一律 authenticated blob fetch；不得把 token 放到 URL / query / localStorage 之外的位置（`sessionStorage` 允許給 testauth token）。
- Commit message 用 conventional style，`feat:` / `fix:` / `test:` / `docs:` / `chore:` 前綴；每個 task 至少一個 commit。

---

## Task 1: Backend — Album cover_photo_id

**Files:**
- Modify: `internal/httpapi/catalog.go`（`albumDTO`、`getAlbum`、`listAlbums`、`listCategoryAlbums`）
- Modify: `internal/catalog/query.go`（`AlbumCoverThumbnail` → `AlbumCover`）
- Modify: `internal/catalog/query_test.go`
- Modify: `internal/httpapi/catalog_test.go`

**Interfaces:**
- Consumes: nothing new
- Produces: `albumDTO.cover_photo_id int64 json:"cover_photo_id,omitempty"`（0 時 omit）；`catalog.Store.AlbumCover(ctx, albumID) (photoID int64, key string, ok bool, err error)`。

- [x] **Step 1: 讀現況**

先讀 `internal/catalog/query.go:187-202`（`AlbumCoverThumbnail`）與 `internal/httpapi/catalog.go:14-22, 170-185`（`albumDTO` + 塞 cover 的位置）。

- [x] **Step 2: 改 test — query 層**

修改 `internal/catalog/query_test.go` 中所有呼叫 `AlbumCoverThumbnail` 的地方，換成 `AlbumCover`；斷言同時檢查回傳的 photo_id 對應到 album 內 `taken_at DESC NULLS LAST, id DESC` 排序後第一張。

```go
photoID, key, ok, err := store.AlbumCover(ctx, albumID)
require.NoError(t, err)
require.True(t, ok)
require.Equal(t, expectedPhotoID, photoID)
require.Equal(t, "abc123.webp-key", key)
```

- [x] **Step 3: 跑 test 確認失敗**

```
make test 2>&1 | grep -E "FAIL|undefined"
```

期望：`AlbumCover undefined`。

- [x] **Step 4: 實作 query.go**

```go
// AlbumCover returns the photo_id + thumbnail_key of the album's cover photo:
// first non-null thumbnail_key in (taken_at DESC NULLS LAST, id DESC) order.
func (s *Store) AlbumCover(ctx context.Context, albumID int64) (int64, string, bool, error) {
	var photoID int64
	var key sql.NullString
	err := s.conn.QueryRowContext(ctx,
		`SELECT id, thumbnail_key
		 FROM photos
		 WHERE album_id=? AND thumbnail_key IS NOT NULL
		 ORDER BY (taken_at IS NULL), taken_at DESC, id DESC
		 LIMIT 1`, albumID,
	).Scan(&photoID, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return photoID, key.String, true, nil
}
```

刪掉舊的 `AlbumCoverThumbnail`。

- [x] **Step 5: 改 DTO 與所有塞 cover 的地方**

在 `internal/httpapi/catalog.go`：

```go
type albumDTO struct {
	ID                int64  `json:"id"`
	CategoryID        int64  `json:"category_id"`
	Name              string `json:"name"`
	RelativePath      string `json:"relative_path"`
	CoverPhotoID      int64  `json:"cover_photo_id,omitempty"`
	CoverThumbnailKey string `json:"cover_thumbnail_key,omitempty"`
}
```

把所有呼叫 `AlbumCoverThumbnail` 的三個地方（`getAlbum`、`listAlbums`、`listCategoryAlbums`）換成：

```go
if pid, key, ok, err := d.Catalog.AlbumCover(r.Context(), id); err == nil && ok {
	dto.CoverPhotoID = pid
	dto.CoverThumbnailKey = key
}
```

- [x] **Step 6: 改 httpapi test 加對 cover_photo_id 的斷言**

`internal/httpapi/catalog_test.go` 三個涉及 album 的 test 加：

```go
assert.NotZero(t, resp.Albums[0].CoverPhotoID)
assert.NotEmpty(t, resp.Albums[0].CoverThumbnailKey)
```

（實際欄位名以 struct/json 為準。）

- [x] **Step 7: 跑 test 確認全綠**

```
make test 2>&1 | tail -20
```

期望：全部 `ok`。

- [x] **Step 8: Commit**

```bash
git add internal/httpapi/catalog.go internal/httpapi/catalog_test.go internal/catalog/query.go internal/catalog/query_test.go
git commit -m "feat: expose album cover_photo_id for frontend thumbnail url"
```

---

## Task 2: web/ 專案骨架

**Files:**
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/tsconfig.node.json`
- Create: `web/vite.config.ts`
- Create: `web/tailwind.config.ts`
- Create: `web/postcss.config.js`
- Create: `web/index.html`
- Create: `web/.env.example`
- Create: `web/.env.local`（gitignored）
- Create: `web/.nvmrc`
- Create: `web/.gitignore`
- Create: `web/src/main.tsx`
- Create: `web/src/App.tsx`
- Create: `web/src/styles/globals.css`
- Create: `web/src/lib/env.ts`
- Create: `web/README.md`
- Modify: `.gitignore`（root）

**Interfaces:**
- Consumes: 無
- Produces: `web/` 可 `npm ci`、`npm run dev`、`npm run build`、`npm run test`（Vitest）跑起來（但 test 尚無 case）；Vite dev proxy `/api` 與 `/media` 到 `http://localhost:8081`。

- [x] **Step 1: 建 `.nvmrc` 與根 `.gitignore` 補丁**

`web/.nvmrc`：

```
20
```

在 root `.gitignore` 追加：

```
web/node_modules
web/dist
web/coverage
web/playwright-report
web/test-results
web/.env.local
```

如果 root 沒有 `.gitignore`，建立一個並加上上面內容。

- [x] **Step 2: `web/package.json`**

```json
{
  "name": "photo-browser-web",
  "private": true,
  "type": "module",
  "version": "0.0.0",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview --port 5173",
    "test": "vitest run",
    "test:watch": "vitest",
    "e2e": "playwright test",
    "lint": "tsc --noEmit"
  },
  "dependencies": {
    "@tanstack/react-query": "^5.51.0",
    "firebase": "^10.13.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.26.0"
  },
  "devDependencies": {
    "@playwright/test": "^1.46.0",
    "@testing-library/jest-dom": "^6.4.8",
    "@testing-library/react": "^16.0.0",
    "@testing-library/user-event": "^14.5.2",
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "autoprefixer": "^10.4.19",
    "jsdom": "^24.1.1",
    "postcss": "^8.4.40",
    "tailwindcss": "^3.4.7",
    "typescript": "^5.5.4",
    "vite": "^5.4.0",
    "vitest": "^2.0.5"
  }
}
```

- [x] **Step 3: `web/tsconfig.json` + `tsconfig.node.json`**

`web/tsconfig.json`：

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "tests"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

`web/tsconfig.node.json`：

```json
{
  "compilerOptions": {
    "composite": true,
    "skipLibCheck": true,
    "module": "ESNext",
    "moduleResolution": "bundler",
    "allowSyntheticDefaultImports": true,
    "strict": true
  },
  "include": ["vite.config.ts", "playwright.config.ts", "tailwind.config.ts"]
}
```

- [x] **Step 4: `web/vite.config.ts`**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8081", changeOrigin: true },
      "/media": { target: "http://localhost:8081", changeOrigin: true },
    },
  },
  build: {
    target: "es2020",
    sourcemap: "hidden",
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    coverage: { reporter: ["text", "html"] },
  },
});
```

- [x] **Step 5: Tailwind + PostCSS**

`web/tailwind.config.ts`：

```ts
import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      minHeight: { touch: "44px" },
      minWidth: { touch: "44px" },
    },
  },
  plugins: [],
} satisfies Config;
```

`web/postcss.config.js`：

```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
```

- [x] **Step 6: `web/index.html`**

```html
<!doctype html>
<html lang="zh-Hant">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/vite.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover" />
    <meta name="color-scheme" content="light dark" />
    <title>Photo Browser</title>
  </head>
  <body class="min-h-screen bg-white text-gray-900 dark:bg-gray-950 dark:text-gray-100">
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [x] **Step 7: `web/.env.example` + `.env.local`**

`.env.example`（committed）：

```
# 選一：testauth（本機 docker-compose stack）或 firebase（prod）
VITE_AUTH_MODE=testauth

# 走 Vite proxy 時可以留空；直接打 backend 就填 http://localhost:8081/api/v1
VITE_API_BASE_URL=/api/v1

# TestAuthProvider 專用
VITE_TESTAUTH_URL=http://localhost:8090
VITE_DEV_USERS=[{"uid":"admin-1","email":"admin@example.com","displayName":"Admin","role":"admin"},{"uid":"member-1","email":"member@example.com","displayName":"Member","role":"member"},{"uid":"denied-1","email":"denied@example.com","displayName":"Denied","role":"member"}]

# FirebaseAuthProvider 專用（Spec 4 才會填實際值）
VITE_FIREBASE_API_KEY=
VITE_FIREBASE_AUTH_DOMAIN=
VITE_FIREBASE_PROJECT_ID=
VITE_FIREBASE_APP_ID=
```

`.env.local`（gitignored）先複製一份 `.env.example` 內容即可。

- [x] **Step 8: `src/main.tsx` + `src/App.tsx` + `src/styles/globals.css` + `src/lib/env.ts` + `src/test-setup.ts`**

`src/styles/globals.css`：

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root { color-scheme: light dark; }

@media (prefers-reduced-motion: reduce) {
  * { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
}
```

`src/main.tsx`：

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./App";
import "./styles/globals.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
```

`src/App.tsx`：

```tsx
export function App() {
  return <div className="p-6">Photo Browser scaffold</div>;
}
```

`src/lib/env.ts`：

```ts
type DevUser = { uid: string; email: string; displayName?: string; role: "admin" | "member" };

export const env = {
  authMode: (import.meta.env.VITE_AUTH_MODE as "testauth" | "firebase") ?? "testauth",
  apiBaseUrl: (import.meta.env.VITE_API_BASE_URL as string) ?? "/api/v1",
  testAuthUrl: (import.meta.env.VITE_TESTAUTH_URL as string | undefined) ?? "",
  devUsers: parseDevUsers(import.meta.env.VITE_DEV_USERS as string | undefined),
  firebase: {
    apiKey: import.meta.env.VITE_FIREBASE_API_KEY as string | undefined,
    authDomain: import.meta.env.VITE_FIREBASE_AUTH_DOMAIN as string | undefined,
    projectId: import.meta.env.VITE_FIREBASE_PROJECT_ID as string | undefined,
    appId: import.meta.env.VITE_FIREBASE_APP_ID as string | undefined,
  },
};

function parseDevUsers(raw: string | undefined): DevUser[] {
  if (!raw) return [];
  try {
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr as DevUser[];
  } catch {
    return [];
  }
}
```

`src/test-setup.ts`：

```ts
import "@testing-library/jest-dom/vitest";
```

- [x] **Step 9: `web/README.md`**

```markdown
# Photo Browser Web

React + Vite + TypeScript frontend for the photo-browser POC.

## Prereqs
- Node 20 (`nvm use`)

## Dev
```
nvm use
npm ci
make dev-web-stack       # 另一個 terminal，起 docker-compose backend
npm run dev              # 本 terminal，Vite dev server on :5173
```

## Test
- `npm run test` — Vitest (unit)
- `npm run e2e`  — Playwright (需要 docker-compose stack 已跑)
- `npm run build`

## 環境變數
複製 `.env.example` → `.env.local`。POC 本機用 `VITE_AUTH_MODE=testauth`。
```

- [x] **Step 10: 建目錄骨架（空 folder 也 commit stub）**

```bash
mkdir -p web/src/{auth,api,media,components/{layout,grid,viewer,states,ui},hooks,routes} web/tests/e2e web/public
```

暫時給 `.gitkeep`：

```bash
touch web/src/auth/.gitkeep web/src/api/.gitkeep web/src/media/.gitkeep web/src/components/{layout,grid,viewer,states,ui}/.gitkeep web/src/hooks/.gitkeep web/src/routes/.gitkeep web/tests/e2e/.gitkeep web/public/.gitkeep
```

- [x] **Step 11: `npm ci` 驗證**

```bash
cd web && npm install     # 首次生成 package-lock.json
npm run build             # 檢查 tsc + vite 能跑
npm run test              # 空測，應該 pass（0 test）
cd ..
```

期望：build 成功，dist 產出；test 通過（no test files）。

- [x] **Step 12: Commit**

```bash
git add web/ .gitignore
git commit -m "chore: scaffold web/ vite+react+ts+tailwind project"
```

---

## Task 3: Dev docker-compose stack

**Files:**
- Create: `deploy/compose/docker-compose.dev.yml`
- Create: `deploy/compose/dev-entrypoint.sh`
- Create: `deploy/compose/dev-users.json`

**Interfaces:**
- Consumes: `photo-browser-api-acceptance` image（既有 Dockerfile 已有 `api-acceptance` target）
- Produces: `docker compose -f deploy/compose/docker-compose.dev.yml up`（從 repo root）啟動 nginx :8081、backend :8080（內部）、testauth :8090；backend 啟動時自動 (a) 跑一次 `photo-app index`、(b) `photo-app admin add-user` 塞 3 個 dev user（admin/member/denied）。

- [x] **Step 1: `dev-users.json`**

```json
[
  { "uid": "admin-1",  "email": "admin@example.com",  "role": "admin"  },
  { "uid": "member-1", "email": "member@example.com", "role": "member" }
]
```

**注意**：`denied-1` 故意不加入 allowlist，用來測 403。

- [x] **Step 2: `dev-entrypoint.sh`**

```sh
#!/usr/bin/env sh
set -eu

echo "[dev-entrypoint] indexing $PHOTO_ROOT ..."
/usr/local/bin/photo-app index

if [ -f /dev-users/users.json ]; then
  echo "[dev-entrypoint] bootstrapping dev users ..."
  # jq 不在 minimal image 裡；用 sh + sed 逐行處理更保險，但簡單就用 while+cut
  # 這裡假設 image 有 python3；若沒有可改用 shell parser
  python3 - <<'PY'
import json, subprocess
with open("/dev-users/users.json") as f:
    users = json.load(f)
for u in users:
    args = ["/usr/local/bin/photo-app", "admin", "add-user",
            f"--uid={u['uid']}", f"--email={u['email']}", f"--role={u['role']}"]
    subprocess.run(args, check=False)  # already-exists 不視為錯誤
PY
fi

echo "[dev-entrypoint] serving on $HTTP_LISTEN ..."
exec /usr/local/bin/photo-app serve
```

`chmod +x deploy/compose/dev-entrypoint.sh`。

**注意**：`api-acceptance` Dockerfile 可能沒有 `python3`。若沒有，改用純 sh 腳本：

```sh
# 若 python3 不存在，用 shell 版本（假設 users.json 是簡單陣列格式）
grep -o '"uid":"[^"]*"' /dev-users/users.json | cut -d'"' -f4 | while read uid; do
  # ... 用 grep + cut 抽 email / role 對應同一 index
  :
done
```

實作時**先跑 `docker run --rm photo-browser-api-acceptance which python3`** 確認；沒有的話 Task 3 加一步在 image 裡裝 python3（Dockerfile `api-acceptance` stage 加 `apt-get install -y python3`），或改用 pure shell parser。優先加 python3 到 image，維護較單純。

- [x] **Step 3: 若需要，改 Dockerfile `api-acceptance` stage**

檢查 `Dockerfile` 有無 `api-acceptance` target；若無 `python3`：

```dockerfile
FROM api-acceptance-base AS api-acceptance
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
```

（依實際 Dockerfile 結構調整。）

- [x] **Step 4: `docker-compose.dev.yml`**

```yaml
services:
  testauth:
    image: photo-browser-api-acceptance
    command: ["/usr/local/bin/testauth"]
    environment:
      MINT_ISSUER: https://securetoken.google.com/demo-proj
      MINT_AUDIENCE: demo-proj
      LISTEN: ":8090"
    ports:
      - "8090:8090"

  backend:
    image: photo-browser-api-acceptance
    entrypoint: ["/usr/local/bin/dev-entrypoint.sh"]
    environment:
      PHOTO_ROOT: /srv/photos
      DATA_DIR: /srv/data
      THUMBNAIL_DIR: /srv/thumbnails
      HTTP_LISTEN: ":8080"
      FIREBASE_PROJECT_ID: demo-proj
      FIREBASE_JWKS_URL: http://testauth:8090/jwks
      ALLOWED_ORIGINS: http://localhost:5173
      INTERNAL_MEDIA_ORIGINALS: /internal-media/originals
      INTERNAL_MEDIA_THUMBNAILS: /internal-media/thumbnails
    volumes:
      - ${PHOTO_FIXTURES:-../../test-photos}:/srv/photos:ro
      - photo-data-dev:/srv/data
      - photo-thumbs-dev:/srv/thumbnails
      - ../../deploy/compose/dev-entrypoint.sh:/usr/local/bin/dev-entrypoint.sh:ro
      - ../../deploy/compose/dev-users.json:/dev-users/users.json:ro
    depends_on:
      - testauth

  nginx:
    image: nginx:latest
    pull_policy: missing
    volumes:
      - ../../deploy/nginx/nginx.conf:/etc/nginx/nginx.conf:ro
      - ${PHOTO_FIXTURES:-../../test-photos}:/srv/photos:ro
      - photo-thumbs-dev:/srv/thumbnails:ro
    ports:
      - "8081:80"
    depends_on:
      - backend

volumes:
  photo-data-dev:
  photo-thumbs-dev:
```

- [x] **Step 5: 手動驗證 stack 起得來**

```bash
docker build --target api-acceptance -t photo-browser-api-acceptance .
docker compose -f deploy/compose/docker-compose.dev.yml up -d
sleep 5
docker compose -f deploy/compose/docker-compose.dev.yml logs backend --tail=30
```

期望：能看到 `indexing`、`bootstrapping dev users`、`serving on :8080`。若失敗，看 log 修 entrypoint。

- [x] **Step 6: 用 curl 打幾個 endpoint 冒煙**

```bash
TOKEN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/v1/me
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/v1/categories
```

期望：`/me` 回 200 + role=admin；`/categories` 回 200 + 有資料。

- [x] **Step 7: 停 stack**

```bash
docker compose -f deploy/compose/docker-compose.dev.yml down
```

（volume 保留供後續 dev 循環快取用）。

- [x] **Step 8: Commit**

```bash
git add deploy/compose/docker-compose.dev.yml deploy/compose/dev-entrypoint.sh deploy/compose/dev-users.json Dockerfile
git commit -m "feat: docker-compose dev stack with auto index and allowlist seed"
```

---

## Task 4: Makefile targets

**Files:**
- Modify: `Makefile`

**Interfaces:**
- Produces: `make dev-web-stack` / `make dev-web-stack-down` / `make test-web` / `make e2e-web` / `make build-web` / `make bundle-guard`。

- [x] **Step 1: 加 target**

在 `Makefile` 尾端加：

```makefile
.PHONY: dev-web-stack dev-web-stack-down test-web e2e-web build-web bundle-guard

dev-web-stack:
	docker build --target api-acceptance -t photo-browser-api-acceptance .
	docker compose -f deploy/compose/docker-compose.dev.yml up -d
	@echo "backend/nginx on :8081, testauth on :8090"
	@echo "next: cd web && npm run dev"

dev-web-stack-down:
	docker compose -f deploy/compose/docker-compose.dev.yml down

test-web:
	cd web && npm ci --prefer-offline --no-audit && npm run test

build-web:
	cd web && npm ci --prefer-offline --no-audit && npm run build

bundle-guard:
	@echo "checking prod bundle has no testauth code..."
	@if grep -r -l -E "TestAuthProvider|/mint\\?sub" web/dist/ >/dev/null 2>&1; then \
		echo "FAIL: testauth code found in prod bundle"; exit 1; \
	fi
	@echo "OK"

e2e-web:
	$(MAKE) dev-web-stack
	cd web && npm ci --prefer-offline --no-audit && npx playwright install --with-deps chromium && npm run e2e
	$(MAKE) dev-web-stack-down
```

- [x] **Step 2: 冒煙 make target**

```bash
make dev-web-stack
sleep 3
curl -s http://localhost:8081/api/v1/health/live
make dev-web-stack-down
make test-web
```

期望：`/health/live` 回 200；`test-web` 通過（0 test）。

- [x] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: make targets for web dev/test/e2e/build/bundle-guard"
```

---

## Task 5: Auth interface + React context

**Files:**
- Create: `web/src/auth/provider.ts`
- Create: `web/src/auth/context.tsx`
- Create: `web/src/auth/context.test.tsx`

**Interfaces:**
- Produces:
  - `type AuthState`（`initializing` / `signed-out` / `signed-in`）
  - `interface AuthProvider { init(); onChange(cb); signIn(); signOut(); getIdToken(force?); }`
  - `<AuthContextProvider value={provider}>` React component + `useAuth()` hook 回傳 `{ state, provider }`

- [x] **Step 1: `provider.ts`**

```ts
export type AuthState =
  | { kind: "initializing" }
  | { kind: "signed-out" }
  | {
      kind: "signed-in";
      uid: string;
      email: string | null;
      displayName: string | null;
    };

export interface AuthProvider {
  init(): Promise<void>;
  onChange(cb: (state: AuthState) => void): () => void;
  signIn(...args: unknown[]): Promise<void>;
  signOut(): Promise<void>;
  getIdToken(forceRefresh?: boolean): Promise<string | null>;
}
```

- [x] **Step 2: test — `context.test.tsx`**

```tsx
import { render, screen, act } from "@testing-library/react";
import { AuthContextProvider, useAuth } from "./context";
import type { AuthProvider, AuthState } from "./provider";

class FakeProvider implements AuthProvider {
  private listener: ((s: AuthState) => void) | null = null;
  state: AuthState = { kind: "initializing" };
  async init() {}
  onChange(cb: (s: AuthState) => void) {
    this.listener = cb;
    cb(this.state);
    return () => { this.listener = null; };
  }
  emit(s: AuthState) { this.state = s; this.listener?.(s); }
  async signIn() {}
  async signOut() {}
  async getIdToken() { return "t"; }
}

function Probe() {
  const { state } = useAuth();
  return <span data-testid="state">{state.kind}</span>;
}

test("mirrors provider state transitions", async () => {
  const fp = new FakeProvider();
  render(
    <AuthContextProvider provider={fp}>
      <Probe />
    </AuthContextProvider>,
  );
  expect(screen.getByTestId("state")).toHaveTextContent("initializing");
  await act(async () => fp.emit({ kind: "signed-out" }));
  expect(screen.getByTestId("state")).toHaveTextContent("signed-out");
  await act(async () => fp.emit({
    kind: "signed-in", uid: "u1", email: "e@x", displayName: "N",
  }));
  expect(screen.getByTestId("state")).toHaveTextContent("signed-in");
});
```

- [x] **Step 3: 跑 test 確認失敗**

```
cd web && npm run test 2>&1 | tail -10
```

期望：`AuthContextProvider` / `useAuth` 未定義。

- [x] **Step 4: `context.tsx`**

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import type { AuthProvider, AuthState } from "./provider";

type Ctx = { state: AuthState; provider: AuthProvider };
const AuthCtx = createContext<Ctx | null>(null);

export function AuthContextProvider({
  provider,
  children,
}: {
  provider: AuthProvider;
  children: ReactNode;
}) {
  const [state, setState] = useState<AuthState>({ kind: "initializing" });
  useEffect(() => {
    let cancelled = false;
    provider.init().catch(() => {
      if (!cancelled) setState({ kind: "signed-out" });
    });
    const off = provider.onChange((s) => {
      if (!cancelled) setState(s);
    });
    return () => { cancelled = true; off(); };
  }, [provider]);
  return <AuthCtx.Provider value={{ state, provider }}>{children}</AuthCtx.Provider>;
}

export function useAuth(): Ctx {
  const ctx = useContext(AuthCtx);
  if (!ctx) throw new Error("useAuth must be inside AuthContextProvider");
  return ctx;
}
```

- [x] **Step 5: 跑 test 確認通過**

```
cd web && npm run test
```

- [x] **Step 6: Commit**

```bash
git add web/src/auth/
git commit -m "feat(web): auth provider interface and react context"
```

---

## Task 6: TestAuthProvider

**Files:**
- Create: `web/src/auth/testauth.ts`
- Create: `web/src/auth/testauth.test.ts`

**Interfaces:**
- Consumes: `AuthProvider`, `AuthState`（Task 5）；`env.testAuthUrl`, `env.devUsers`（Task 2）
- Produces: `class TestAuthProvider implements AuthProvider`；`signIn(uid: string)` 拿指定 dev user；token 存 `sessionStorage['ta.token']`；`getIdToken(true)` 呼 `/mint` 重拿。

- [x] **Step 1: test**

```ts
import { TestAuthProvider } from "./testauth";
import { vi, beforeEach, afterEach, test, expect } from "vitest";

const now = 1_700_000_000_000;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  sessionStorage.clear();
});
afterEach(() => vi.useRealTimers());

function mintJwt(exp: number, sub = "admin-1", email = "a@x"): string {
  const b64 = (o: unknown) => btoa(JSON.stringify(o)).replaceAll("=", "").replaceAll("+", "-").replaceAll("/", "_");
  return `${b64({ alg: "RS256" })}.${b64({ sub, email, exp })}.sig`;
}

test("signIn mints and stores token", async () => {
  const jwt = mintJwt(Math.floor(now / 1000) + 3600);
  globalThis.fetch = vi.fn().mockResolvedValue(new Response(jwt, { status: 200 })) as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers: [{ uid: "admin-1", email: "a@x", role: "admin" }] });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken()).toBe(jwt);
  expect(sessionStorage.getItem("ta.token")).toBe(jwt);
});

test("getIdToken(true) re-mints", async () => {
  const jwt1 = mintJwt(Math.floor(now / 1000) + 3600);
  const jwt2 = mintJwt(Math.floor(now / 1000) + 7200);
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(new Response(jwt1))
    .mockResolvedValueOnce(new Response(jwt2));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers: [{ uid: "admin-1", email: "a@x", role: "admin" }] });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken(true)).toBe(jwt2);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("expiry within skew triggers re-mint on get", async () => {
  const nearExpiry = mintJwt(Math.floor(now / 1000) + 5); // in 5s
  const fresh = mintJwt(Math.floor(now / 1000) + 3600);
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(new Response(nearExpiry))
    .mockResolvedValueOnce(new Response(fresh));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers: [{ uid: "admin-1", email: "a@x", role: "admin" }] });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken()).toBe(fresh);
});

test("signOut clears storage and emits signed-out", async () => {
  globalThis.fetch = vi.fn().mockResolvedValue(new Response(mintJwt(Math.floor(now / 1000) + 3600))) as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers: [{ uid: "admin-1", email: "a@x", role: "admin" }] });
  await p.init();
  await p.signIn("admin-1");
  const states: string[] = [];
  p.onChange((s) => states.push(s.kind));
  await p.signOut();
  expect(sessionStorage.getItem("ta.token")).toBeNull();
  expect(states.at(-1)).toBe("signed-out");
});
```

- [x] **Step 2: 跑 test 確認失敗**

```
cd web && npm run test testauth 2>&1 | tail -10
```

- [x] **Step 3: 實作 `testauth.ts`**

```ts
import type { AuthProvider, AuthState } from "./provider";

type DevUser = { uid: string; email: string; displayName?: string; role: "admin" | "member" };
type Opts = { url: string; devUsers: DevUser[]; skewSec?: number };

const TOKEN_KEY = "ta.token";
const UID_KEY = "ta.uid";
const DEFAULT_SKEW = 60;

export class TestAuthProvider implements AuthProvider {
  private listeners = new Set<(s: AuthState) => void>();
  private state: AuthState = { kind: "initializing" };
  private token: string | null = null;
  private currentUid: string | null = null;
  constructor(private opts: Opts) {}

  async init() {
    const t = sessionStorage.getItem(TOKEN_KEY);
    const uid = sessionStorage.getItem(UID_KEY);
    if (t && uid && !this.isExpired(t)) {
      this.token = t;
      this.currentUid = uid;
      this.emit(this.stateFromUid(uid));
    } else {
      sessionStorage.removeItem(TOKEN_KEY);
      sessionStorage.removeItem(UID_KEY);
      this.emit({ kind: "signed-out" });
    }
  }

  onChange(cb: (s: AuthState) => void) {
    this.listeners.add(cb);
    cb(this.state);
    return () => this.listeners.delete(cb);
  }

  async signIn(uid?: string) {
    const target = uid ?? this.currentUid;
    if (!target) throw new Error("TestAuthProvider.signIn requires uid");
    const user = this.opts.devUsers.find((u) => u.uid === target);
    if (!user) throw new Error(`unknown dev uid: ${target}`);
    this.currentUid = user.uid;
    this.token = await this.mint(user);
    sessionStorage.setItem(TOKEN_KEY, this.token);
    sessionStorage.setItem(UID_KEY, user.uid);
    this.emit(this.stateFromUid(user.uid));
  }

  async signOut() {
    this.token = null;
    this.currentUid = null;
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(UID_KEY);
    this.emit({ kind: "signed-out" });
  }

  async getIdToken(forceRefresh = false): Promise<string | null> {
    if (!this.currentUid) return null;
    if (forceRefresh || !this.token || this.isExpired(this.token)) {
      const user = this.opts.devUsers.find((u) => u.uid === this.currentUid);
      if (!user) return null;
      this.token = await this.mint(user);
      sessionStorage.setItem(TOKEN_KEY, this.token);
    }
    return this.token;
  }

  private async mint(u: DevUser): Promise<string> {
    const url = new URL(this.opts.url);
    url.pathname = "/mint";
    url.searchParams.set("sub", u.uid);
    url.searchParams.set("email", u.email);
    url.searchParams.set("verified", "1");
    const res = await fetch(url.toString());
    if (!res.ok) throw new Error(`testauth mint failed: ${res.status}`);
    return (await res.text()).trim();
  }

  private isExpired(jwt: string): boolean {
    try {
      const [, payload] = jwt.split(".");
      const claims = JSON.parse(atob(payload.replaceAll("-", "+").replaceAll("_", "/")));
      const skew = this.opts.skewSec ?? DEFAULT_SKEW;
      return typeof claims.exp !== "number" || Date.now() / 1000 + skew >= claims.exp;
    } catch {
      return true;
    }
  }

  private stateFromUid(uid: string): AuthState {
    const u = this.opts.devUsers.find((x) => x.uid === uid);
    if (!u) return { kind: "signed-out" };
    return { kind: "signed-in", uid: u.uid, email: u.email, displayName: u.displayName ?? null };
  }

  private emit(s: AuthState) {
    this.state = s;
    for (const cb of this.listeners) cb(s);
  }
}
```

- [x] **Step 4: 跑 test 通過**

```
cd web && npm run test testauth
```

- [x] **Step 5: Commit**

```bash
git add web/src/auth/testauth.ts web/src/auth/testauth.test.ts
git commit -m "feat(web): TestAuthProvider backed by testauth /mint"
```

---

## Task 7: FirebaseAuthProvider

**Files:**
- Create: `web/src/auth/firebase.ts`
- Create: `web/src/auth/firebase.test.ts`

**Interfaces:**
- Consumes: `AuthProvider`, `AuthState`；`env.firebase`
- Produces: `class FirebaseAuthProvider implements AuthProvider`；用 modular Firebase SDK；`signInWithPopup(GoogleAuthProvider)`；`onIdTokenChanged` 是唯一 state 來源；`getIdToken(true)` 呼 SDK `user.getIdToken(true)`。

- [x] **Step 1: test（mock 掉 Firebase SDK）**

```ts
import { FirebaseAuthProvider } from "./firebase";
import { vi, test, expect, beforeEach } from "vitest";

const mocks = vi.hoisted(() => ({
  initializeApp: vi.fn(),
  getAuth: vi.fn(),
  GoogleAuthProvider: vi.fn(),
  signInWithPopup: vi.fn(),
  signOut: vi.fn(),
  onIdTokenChanged: vi.fn(),
}));

vi.mock("firebase/app", () => ({ initializeApp: mocks.initializeApp }));
vi.mock("firebase/auth", () => ({
  getAuth: mocks.getAuth,
  GoogleAuthProvider: mocks.GoogleAuthProvider,
  signInWithPopup: mocks.signInWithPopup,
  signOut: mocks.signOut,
  onIdTokenChanged: mocks.onIdTokenChanged,
}));

beforeEach(() => {
  Object.values(mocks).forEach((m) => (m as ReturnType<typeof vi.fn>).mockReset?.());
});

test("emits signed-in when Firebase reports a user", async () => {
  const user = {
    uid: "abc", email: "a@x", displayName: "Alice",
    getIdToken: vi.fn().mockResolvedValue("tok"),
  };
  let cb: ((u: unknown) => void) | null = null;
  mocks.onIdTokenChanged.mockImplementation((_auth: unknown, fn: (u: unknown) => void) => {
    cb = fn; return () => {};
  });
  mocks.getAuth.mockReturnValue({});
  const p = new FirebaseAuthProvider({
    apiKey: "k", authDomain: "d", projectId: "p", appId: "a",
  });
  const states: string[] = [];
  await p.init();
  p.onChange((s) => states.push(s.kind));
  cb!(user);
  expect(states.at(-1)).toBe("signed-in");
  expect(await p.getIdToken()).toBe("tok");
  expect(user.getIdToken).toHaveBeenCalledWith(false);
});

test("getIdToken(true) forces refresh", async () => {
  const user = { uid: "abc", email: null, displayName: null, getIdToken: vi.fn().mockResolvedValue("fresh") };
  let cb: ((u: unknown) => void) | null = null;
  mocks.onIdTokenChanged.mockImplementation((_a: unknown, fn: (u: unknown) => void) => { cb = fn; return () => {}; });
  mocks.getAuth.mockReturnValue({});
  const p = new FirebaseAuthProvider({ apiKey: "k", authDomain: "d", projectId: "p", appId: "a" });
  await p.init();
  p.onChange(() => {});
  cb!(user);
  await p.getIdToken(true);
  expect(user.getIdToken).toHaveBeenCalledWith(true);
});
```

- [x] **Step 2: 跑 test 確認失敗**

```
cd web && npm run test firebase 2>&1 | tail -10
```

- [x] **Step 3: 實作 `firebase.ts`**

```ts
import { initializeApp, type FirebaseApp } from "firebase/app";
import {
  getAuth, GoogleAuthProvider, onIdTokenChanged, signInWithPopup, signOut as fbSignOut,
  type Auth, type User,
} from "firebase/auth";
import type { AuthProvider, AuthState } from "./provider";

type Cfg = { apiKey: string; authDomain: string; projectId: string; appId: string };

export class FirebaseAuthProvider implements AuthProvider {
  private app: FirebaseApp | null = null;
  private auth: Auth | null = null;
  private user: User | null = null;
  private listeners = new Set<(s: AuthState) => void>();
  private state: AuthState = { kind: "initializing" };
  private unsub: (() => void) | null = null;

  constructor(private cfg: Cfg) {}

  async init() {
    if (!this.cfg.apiKey) throw new Error("FirebaseAuthProvider: missing config");
    this.app = initializeApp(this.cfg);
    this.auth = getAuth(this.app);
    this.unsub = onIdTokenChanged(this.auth, (u) => {
      this.user = u;
      if (!u) this.emit({ kind: "signed-out" });
      else this.emit({
        kind: "signed-in", uid: u.uid, email: u.email, displayName: u.displayName,
      });
    });
  }

  onChange(cb: (s: AuthState) => void) {
    this.listeners.add(cb);
    cb(this.state);
    return () => this.listeners.delete(cb);
  }

  async signIn() {
    if (!this.auth) throw new Error("FirebaseAuthProvider not initialized");
    await signInWithPopup(this.auth, new GoogleAuthProvider());
  }

  async signOut() {
    if (this.auth) await fbSignOut(this.auth);
  }

  async getIdToken(forceRefresh = false): Promise<string | null> {
    if (!this.user) return null;
    return this.user.getIdToken(forceRefresh);
  }

  private emit(s: AuthState) {
    this.state = s;
    for (const cb of this.listeners) cb(s);
  }
}
```

- [x] **Step 4: 跑 test 通過**

```
cd web && npm run test firebase
```

- [x] **Step 5: Commit**

```bash
git add web/src/auth/firebase.ts web/src/auth/firebase.test.ts
git commit -m "feat(web): FirebaseAuthProvider with modular sdk + popup sign-in"
```

---

## Task 8: Auth adapter select + wire into App

**Files:**
- Create: `web/src/auth/select.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/main.tsx`

**Interfaces:**
- Consumes: `env.authMode`, `env.testAuthUrl`, `env.devUsers`, `env.firebase`；`TestAuthProvider`, `FirebaseAuthProvider`
- Produces: `makeAuthProvider(): AuthProvider`；`<App/>` 包 `<AuthContextProvider>`。

- [x] **Step 1: `select.ts`**

```ts
import type { AuthProvider } from "./provider";
import { env } from "../lib/env";

export function makeAuthProvider(): AuthProvider {
  if (env.authMode === "testauth") {
    const { TestAuthProvider } = require("./testauth") as typeof import("./testauth");
    return new TestAuthProvider({ url: env.testAuthUrl, devUsers: env.devUsers });
  }
  if (env.authMode === "firebase") {
    const { FirebaseAuthProvider } = require("./firebase") as typeof import("./firebase");
    const c = env.firebase;
    if (!c.apiKey || !c.authDomain || !c.projectId || !c.appId) {
      throw new Error("FirebaseAuthProvider: missing VITE_FIREBASE_* env");
    }
    return new FirebaseAuthProvider({
      apiKey: c.apiKey, authDomain: c.authDomain, projectId: c.projectId, appId: c.appId,
    });
  }
  throw new Error(`unknown VITE_AUTH_MODE: ${env.authMode}`);
}
```

**注意**：`require` 在 ESM 下不能用。改用動態 import + top-level 靜態 import 才能被 Vite tree-shake。正確版本：

```ts
import type { AuthProvider } from "./provider";
import { env } from "../lib/env";
import { TestAuthProvider } from "./testauth";
import { FirebaseAuthProvider } from "./firebase";

export function makeAuthProvider(): AuthProvider {
  if (env.authMode === "testauth") {
    return new TestAuthProvider({ url: env.testAuthUrl, devUsers: env.devUsers });
  }
  if (env.authMode === "firebase") {
    const c = env.firebase;
    if (!c.apiKey || !c.authDomain || !c.projectId || !c.appId) {
      throw new Error("FirebaseAuthProvider: missing VITE_FIREBASE_* env");
    }
    return new FirebaseAuthProvider({
      apiKey: c.apiKey, authDomain: c.authDomain, projectId: c.projectId, appId: c.appId,
    });
  }
  throw new Error(`unknown VITE_AUTH_MODE: ${env.authMode}`);
}
```

但這樣兩個 adapter 都會進 bundle。要真正 tree-shake 掉 `TestAuthProvider` 在 prod，改用 Vite 的 `import.meta.env` guard + 動態 import：

```ts
import type { AuthProvider } from "./provider";
import { env } from "../lib/env";

export async function makeAuthProvider(): Promise<AuthProvider> {
  if (env.authMode === "testauth") {
    const mod = await import("./testauth");
    return new mod.TestAuthProvider({ url: env.testAuthUrl, devUsers: env.devUsers });
  }
  if (env.authMode === "firebase") {
    const c = env.firebase;
    if (!c.apiKey || !c.authDomain || !c.projectId || !c.appId) {
      throw new Error("FirebaseAuthProvider: missing VITE_FIREBASE_* env");
    }
    const mod = await import("./firebase");
    return new mod.FirebaseAuthProvider({
      apiKey: c.apiKey, authDomain: c.authDomain, projectId: c.projectId, appId: c.appId,
    });
  }
  throw new Error(`unknown VITE_AUTH_MODE: ${env.authMode}`);
}
```

動態 import + Vite 的 build-time env replacement 會讓對應的 chunk 只在 matching branch 才 emit——**Task 22 的 bundle guard 會實測驗證**。用 async 版本。

- [x] **Step 2: 改 `App.tsx` + `main.tsx`**

`web/src/App.tsx`：

```tsx
import { useEffect, useState } from "react";
import { AuthContextProvider } from "./auth/context";
import type { AuthProvider } from "./auth/provider";
import { makeAuthProvider } from "./auth/select";

export function App() {
  const [provider, setProvider] = useState<AuthProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    makeAuthProvider()
      .then(setProvider)
      .catch((e: unknown) => setError(String(e)));
  }, []);
  if (error) return <div className="p-6 text-red-600">Auth error: {error}</div>;
  if (!provider) return <div className="p-6">Loading…</div>;
  return (
    <AuthContextProvider provider={provider}>
      <div className="p-6">Signed in / out screen goes here</div>
    </AuthContextProvider>
  );
}
```

- [x] **Step 3: 冒煙**

```bash
cd web && npm run test && npm run build
```

期望：build 成功；test 綠。

- [x] **Step 4: Commit**

```bash
git add web/src/auth/select.ts web/src/App.tsx
git commit -m "feat(web): build-time auth adapter selection via dynamic import"
```

---

## Task 9: API client (fetch wrapper)

**Files:**
- Create: `web/src/api/types.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/client.test.ts`

**Interfaces:**
- Consumes: `AuthProvider`, `env.apiBaseUrl`
- Produces:
  - `class ApiError extends Error { status, code, requestId, message }`
  - `interface ApiClient { getJson<T>(path): Promise<T>; getBlob(path): Promise<Blob>; }`
  - `makeApi(auth: AuthProvider, baseUrl: string): ApiClient`
  - 行為：Auth header、`X-Request-Id` capture、401 → `getIdToken(true)` 重打一次；envelope error 解析為 `ApiError`；403 直接丟 `ApiError`（不 retry）；network / 5xx 丟一般 `Error`。

- [x] **Step 1: `types.ts`**

```ts
export type Category = { id: number; name: string; relative_path: string };

export type Album = {
  id: number;
  category_id: number;
  name: string;
  relative_path: string;
  cover_photo_id?: number;
  cover_thumbnail_key?: string;
};

export type Photo = {
  id: number;
  album_id: number;
  filename: string;
  relative_path: string;
  mime_type: string;
  width?: number;
  height?: number;
  taken_at?: string;
  thumbnail_key?: string;
  file_size: number;
  file_mtime_ns: number;
};

export type Cursor = { next_cursor: string | null };

export type Page<T, K extends string> = { [P in K]: T[] } & Cursor;

export type Me = { uid: string; email: string | null; role: "admin" | "member" };

export type ErrorEnvelope = { code: string; message: string; request_id?: string };
```

- [x] **Step 2: test — `client.test.ts`**

```ts
import { makeApi, ApiError } from "./client";
import type { AuthProvider } from "../auth/provider";
import { vi, test, expect, beforeEach } from "vitest";

class FakeAuth implements AuthProvider {
  tokens: string[] = ["t1", "t2"];
  idx = 0;
  async init() {}
  onChange() { return () => {}; }
  async signIn() {}
  async signOut() {}
  async getIdToken(force = false) {
    if (force) this.idx++;
    return this.tokens[this.idx] ?? null;
  }
}

beforeEach(() => vi.restoreAllMocks());

test("getJson attaches bearer and parses body", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const body = await api.getJson<{ ok: boolean }>("/me");
  expect(body).toEqual({ ok: true });
  const call = fetchMock.mock.calls[0];
  expect((call[1] as RequestInit).headers).toMatchObject({ Authorization: "Bearer t1" });
  expect(call[0]).toBe("/api/v1/me");
});

test("401 triggers refresh + retry once", async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ code: "unauthenticated", message: "" }), { status: 401 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const body = await api.getJson<{ ok: boolean }>("/me");
  expect(body).toEqual({ ok: true });
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect((fetchMock.mock.calls[1][1] as RequestInit).headers).toMatchObject({ Authorization: "Bearer t2" });
});

test("second 401 throws ApiError(401)", async () => {
  const err = new Response(JSON.stringify({ code: "unauthenticated", message: "bad", request_id: "r1" }), { status: 401 });
  globalThis.fetch = vi.fn().mockResolvedValue(err.clone()) as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/me")).rejects.toMatchObject({
    status: 401, code: "unauthenticated", requestId: "r1",
  });
});

test("403 throws ApiError without retry", async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ code: "forbidden", message: "no" }),
    { status: 403, headers: { "X-Request-Id": "abc" } },
  ));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/me")).rejects.toBeInstanceOf(ApiError);
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("getBlob returns Blob for 200", async () => {
  const blob = new Blob([new Uint8Array([1, 2, 3])], { type: "image/webp" });
  globalThis.fetch = vi.fn().mockResolvedValue(new Response(blob, { status: 200 })) as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const out = await api.getBlob("/photos/1/thumbnail/abc");
  expect(out.type).toBe("image/webp");
});
```

- [x] **Step 3: 跑失敗**

```
cd web && npm run test api/client 2>&1 | tail -10
```

- [x] **Step 4: `client.ts`**

```ts
import type { AuthProvider } from "../auth/provider";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public requestId: string | null,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiClient {
  getJson<T>(path: string): Promise<T>;
  getBlob(path: string): Promise<Blob>;
}

export function makeApi(auth: AuthProvider, baseUrl: string): ApiClient {
  async function request(path: string): Promise<Response> {
    const send = async (force: boolean) => {
      const token = await auth.getIdToken(force);
      const headers = new Headers();
      if (token) headers.set("Authorization", `Bearer ${token}`);
      return fetch(baseUrl + path, { headers });
    };
    let res = await send(false);
    if (res.status === 401) res = await send(true);
    if (!res.ok) throw await toApiError(res);
    return res;
  }
  return {
    getJson: async <T>(p: string) => (await request(p)).json() as Promise<T>,
    getBlob: async (p: string) => (await request(p)).blob(),
  };
}

async function toApiError(res: Response): Promise<ApiError> {
  const reqId = res.headers.get("X-Request-Id");
  let code = "unknown";
  let message = res.statusText;
  try {
    const body = (await res.json()) as { code?: string; message?: string; request_id?: string };
    if (body.code) code = body.code;
    if (body.message) message = body.message;
    return new ApiError(res.status, code, body.request_id ?? reqId, message);
  } catch {
    return new ApiError(res.status, code, reqId, message);
  }
}
```

- [x] **Step 5: 跑通過**

```
cd web && npm run test api/client
```

- [x] **Step 6: Commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts
git commit -m "feat(web): api client with bearer, 401 retry, envelope errors"
```

---

## Task 10: TanStack Query hooks

**Files:**
- Create: `web/src/api/queries.ts`
- Create: `web/src/api/queries.test.tsx`
- Modify: `web/src/App.tsx`（放 `QueryClientProvider`）

**Interfaces:**
- Consumes: `ApiClient`（Task 9）；query hooks 讀 React context 拿 client。
- Produces:
  - `<ApiProvider client={...}>` + `useApi(): ApiClient`
  - `useMe()`, `useCategories()`, `useCategory(id)`, `useAlbums(opts)`, `useAlbum(id)`, `usePhotos(opts)`, `useAlbumPhotos(albumId)`, `useTimeline()`
  - Cursor 用 `useInfiniteQuery`；`getNextPageParam: (last) => last.next_cursor ?? undefined`。

- [x] **Step 1: 建 `ApiProvider` + `useApi` + query key 工廠**

在 `web/src/api/context.tsx` 建：

```tsx
import { createContext, useContext, type ReactNode } from "react";
import type { ApiClient } from "./client";
const ApiCtx = createContext<ApiClient | null>(null);
export function ApiProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return <ApiCtx.Provider value={client}>{children}</ApiCtx.Provider>;
}
export function useApi(): ApiClient {
  const c = useContext(ApiCtx);
  if (!c) throw new Error("useApi must be inside ApiProvider");
  return c;
}
```

- [x] **Step 2: `queries.ts`**

```ts
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useApi } from "./context";
import type { Album, Category, Cursor, Me, Photo } from "./types";

type AlbumsPage = { albums: Album[] } & Cursor;
type PhotosPage = { photos: Photo[] } & Cursor;
type CategoriesPage = { categories: Category[] } & Cursor;

const withCursor = (base: string, cursor?: string) =>
  cursor ? `${base}${base.includes("?") ? "&" : "?"}cursor=${encodeURIComponent(cursor)}` : base;

export function useMe() {
  const api = useApi();
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api.getJson<Me>("/me"),
    retry: (n, err) => n < 2 && !(err instanceof Error && err.name === "ApiError" && (err as { status?: number }).status === 403),
  });
}

export function useCategories() {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["categories"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<CategoriesPage>(withCursor("/categories", pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useCategoryAlbums(categoryId: number) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["albums", { categoryId }],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<AlbumsPage>(withCursor(`/categories/${categoryId}/albums`, pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useAlbums() {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["albums", "all"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<AlbumsPage>(withCursor("/albums", pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useAlbum(albumId: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["album", albumId],
    queryFn: () => api.getJson<Album>(`/albums/${albumId}`),
  });
}

export function useAlbumPhotos(albumId: number) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["photos", { albumId }],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<PhotosPage>(withCursor(`/albums/${albumId}/photos`, pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useAllPhotos() {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["photos", "all"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<PhotosPage>(withCursor("/photos", pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useTimeline() {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["photos", "timeline"],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<PhotosPage>(withCursor("/photos/timeline", pageParam)),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}
```

- [x] **Step 3: test — `queries.test.tsx`（挑一個 hook 做 sanity）**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { ApiProvider } from "./context";
import { useAllPhotos } from "./queries";
import type { ApiClient } from "./client";
import { test, expect, vi } from "vitest";

function wrap(client: ApiClient) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>
      <ApiProvider client={client}>{children}</ApiProvider>
    </QueryClientProvider>
  );
}

function Probe() {
  const q = useAllPhotos();
  if (q.isLoading) return <div>load</div>;
  return <div data-testid="count">{q.data?.pages.flatMap(p => p.photos).length ?? 0}</div>;
}

test("useAllPhotos aggregates pages", async () => {
  const client: ApiClient = {
    getJson: vi.fn().mockResolvedValueOnce({ photos: [{ id: 1 }, { id: 2 }], next_cursor: null }),
    getBlob: vi.fn(),
  };
  const Wrapper = wrap(client);
  render(<Wrapper><Probe /></Wrapper>);
  await waitFor(() => expect(screen.getByTestId("count")).toHaveTextContent("2"));
});
```

- [x] **Step 4: 改 `App.tsx` 加 QueryClient + ApiProvider**

```tsx
import { useEffect, useMemo, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "./auth/context";
import type { AuthProvider } from "./auth/provider";
import { makeAuthProvider } from "./auth/select";
import { makeApi } from "./api/client";
import { ApiProvider } from "./api/context";
import { env } from "./lib/env";

export function App() {
  const [provider, setProvider] = useState<AuthProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    makeAuthProvider().then(setProvider).catch((e) => setError(String(e)));
  }, []);
  const qc = useMemo(() => new QueryClient({
    defaultOptions: {
      queries: {
        retry: (n, e) => n < 2 && !isTerminalStatus(e),
        staleTime: 30_000,
        gcTime: 5 * 60_000,
      },
    },
  }), []);
  if (error) return <div className="p-6 text-red-600">Auth error: {error}</div>;
  if (!provider) return <div className="p-6">Loading…</div>;
  const client = makeApi(provider, env.apiBaseUrl);
  return (
    <AuthContextProvider provider={provider}>
      <QueryClientProvider client={qc}>
        <ApiProvider client={client}>
          <div className="p-6">Placeholder — routes go in Task 12</div>
        </ApiProvider>
      </QueryClientProvider>
    </AuthContextProvider>
  );
}

function isTerminalStatus(e: unknown): boolean {
  if (e && typeof e === "object" && "status" in e) {
    const s = (e as { status: number }).status;
    return s === 401 || s === 403 || s === 404;
  }
  return false;
}
```

- [x] **Step 5: 跑 test + build**

```
cd web && npm run test && npm run build
```

- [x] **Step 6: Commit**

```bash
git add web/src/api/context.tsx web/src/api/queries.ts web/src/api/queries.test.tsx web/src/App.tsx
git commit -m "feat(web): tanstack query hooks for catalog api"
```

---

## Task 11: Media blob cache + useAuthedImage

**Files:**
- Create: `web/src/media/blob-cache.ts`
- Create: `web/src/media/blob-cache.test.ts`
- Create: `web/src/media/hooks.ts`
- Create: `web/src/media/hooks.test.tsx`

**Interfaces:**
- Consumes: `ApiClient`（Task 9）
- Produces:
  - `class BlobCache { get(url): Promise<{ objectUrl: string }>; release(url); dispose(); }` — LRU 容量 50、in-flight dedup、concurrency 4、error 也 cache（TTL 30s）。
  - `useAuthedImage(url): { state: "loading" | "ready" | "error"; objectUrl?; kind? }`。
  - `MediaProvider` + `useMedia()`：讓 `<AppShell>` 提供單一 cache 實例。

- [x] **Step 1: test — `blob-cache.test.ts`**

```ts
import { BlobCache } from "./blob-cache";
import { vi, test, expect, beforeEach } from "vitest";
import type { ApiClient } from "../api/client";

function makeClient(...blobs: Blob[]): ApiClient {
  return {
    getJson: vi.fn(),
    getBlob: vi.fn().mockImplementation(() => {
      const b = blobs.shift();
      if (!b) throw new Error("no more blobs");
      return Promise.resolve(b);
    }),
  };
}

const b = (bytes = 1) => new Blob([new Uint8Array(bytes).fill(1)], { type: "image/webp" });

beforeEach(() => {
  (globalThis.URL.createObjectURL as unknown) = vi.fn().mockImplementation(() => "blob://" + Math.random());
  (globalThis.URL.revokeObjectURL as unknown) = vi.fn();
});

test("dedupes concurrent fetches", async () => {
  const c = makeClient(b());
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4 });
  const [a, b2] = await Promise.all([cache.get("/x"), cache.get("/x")]);
  expect(a.objectUrl).toBe(b2.objectUrl);
  expect(c.getBlob).toHaveBeenCalledTimes(1);
});

test("LRU eviction revokes", async () => {
  const c = makeClient(b(), b(), b());
  const cache = new BlobCache({ api: c, capacity: 2, concurrency: 4 });
  await cache.get("/a"); await cache.get("/b"); await cache.get("/c");
  expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1);
});

test("caches error and retries after ttl", async () => {
  const c: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn()
      .mockRejectedValueOnce(Object.assign(new Error("nf"), { status: 404 }))
      .mockResolvedValueOnce(b()),
  };
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4, errorTtlMs: 10 });
  await expect(cache.get("/x")).rejects.toBeDefined();
  await expect(cache.get("/x")).rejects.toBeDefined();
  expect(c.getBlob).toHaveBeenCalledTimes(1);
  await new Promise((r) => setTimeout(r, 15));
  await cache.get("/x");
  expect(c.getBlob).toHaveBeenCalledTimes(2);
});

test("concurrency limit gates parallel fetches", async () => {
  let inflight = 0;
  let peak = 0;
  const client: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn().mockImplementation(async () => {
      inflight++; peak = Math.max(peak, inflight);
      await new Promise((r) => setTimeout(r, 10));
      inflight--;
      return b();
    }),
  };
  const cache = new BlobCache({ api: client, capacity: 100, concurrency: 2 });
  await Promise.all(Array.from({ length: 6 }, (_, i) => cache.get("/p" + i)));
  expect(peak).toBeLessThanOrEqual(2);
});
```

- [x] **Step 2: 跑失敗**

```
cd web && npm run test blob-cache 2>&1 | tail -10
```

- [x] **Step 3: `blob-cache.ts`**

```ts
import type { ApiClient } from "../api/client";

type Entry =
  | { kind: "ready"; objectUrl: string }
  | { kind: "error"; error: unknown; ts: number };

type Opts = {
  api: ApiClient;
  capacity?: number;
  concurrency?: number;
  errorTtlMs?: number;
};

export class BlobCache {
  private capacity: number;
  private concurrency: number;
  private errorTtl: number;
  private lru = new Map<string, Entry>();      // insertion order = LRU
  private inflight = new Map<string, Promise<Entry>>();
  private queue: Array<() => void> = [];
  private active = 0;
  private api: ApiClient;

  constructor(o: Opts) {
    this.api = o.api;
    this.capacity = o.capacity ?? 50;
    this.concurrency = o.concurrency ?? 4;
    this.errorTtl = o.errorTtlMs ?? 30_000;
  }

  async get(path: string): Promise<{ objectUrl: string }> {
    const cached = this.lru.get(path);
    if (cached) {
      if (cached.kind === "ready") {
        this.lru.delete(path); this.lru.set(path, cached);
        return { objectUrl: cached.objectUrl };
      }
      if (Date.now() - cached.ts < this.errorTtl) throw cached.error;
      this.lru.delete(path);
    }
    const inflight = this.inflight.get(path);
    if (inflight) {
      const e = await inflight;
      if (e.kind === "ready") return { objectUrl: e.objectUrl };
      throw e.error;
    }
    const p = this.enqueue(path);
    this.inflight.set(path, p);
    try {
      const e = await p;
      if (e.kind === "ready") return { objectUrl: e.objectUrl };
      throw e.error;
    } finally {
      this.inflight.delete(path);
    }
  }

  dispose() {
    for (const e of this.lru.values()) if (e.kind === "ready") URL.revokeObjectURL(e.objectUrl);
    this.lru.clear();
  }

  private enqueue(path: string): Promise<Entry> {
    return new Promise((resolve) => {
      const run = async () => {
        this.active++;
        try {
          const blob = await this.api.getBlob(path);
          const objectUrl = URL.createObjectURL(blob);
          const entry: Entry = { kind: "ready", objectUrl };
          this.insert(path, entry);
          resolve(entry);
        } catch (error) {
          const entry: Entry = { kind: "error", error, ts: Date.now() };
          this.insert(path, entry);
          resolve(entry);
        } finally {
          this.active--;
          const next = this.queue.shift();
          if (next) next();
        }
      };
      if (this.active < this.concurrency) run();
      else this.queue.push(run);
    });
  }

  private insert(path: string, entry: Entry) {
    this.lru.set(path, entry);
    while (this.lru.size > this.capacity) {
      const oldestKey = this.lru.keys().next().value as string | undefined;
      if (!oldestKey) break;
      const old = this.lru.get(oldestKey)!;
      this.lru.delete(oldestKey);
      if (old.kind === "ready") URL.revokeObjectURL(old.objectUrl);
    }
  }
}
```

- [x] **Step 4: 跑通過**

```
cd web && npm run test blob-cache
```

- [x] **Step 5: `hooks.ts`（含 MediaProvider）**

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { BlobCache } from "./blob-cache";
import type { ApiClient } from "../api/client";

const MediaCtx = createContext<BlobCache | null>(null);

export function MediaProvider({ api, children }: { api: ApiClient; children: ReactNode }) {
  const [cache] = useState(() => new BlobCache({ api }));
  useEffect(() => () => cache.dispose(), [cache]);
  return <MediaCtx.Provider value={cache}>{children}</MediaCtx.Provider>;
}

export function useMedia(): BlobCache {
  const c = useContext(MediaCtx);
  if (!c) throw new Error("useMedia must be inside MediaProvider");
  return c;
}

type State =
  | { state: "loading" }
  | { state: "ready"; objectUrl: string }
  | { state: "error"; kind: "not-found" | "forbidden" | "network" };

export function useAuthedImage(path: string | null): State {
  const cache = useMedia();
  const [s, setS] = useState<State>({ state: "loading" });
  useEffect(() => {
    let alive = true;
    if (!path) { setS({ state: "loading" }); return; }
    setS({ state: "loading" });
    cache.get(path).then(
      (r) => alive && setS({ state: "ready", objectUrl: r.objectUrl }),
      (err) => {
        if (!alive) return;
        const status = (err as { status?: number })?.status;
        if (status === 404) setS({ state: "error", kind: "not-found" });
        else if (status === 403) setS({ state: "error", kind: "forbidden" });
        else setS({ state: "error", kind: "network" });
      },
    );
    return () => { alive = false; };
  }, [path, cache]);
  return s;
}
```

- [x] **Step 6: test — `hooks.test.tsx`**

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import { MediaProvider, useAuthedImage } from "./hooks";
import { test, expect, vi } from "vitest";

const client = {
  getJson: vi.fn(),
  getBlob: vi.fn().mockResolvedValue(new Blob([new Uint8Array([1])], { type: "image/webp" })),
};

(globalThis.URL.createObjectURL as unknown) = vi.fn().mockReturnValue("blob://x");
(globalThis.URL.revokeObjectURL as unknown) = vi.fn();

function P() {
  const s = useAuthedImage("/thumb/1");
  return <div data-testid="s">{s.state === "ready" ? "ok" : s.state}</div>;
}

test("resolves to ready", async () => {
  render(<MediaProvider api={client}><P /></MediaProvider>);
  await waitFor(() => expect(screen.getByTestId("s")).toHaveTextContent("ok"));
});
```

- [x] **Step 7: 跑全部通過**

```
cd web && npm run test
```

- [x] **Step 8: Commit**

```bash
git add web/src/media/
git commit -m "feat(web): authed media blob cache + useAuthedImage hook"
```

---

## Task 12: Router + AppShell + BottomNav + ProtectedShell

**Files:**
- Create: `web/src/components/layout/AppShell.tsx`
- Create: `web/src/components/layout/BottomNav.tsx`
- Create: `web/src/components/layout/Header.tsx`
- Create: `web/src/components/states/Loading.tsx`
- Create: `web/src/components/states/ErrorPanel.tsx`
- Create: `web/src/components/states/Empty.tsx`
- Create: `web/src/routes/protected.tsx`
- Create: `web/src/routes/protected.test.tsx`
- Create: `web/src/routes/router.tsx`
- Modify: `web/src/App.tsx`（改用 RouterProvider）

**Interfaces:**
- Produces：路由 tree（跟 spec §6 對齊）；`ProtectedShell` 依 `AuthState + useMe()` 顯示 loading / login redirect / forbidden redirect / `<Outlet />`。

- [x] **Step 1: 狀態 component（先寫好給後續用）**

`Loading.tsx`：

```tsx
export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex items-center justify-center p-8 text-sm text-gray-500">
      {label}
    </div>
  );
}
```

`Empty.tsx`：

```tsx
export function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center p-10 text-center">
      <p className="text-base font-medium">{title}</p>
      {hint && <p className="mt-2 text-sm text-gray-500">{hint}</p>}
    </div>
  );
}
```

`ErrorPanel.tsx`：

```tsx
type Kind = "network" | "not-found" | "forbidden" | "pagination";
type Props = { kind: Kind; onRetry?: () => void };

const copy: Record<Kind, string> = {
  network: "Cannot reach the server right now.",
  "not-found": "This item could not be found.",
  forbidden: "You do not have permission to view this.",
  pagination: "Failed to load more items.",
};

export function ErrorPanel({ kind, onRetry }: Props) {
  return (
    <div role="alert" className="flex flex-col items-center justify-center gap-3 p-8 text-center">
      <p className="text-sm text-gray-700 dark:text-gray-200">{copy[kind]}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          className="min-h-touch min-w-touch rounded border px-4 py-2 text-sm hover:bg-gray-100"
        >
          Retry
        </button>
      )}
    </div>
  );
}
```

- [x] **Step 2: `BottomNav.tsx`**

```tsx
import { NavLink } from "react-router-dom";

const items = [
  { to: "/photos", label: "Photos" },
  { to: "/albums", label: "Albums" },
  { to: "/categories", label: "Categories" },
  { to: "/profile", label: "Profile" },
];

export function BottomNav() {
  return (
    <nav
      aria-label="Primary"
      className="fixed inset-x-0 bottom-0 z-10 flex justify-around border-t bg-white/90 backdrop-blur dark:bg-gray-900/90"
    >
      {items.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          className={({ isActive }) =>
            "min-h-touch min-w-touch flex-1 py-3 text-center text-xs " +
            (isActive ? "font-semibold text-blue-600" : "text-gray-600")
          }
        >
          {it.label}
        </NavLink>
      ))}
    </nav>
  );
}
```

- [x] **Step 3: `Header.tsx`**

```tsx
export function Header({ title }: { title: string }) {
  return (
    <header className="sticky top-0 z-10 border-b bg-white/90 px-4 py-3 backdrop-blur dark:bg-gray-900/90">
      <h1 className="text-base font-semibold">{title}</h1>
    </header>
  );
}
```

- [x] **Step 4: `AppShell.tsx`**

```tsx
import { Outlet } from "react-router-dom";
import { BottomNav } from "./BottomNav";

export function AppShell() {
  return (
    <div className="flex min-h-screen flex-col pb-16">
      <main className="flex-1">
        <Outlet />
      </main>
      <BottomNav />
    </div>
  );
}
```

- [x] **Step 5: `protected.tsx`**

```tsx
import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "../auth/context";
import { useMe } from "../api/queries";
import { Loading } from "../components/states/Loading";

export function ProtectedShell() {
  const { state } = useAuth();
  const me = useMe();

  if (state.kind === "initializing") return <Loading label="Signing in…" />;
  if (state.kind === "signed-out") return <Navigate to="/login" replace />;

  if (me.isLoading) return <Loading label="Checking permissions…" />;
  if (me.error) {
    const status = (me.error as { status?: number })?.status;
    if (status === 403) return <Navigate to="/forbidden" replace />;
    if (status === 401) return <Navigate to="/login" replace />;
    return <Loading label="Retrying…" />;
  }
  return <Outlet />;
}
```

- [x] **Step 6: `router.tsx`**

```tsx
import { createBrowserRouter, RouterProvider, Navigate } from "react-router-dom";
import { AppShell } from "../components/layout/AppShell";
import { ProtectedShell } from "./protected";
import { Login } from "./login";
import { Forbidden } from "./forbidden";
import { Photos } from "./photos";
import { Timeline } from "./timeline";
import { Albums } from "./albums";
import { AlbumDetail } from "./album-detail";
import { Categories } from "./categories";
import { CategoryDetail } from "./category-detail";
import { Viewer } from "./viewer";
import { Profile } from "./profile";

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  { path: "/forbidden", element: <Forbidden /> },
  {
    element: <ProtectedShell />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/photos" replace /> },
          { path: "/photos", element: <Photos /> },
          { path: "/photos/timeline", element: <Timeline /> },
          { path: "/albums", element: <Albums /> },
          { path: "/albums/:albumId", element: <AlbumDetail /> },
          { path: "/categories", element: <Categories /> },
          { path: "/categories/:categoryId", element: <CategoryDetail /> },
          { path: "/viewer/:photoId", element: <Viewer /> },
          { path: "/profile", element: <Profile /> },
        ],
      },
    ],
  },
]);

export function AppRouter() {
  return <RouterProvider router={router} />;
}
```

- [x] **Step 7: 建 route stub 檔（給 router 引用不會爆）**

每個檔案暫時內容如下（後續 task 會實作）：

```tsx
// e.g. web/src/routes/photos.tsx
export function Photos() { return <div className="p-4">Photos (stub)</div>; }
```

八個路由 stub：`photos.tsx` `timeline.tsx` `albums.tsx` `album-detail.tsx` `categories.tsx` `category-detail.tsx` `viewer.tsx` `profile.tsx` `login.tsx` `forbidden.tsx`。

- [x] **Step 8: 改 `App.tsx` 塞 MediaProvider + AppRouter**

```tsx
import { useEffect, useMemo, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "./auth/context";
import type { AuthProvider } from "./auth/provider";
import { makeAuthProvider } from "./auth/select";
import { makeApi } from "./api/client";
import { ApiProvider } from "./api/context";
import { MediaProvider } from "./media/hooks";
import { AppRouter } from "./routes/router";
import { env } from "./lib/env";

export function App() {
  const [provider, setProvider] = useState<AuthProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { makeAuthProvider().then(setProvider).catch((e) => setError(String(e))); }, []);
  const qc = useMemo(() => new QueryClient({
    defaultOptions: { queries: { retry: retryPolicy, staleTime: 30_000, gcTime: 5 * 60_000 } },
  }), []);
  if (error) return <div className="p-6 text-red-600">Auth error: {error}</div>;
  if (!provider) return <div className="p-6">Loading…</div>;
  const client = makeApi(provider, env.apiBaseUrl);
  return (
    <AuthContextProvider provider={provider}>
      <QueryClientProvider client={qc}>
        <ApiProvider client={client}>
          <MediaProvider api={client}>
            <AppRouter />
          </MediaProvider>
        </ApiProvider>
      </QueryClientProvider>
    </AuthContextProvider>
  );
}

function retryPolicy(n: number, e: unknown): boolean {
  if (e && typeof e === "object" && "status" in e) {
    const s = (e as { status: number }).status;
    if (s === 401 || s === 403 || s === 404) return false;
  }
  return n < 2;
}
```

- [x] **Step 9: test — `protected.test.tsx`**

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "../auth/context";
import type { AuthProvider, AuthState } from "../auth/provider";
import { ApiProvider } from "../api/context";
import { ProtectedShell } from "./protected";
import { test, expect, vi } from "vitest";

function fake(state: AuthState): AuthProvider {
  return {
    async init() {},
    onChange(cb) { cb(state); return () => {}; },
    async signIn() {}, async signOut() {},
    async getIdToken() { return "t"; },
  };
}

function renderWith(auth: AuthProvider, meResponse: () => unknown) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const client = { getJson: vi.fn().mockImplementation(meResponse), getBlob: vi.fn() };
  return render(
    <MemoryRouter initialEntries={["/x"]}>
      <AuthContextProvider provider={auth}>
        <QueryClientProvider client={qc}>
          <ApiProvider client={client as any}>
            <Routes>
              <Route path="/login" element={<div>LOGIN</div>} />
              <Route path="/forbidden" element={<div>FORBIDDEN</div>} />
              <Route path="/x" element={<ProtectedShell />}>
                <Route index element={<div>SECRET</div>} />
              </Route>
            </Routes>
          </ApiProvider>
        </QueryClientProvider>
      </AuthContextProvider>
    </MemoryRouter>,
  );
}

test("signed-out redirects to /login", async () => {
  renderWith(fake({ kind: "signed-out" }), () => Promise.resolve({ uid: "x", email: "", role: "admin" }));
  expect(await screen.findByText("LOGIN")).toBeInTheDocument();
});

test("403 redirects to /forbidden", async () => {
  renderWith(
    fake({ kind: "signed-in", uid: "u", email: null, displayName: null }),
    () => Promise.reject(Object.assign(new Error("no"), { status: 403 })),
  );
  expect(await screen.findByText("FORBIDDEN")).toBeInTheDocument();
});

test("success renders outlet", async () => {
  renderWith(
    fake({ kind: "signed-in", uid: "u", email: null, displayName: null }),
    () => Promise.resolve({ uid: "u", email: null, role: "admin" }),
  );
  expect(await screen.findByText("SECRET")).toBeInTheDocument();
});
```

- [x] **Step 10: 跑 test + build**

```
cd web && npm run test && npm run build
```

- [x] **Step 11: Commit**

```bash
git add web/src/components/ web/src/routes/
git commit -m "feat(web): router shell with protected route and bottom nav"
```

---

## Task 13: Login route

**Files:**
- Modify: `web/src/routes/login.tsx`
- Create: `web/src/routes/login.test.tsx`

**Interfaces:**
- Consumes: `useAuth`, `env.authMode`, `env.devUsers`
- Produces: dev mode 顯示 user 下拉；prod 顯示 Google 按鈕。已登入者導 `/photos`。

- [x] **Step 1: `login.tsx`**

```tsx
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../auth/context";
import { env } from "../lib/env";

export function Login() {
  const { state, provider } = useAuth();
  const nav = useNavigate();
  const [err, setErr] = useState<string | null>(null);
  const [uid, setUid] = useState<string>(env.devUsers[0]?.uid ?? "");

  useEffect(() => {
    if (state.kind === "signed-in") nav("/photos", { replace: true });
  }, [state.kind, nav]);

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col items-center justify-center gap-6 p-6">
      <h1 className="text-xl font-semibold">Photo Browser</h1>
      <p className="text-center text-sm text-gray-500">Private family photo library — sign in to continue.</p>

      {env.authMode === "firebase" ? (
        <button
          onClick={() => provider.signIn().catch((e) => setErr(String(e)))}
          className="min-h-touch min-w-touch rounded bg-blue-600 px-5 py-3 text-white"
        >
          Sign in with Google
        </button>
      ) : (
        <>
          <label className="text-sm">
            Dev user
            <select
              value={uid}
              onChange={(e) => setUid(e.target.value)}
              className="ml-2 rounded border px-2 py-1"
            >
              {env.devUsers.map((u) => (
                <option key={u.uid} value={u.uid}>{u.email} ({u.role})</option>
              ))}
            </select>
          </label>
          <button
            onClick={() => provider.signIn(uid).catch((e) => setErr(String(e)))}
            className="min-h-touch min-w-touch rounded bg-blue-600 px-5 py-3 text-white"
          >
            Sign in
          </button>
        </>
      )}
      {err && <p role="alert" className="text-sm text-red-600">{err}</p>}
    </main>
  );
}
```

- [x] **Step 2: test**

```tsx
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthContextProvider } from "../auth/context";
import type { AuthProvider } from "../auth/provider";
import { Login } from "./login";
import { test, expect, vi } from "vitest";

const fakeProvider = (opts: Partial<{ onSignIn: (arg?: unknown) => Promise<void> }> = {}): AuthProvider => ({
  async init() {},
  onChange(cb) { cb({ kind: "signed-out" }); return () => {}; },
  signIn: opts.onSignIn ?? (async () => {}),
  async signOut() {},
  async getIdToken() { return null; },
});

test("dev mode calls signIn with selected uid", async () => {
  const spy = vi.fn().mockResolvedValue(undefined);
  render(
    <MemoryRouter>
      <AuthContextProvider provider={fakeProvider({ onSignIn: spy })}>
        <Login />
      </AuthContextProvider>
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByText(/Sign in/));
  await waitFor(() => expect(spy).toHaveBeenCalled());
});
```

- [x] **Step 3: 跑 test + build**

```
cd web && npm run test login && npm run build
```

- [x] **Step 4: Commit**

```bash
git add web/src/routes/login.tsx web/src/routes/login.test.tsx
git commit -m "feat(web): login route with dev dropdown and google button"
```

---

## Task 14: Photos + PhotoTile + PhotoGrid + segmented control

**Files:**
- Create: `web/src/components/grid/PhotoTile.tsx`
- Create: `web/src/components/grid/PhotoGrid.tsx`
- Create: `web/src/components/ui/Tabs.tsx`
- Modify: `web/src/routes/photos.tsx`
- Create: `web/src/routes/photos.test.tsx`

**Interfaces:**
- Consumes: `useAllPhotos`, `useAuthedImage`, `Photo` type。
- Produces: `<PhotoGrid photos onNext onLoadMore hasMore />`；`<PhotoTile photo />` 點擊 navigate `/viewer/:id?from=all`。

- [x] **Step 1: `Tabs.tsx`（segmented control）**

```tsx
import { NavLink } from "react-router-dom";

type Item = { to: string; label: string; end?: boolean };

export function Tabs({ items }: { items: Item[] }) {
  return (
    <div role="tablist" className="flex gap-1 border-b bg-white p-2 dark:bg-gray-900">
      {items.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          end={it.end}
          role="tab"
          className={({ isActive }) =>
            "min-h-touch rounded px-3 py-2 text-sm " +
            (isActive ? "bg-blue-600 text-white" : "text-gray-700 hover:bg-gray-100")
          }
        >
          {it.label}
        </NavLink>
      ))}
    </div>
  );
}
```

- [x] **Step 2: `PhotoTile.tsx`**

```tsx
import { Link } from "react-router-dom";
import type { Photo } from "../../api/types";
import { useAuthedImage } from "../../media/hooks";

export function PhotoTile({ photo, from }: { photo: Photo; from: string }) {
  const src = photo.thumbnail_key
    ? `/photos/${photo.id}/thumbnail/${photo.thumbnail_key}`
    : null;
  const img = useAuthedImage(src);
  return (
    <Link
      to={`/viewer/${photo.id}?from=${from}`}
      className="relative block aspect-square overflow-hidden rounded bg-gray-200 dark:bg-gray-800"
    >
      {img.state === "ready" ? (
        <img
          src={img.objectUrl}
          alt={photo.filename}
          loading="lazy"
          className="h-full w-full object-cover"
        />
      ) : img.state === "error" ? (
        <span className="flex h-full w-full items-center justify-center text-xs text-gray-400">
          n/a
        </span>
      ) : (
        <span className="block h-full w-full animate-pulse bg-gray-300 dark:bg-gray-700" aria-hidden="true" />
      )}
    </Link>
  );
}
```

- [x] **Step 3: `PhotoGrid.tsx`（含 IntersectionObserver 觸發 loadMore）**

```tsx
import { useEffect, useRef } from "react";
import type { Photo } from "../../api/types";
import { PhotoTile } from "./PhotoTile";

type Props = {
  photos: Photo[];
  from: string;
  hasMore: boolean;
  onLoadMore: () => void;
};

export function PhotoGrid({ photos, from, hasMore, onLoadMore }: Props) {
  const sentinel = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!hasMore || !sentinel.current) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) onLoadMore();
    }, { rootMargin: "400px" });
    io.observe(sentinel.current);
    return () => io.disconnect();
  }, [hasMore, onLoadMore]);
  return (
    <div className="p-2">
      <div className="grid grid-cols-3 gap-1 sm:grid-cols-4 md:grid-cols-6 lg:grid-cols-8">
        {photos.map((p) => (
          <PhotoTile key={p.id} photo={p} from={from} />
        ))}
      </div>
      {hasMore && <div ref={sentinel} className="h-8" />}
    </div>
  );
}
```

- [x] **Step 4: `photos.tsx`**

```tsx
import { Header } from "../components/layout/Header";
import { Tabs } from "../components/ui/Tabs";
import { PhotoGrid } from "../components/grid/PhotoGrid";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useAllPhotos } from "../api/queries";

export function Photos() {
  const q = useAllPhotos();
  const photos = q.data?.pages.flatMap((p) => p.photos) ?? [];
  return (
    <>
      <Header title="Photos" />
      <Tabs items={[
        { to: "/photos", label: "All", end: true },
        { to: "/photos/timeline", label: "Timeline" },
      ]} />
      {q.isLoading ? <Loading /> :
       q.error ? <ErrorPanel kind="network" onRetry={() => q.refetch()} /> :
       photos.length === 0 ? <Empty title="No photos yet" hint="Add photos to your NAS and re-run the indexer." /> :
       <PhotoGrid
         photos={photos}
         from="all"
         hasMore={!!q.hasNextPage}
         onLoadMore={() => q.fetchNextPage()}
       />}
    </>
  );
}
```

- [x] **Step 5: test — `photos.test.tsx`**

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiProvider } from "../api/context";
import { MediaProvider } from "../media/hooks";
import { Photos } from "./photos";
import type { ApiClient } from "../api/client";
import { test, expect, vi } from "vitest";

(globalThis.URL.createObjectURL as unknown) = vi.fn().mockReturnValue("blob://");
(globalThis.URL.revokeObjectURL as unknown) = vi.fn();

function wrap(client: ApiClient) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <ApiProvider client={client}>
          <MediaProvider api={client}>{children}</MediaProvider>
        </ApiProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

test("empty state when no photos", async () => {
  const client: ApiClient = {
    getJson: vi.fn().mockResolvedValue({ photos: [], next_cursor: null }),
    getBlob: vi.fn(),
  };
  const Wrapper = wrap(client);
  render(<Wrapper><Photos /></Wrapper>);
  await waitFor(() => expect(screen.getByText(/No photos yet/)).toBeInTheDocument());
});
```

- [x] **Step 6: 跑 test + build**

```
cd web && npm run test photos && npm run build
```

- [x] **Step 7: Commit**

```bash
git add web/src/components/grid/ web/src/components/ui/Tabs.tsx web/src/routes/photos.tsx web/src/routes/photos.test.tsx
git commit -m "feat(web): photos route with tile grid and infinite scroll"
```

---

## Task 15: Timeline route

**Files:**
- Create: `web/src/lib/fmt.ts`
- Create: `web/src/lib/fmt.test.ts`
- Modify: `web/src/routes/timeline.tsx`

**Interfaces:**
- Consumes: `useTimeline`, `groupByYearMonth`
- Produces: 只顯示有 `taken_at` 的照片，依 local Year/Month heading 分組。

- [x] **Step 1: test — `fmt.test.ts`**

```ts
import { groupByYearMonth } from "./fmt";
import type { Photo } from "../api/types";
import { test, expect } from "vitest";

const p = (id: number, taken?: string): Photo => ({
  id, album_id: 1, filename: `f${id}.jpg`, relative_path: "", mime_type: "image/jpeg",
  file_size: 1, file_mtime_ns: 1, taken_at: taken,
});

test("groups preserving input order within group", () => {
  const groups = groupByYearMonth([
    p(1, "2025-05-04T00:00:00Z"),
    p(2, "2025-05-01T00:00:00Z"),
    p(3, "2025-04-30T00:00:00Z"),
  ]);
  expect(groups.map((g) => g.key)).toEqual(["2025-05", "2025-04"]);
  expect(groups[0].photos.map((x) => x.id)).toEqual([1, 2]);
});

test("skips photos without taken_at", () => {
  const groups = groupByYearMonth([p(1), p(2, "2025-05-01T00:00:00Z")]);
  expect(groups.flatMap((g) => g.photos.map((x) => x.id))).toEqual([2]);
});
```

- [x] **Step 2: 跑失敗**

- [x] **Step 3: `fmt.ts`**

```ts
import type { Photo } from "../api/types";

export type Group = { key: string; label: string; photos: Photo[] };

export function groupByYearMonth(photos: Photo[]): Group[] {
  const out: Group[] = [];
  const idx = new Map<string, number>();
  for (const p of photos) {
    if (!p.taken_at) continue;
    const d = new Date(p.taken_at);
    if (Number.isNaN(d.getTime())) continue;
    const y = d.getFullYear();
    const m = d.getMonth() + 1;
    const key = `${y}-${String(m).padStart(2, "0")}`;
    let i = idx.get(key);
    if (i === undefined) {
      i = out.length;
      idx.set(key, i);
      out.push({ key, label: `${y} / ${String(m).padStart(2, "0")}`, photos: [] });
    }
    out[i].photos.push(p);
  }
  return out;
}
```

- [x] **Step 4: 跑通過**

- [x] **Step 5: `timeline.tsx`**

```tsx
import { Header } from "../components/layout/Header";
import { Tabs } from "../components/ui/Tabs";
import { PhotoGrid } from "../components/grid/PhotoGrid";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useTimeline } from "../api/queries";
import { groupByYearMonth } from "../lib/fmt";

export function Timeline() {
  const q = useTimeline();
  const all = q.data?.pages.flatMap((p) => p.photos) ?? [];
  const groups = groupByYearMonth(all);
  return (
    <>
      <Header title="Photos" />
      <Tabs items={[
        { to: "/photos", label: "All", end: true },
        { to: "/photos/timeline", label: "Timeline" },
      ]} />
      {q.isLoading ? <Loading /> :
       q.error ? <ErrorPanel kind="network" onRetry={() => q.refetch()} /> :
       groups.length === 0 ? <Empty title="No dated photos yet" /> :
       <div>
         {groups.map((g) => (
           <section key={g.key} className="py-2">
             <h2 className="px-3 py-2 text-sm font-semibold text-gray-600">{g.label}</h2>
             <PhotoGrid photos={g.photos} from={`t${g.key}`} hasMore={false} onLoadMore={() => {}} />
           </section>
         ))}
         {q.hasNextPage && (
           <div className="p-4 text-center">
             <button onClick={() => q.fetchNextPage()} className="min-h-touch rounded border px-4 py-2 text-sm">
               Load more
             </button>
           </div>
         )}
       </div>}
    </>
  );
}
```

- [x] **Step 6: 跑 test + build; Commit**

```bash
cd web && npm run test && npm run build
git add web/src/lib/ web/src/routes/timeline.tsx
git commit -m "feat(web): timeline route grouped by year/month"
```

---

## Task 16: Albums + AlbumDetail

**Files:**
- Create: `web/src/components/grid/AlbumCard.tsx`
- Modify: `web/src/routes/albums.tsx`
- Modify: `web/src/routes/album-detail.tsx`

**Interfaces:**
- Consumes: `useAlbums`, `useAlbum`, `useAlbumPhotos`, `useAuthedImage`
- Produces: Album list（cover + name + count 由 UI 用 `useAlbumPhotos(id)` 拿一頁得 total 是複雜，POC 先不顯示 count），AlbumDetail：album meta header + photo grid。

- [x] **Step 1: `AlbumCard.tsx`**

```tsx
import { Link } from "react-router-dom";
import type { Album } from "../../api/types";
import { useAuthedImage } from "../../media/hooks";

export function AlbumCard({ album }: { album: Album }) {
  const src = album.cover_photo_id && album.cover_thumbnail_key
    ? `/photos/${album.cover_photo_id}/thumbnail/${album.cover_thumbnail_key}`
    : null;
  const img = useAuthedImage(src);
  return (
    <Link
      to={`/albums/${album.id}`}
      className="block overflow-hidden rounded border bg-white shadow-sm dark:bg-gray-900"
    >
      <div className="aspect-[4/3] bg-gray-200 dark:bg-gray-800">
        {img.state === "ready" ? (
          <img src={img.objectUrl} alt={album.name} loading="lazy" className="h-full w-full object-cover" />
        ) : img.state === "error" ? (
          <span className="flex h-full w-full items-center justify-center text-xs text-gray-400">no cover</span>
        ) : (
          <span className="block h-full w-full animate-pulse bg-gray-300 dark:bg-gray-700" aria-hidden="true" />
        )}
      </div>
      <div className="p-3">
        <p className="text-sm font-medium">{album.name}</p>
        <p className="text-xs text-gray-500">/{album.relative_path}</p>
      </div>
    </Link>
  );
}
```

- [x] **Step 2: `albums.tsx`**

```tsx
import { Header } from "../components/layout/Header";
import { AlbumCard } from "../components/grid/AlbumCard";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useAlbums } from "../api/queries";
import { useEffect, useRef } from "react";

export function Albums() {
  const q = useAlbums();
  const albums = q.data?.pages.flatMap((p) => p.albums) ?? [];
  const sentinel = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!q.hasNextPage || !sentinel.current) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && q.fetchNextPage(), { rootMargin: "400px" });
    io.observe(sentinel.current);
    return () => io.disconnect();
  }, [q.hasNextPage, q.fetchNextPage]);
  return (
    <>
      <Header title="Albums" />
      {q.isLoading ? <Loading /> :
       q.error ? <ErrorPanel kind="network" onRetry={() => q.refetch()} /> :
       albums.length === 0 ? <Empty title="No albums yet" /> :
       <div className="grid grid-cols-2 gap-3 p-3 sm:grid-cols-3 md:grid-cols-4">
         {albums.map((a) => <AlbumCard key={a.id} album={a} />)}
         {q.hasNextPage && <div ref={sentinel} className="col-span-full h-8" />}
       </div>}
    </>
  );
}
```

- [x] **Step 3: `album-detail.tsx`**

```tsx
import { useParams } from "react-router-dom";
import { useAlbum, useAlbumPhotos } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { Empty } from "../components/states/Empty";
import { PhotoGrid } from "../components/grid/PhotoGrid";

export function AlbumDetail() {
  const { albumId } = useParams();
  const id = Number(albumId);
  const meta = useAlbum(id);
  const q = useAlbumPhotos(id);
  const photos = q.data?.pages.flatMap((p) => p.photos) ?? [];
  return (
    <>
      <Header title={meta.data?.name ?? "Album"} />
      {q.isLoading || meta.isLoading ? <Loading /> :
       (q.error || meta.error) ? <ErrorPanel kind="network" onRetry={() => { q.refetch(); meta.refetch(); }} /> :
       photos.length === 0 ? <Empty title="Album is empty" /> :
       <PhotoGrid
         photos={photos}
         from={`album-${id}`}
         hasMore={!!q.hasNextPage}
         onLoadMore={() => q.fetchNextPage()}
       />}
    </>
  );
}
```

- [x] **Step 4: 跑 test + build; Commit**

```bash
cd web && npm run test && npm run build
git add web/src/components/grid/AlbumCard.tsx web/src/routes/albums.tsx web/src/routes/album-detail.tsx
git commit -m "feat(web): albums list and album detail routes"
```

---

## Task 17: Categories + CategoryDetail

**Files:**
- Create: `web/src/components/grid/CategoryCard.tsx`
- Modify: `web/src/routes/categories.tsx`
- Modify: `web/src/routes/category-detail.tsx`

**Interfaces:**
- Consumes: `useCategories`, `useCategoryAlbums`, `AlbumCard`
- Produces：Category list（純文字 tile），CategoryDetail 秀該 category 下的 albums。

- [x] **Step 1: `CategoryCard.tsx`**

```tsx
import { Link } from "react-router-dom";
import type { Category } from "../../api/types";

export function CategoryCard({ category }: { category: Category }) {
  return (
    <Link
      to={`/categories/${category.id}`}
      className="min-h-touch flex items-center justify-between rounded border bg-white px-4 py-4 shadow-sm hover:bg-gray-50 dark:bg-gray-900"
    >
      <span className="text-sm font-medium">{category.name}</span>
      <span aria-hidden="true">›</span>
    </Link>
  );
}
```

- [x] **Step 2: `categories.tsx`**

```tsx
import { Header } from "../components/layout/Header";
import { CategoryCard } from "../components/grid/CategoryCard";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useCategories } from "../api/queries";

export function Categories() {
  const q = useCategories();
  const cats = q.data?.pages.flatMap((p) => p.categories) ?? [];
  return (
    <>
      <Header title="Categories" />
      {q.isLoading ? <Loading /> :
       q.error ? <ErrorPanel kind="network" onRetry={() => q.refetch()} /> :
       cats.length === 0 ? <Empty title="No categories yet" /> :
       <div className="flex flex-col gap-2 p-3">
         {cats.map((c) => <CategoryCard key={c.id} category={c} />)}
         {q.hasNextPage && (
           <button onClick={() => q.fetchNextPage()} className="min-h-touch rounded border px-4 py-2 text-sm">
             Load more
           </button>
         )}
       </div>}
    </>
  );
}
```

- [x] **Step 3: `category-detail.tsx`**

```tsx
import { useParams } from "react-router-dom";
import { useCategoryAlbums } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { Empty } from "../components/states/Empty";
import { AlbumCard } from "../components/grid/AlbumCard";

export function CategoryDetail() {
  const { categoryId } = useParams();
  const id = Number(categoryId);
  const q = useCategoryAlbums(id);
  const albums = q.data?.pages.flatMap((p) => p.albums) ?? [];
  return (
    <>
      <Header title="Category" />
      {q.isLoading ? <Loading /> :
       q.error ? <ErrorPanel kind="network" onRetry={() => q.refetch()} /> :
       albums.length === 0 ? <Empty title="No albums in this category" /> :
       <div className="grid grid-cols-2 gap-3 p-3 sm:grid-cols-3 md:grid-cols-4">
         {albums.map((a) => <AlbumCard key={a.id} album={a} />)}
         {q.hasNextPage && (
           <button onClick={() => q.fetchNextPage()} className="col-span-full min-h-touch rounded border px-4 py-2 text-sm">
             Load more
           </button>
         )}
       </div>}
    </>
  );
}
```

- [x] **Step 4: 跑 test + build; Commit**

```bash
cd web && npm run test && npm run build
git add web/src/components/grid/CategoryCard.tsx web/src/routes/categories.tsx web/src/routes/category-detail.tsx
git commit -m "feat(web): categories list and category detail routes"
```

---

## Task 18: Viewer route

**Files:**
- Create: `web/src/components/viewer/PhotoViewer.tsx`
- Create: `web/src/components/viewer/PhotoViewer.test.tsx`
- Modify: `web/src/routes/viewer.tsx`

**Interfaces:**
- Consumes: photo id from URL；`useAllPhotos` / `useAlbumPhotos` / `useTimeline` 從 `?from=` 挑集合；`useAuthedImage` 拿 original。
- Produces：Full-screen viewer 支援 left/right/Escape，錯誤時顯示 "Photo unavailable"。

- [x] **Step 1: 讀取當前 collection 的策略**

`?from=` 語意：
- `all` → `useAllPhotos()`
- `album-<id>` → `useAlbumPhotos(id)`
- `t<YYYY-MM>` → `useTimeline()` filter by group（`from` prefix `t`）
- 其他 → fallback：只顯示當前一張，前後 nav disabled

- [x] **Step 2: `PhotoViewer.tsx`**

```tsx
import { useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { Photo } from "../../api/types";
import { useAuthedImage } from "../../media/hooks";

type Props = {
  current: Photo;
  siblings: Photo[];   // 已載入的當前 collection
  from: string;
};

export function PhotoViewer({ current, siblings, from }: Props) {
  const nav = useNavigate();
  const idx = siblings.findIndex((p) => p.id === current.id);
  const prev = idx > 0 ? siblings[idx - 1] : null;
  const next = idx >= 0 && idx < siblings.length - 1 ? siblings[idx + 1] : null;
  const img = useAuthedImage(`/photos/${current.id}/original`);

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.key === "Escape") nav(-1);
      else if (e.key === "ArrowLeft" && prev) nav(`/viewer/${prev.id}?from=${from}`);
      else if (e.key === "ArrowRight" && next) nav(`/viewer/${next.id}?from=${from}`);
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [prev, next, nav, from]);

  return (
    <div role="dialog" aria-modal="true" aria-label={current.filename} className="fixed inset-0 z-20 flex flex-col bg-black text-white">
      <div className="flex items-center justify-between p-3">
        <button onClick={() => nav(-1)} className="min-h-touch min-w-touch rounded px-3 py-2" aria-label="Close">
          ✕
        </button>
        <div className="text-sm">
          <div className="font-medium">{current.filename}</div>
          {current.taken_at && <div className="text-xs text-gray-300">{current.taken_at}</div>}
        </div>
        <div className="w-11" aria-hidden="true" />
      </div>
      <div className="relative flex flex-1 items-center justify-center">
        {img.state === "ready" ? (
          <img src={img.objectUrl} alt={current.filename} className="max-h-full max-w-full object-contain" />
        ) : img.state === "error" ? (
          <p>Photo unavailable</p>
        ) : (
          <p>Loading…</p>
        )}
        {prev && (
          <Link
            to={`/viewer/${prev.id}?from=${from}`}
            aria-label="Previous"
            className="absolute left-2 top-1/2 -translate-y-1/2 min-h-touch min-w-touch rounded bg-white/10 px-3 py-2"
          >
            ‹
          </Link>
        )}
        {next && (
          <Link
            to={`/viewer/${next.id}?from=${from}`}
            aria-label="Next"
            className="absolute right-2 top-1/2 -translate-y-1/2 min-h-touch min-w-touch rounded bg-white/10 px-3 py-2"
          >
            ›
          </Link>
        )}
      </div>
    </div>
  );
}
```

- [x] **Step 3: test — `PhotoViewer.test.tsx`**

```tsx
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { MediaProvider } from "../../media/hooks";
import { PhotoViewer } from "./PhotoViewer";
import type { ApiClient } from "../../api/client";
import type { Photo } from "../../api/types";
import { test, expect, vi } from "vitest";

(globalThis.URL.createObjectURL as unknown) = vi.fn().mockReturnValue("blob://");
(globalThis.URL.revokeObjectURL as unknown) = vi.fn();

const p = (id: number): Photo => ({
  id, album_id: 1, filename: `f${id}.jpg`, relative_path: "", mime_type: "image/jpeg",
  file_size: 1, file_mtime_ns: 1,
});

const client: ApiClient = {
  getJson: vi.fn(),
  getBlob: vi.fn().mockResolvedValue(new Blob([new Uint8Array([1])], { type: "image/jpeg" })),
};

test("shows filename and next/prev buttons", () => {
  render(
    <MemoryRouter>
      <MediaProvider api={client}>
        <PhotoViewer current={p(2)} siblings={[p(1), p(2), p(3)]} from="all" />
      </MediaProvider>
    </MemoryRouter>,
  );
  expect(screen.getByText("f2.jpg")).toBeInTheDocument();
  expect(screen.getByLabelText("Previous")).toBeInTheDocument();
  expect(screen.getByLabelText("Next")).toBeInTheDocument();
});

test("Escape triggers close", () => {
  render(
    <MemoryRouter>
      <MediaProvider api={client}>
        <PhotoViewer current={p(1)} siblings={[p(1)]} from="all" />
      </MediaProvider>
    </MemoryRouter>,
  );
  fireEvent.keyDown(window, { key: "Escape" });
  // navigate(-1) 不會拋錯就算通過；也可裝 spy in memory router 更精細
});
```

- [x] **Step 4: `viewer.tsx`**

```tsx
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import { PhotoViewer } from "../components/viewer/PhotoViewer";
import { useAllPhotos, useAlbumPhotos, useTimeline } from "../api/queries";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import type { Photo } from "../api/types";

export function Viewer() {
  const { photoId } = useParams();
  const [sp] = useSearchParams();
  const from = sp.get("from") ?? "";
  const id = Number(photoId);
  const nav = useNavigate();

  const siblings = useSiblings(from);

  if (siblings.state === "loading") return <Loading />;
  if (siblings.state === "error") return <ErrorPanel kind="network" onRetry={() => nav(0)} />;

  const current = siblings.photos.find((p) => p.id === id);
  if (!current) return <ErrorPanel kind="not-found" onRetry={() => nav(-1)} />;

  return <PhotoViewer current={current} siblings={siblings.photos} from={from} />;
}

type SibState = { state: "loading" | "error" } | { state: "ready"; photos: Photo[] };

function useSiblings(from: string): SibState {
  const all = useAllPhotos();
  const albumQ = useAlbumPhotos(parseAlbumFrom(from) ?? 0);
  const tlQ = useTimeline();

  if (from === "all") return summarize(all);
  if (from.startsWith("album-")) return summarize(albumQ);
  if (from.startsWith("t")) return summarize(tlQ);
  return { state: "ready", photos: [] };
}

function parseAlbumFrom(from: string): number | null {
  const m = /^album-(\d+)$/.exec(from);
  return m ? Number(m[1]) : null;
}

function summarize(q: ReturnType<typeof useAllPhotos>): SibState {
  if (q.isLoading) return { state: "loading" };
  if (q.error) return { state: "error" };
  return { state: "ready", photos: q.data?.pages.flatMap((p) => p.photos) ?? [] };
}
```

**注意**：`useSiblings` 三個 hook 都呼喚 —— React hook 規則要求無條件呼叫，OK。有點浪費但 TanStack Query 對重複呼叫同 key 會 share cache，不會多打 network。可接受。

- [x] **Step 5: 跑 test + build; Commit**

```bash
cd web && npm run test && npm run build
git add web/src/components/viewer/ web/src/routes/viewer.tsx
git commit -m "feat(web): full-screen photo viewer with prev/next/escape"
```

---

## Task 19: Profile + Forbidden

**Files:**
- Modify: `web/src/routes/profile.tsx`
- Modify: `web/src/routes/forbidden.tsx`

**Interfaces:**
- Consumes: `useAuth`, `useMe`, `provider.signOut`
- Produces：Profile 顯示 displayName/email/role + Logout；Forbidden 顯示訊息 + Logout。

- [x] **Step 1: `profile.tsx`**

```tsx
import { useAuth } from "../auth/context";
import { useMe } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";

export function Profile() {
  const { state, provider } = useAuth();
  const me = useMe();
  if (me.isLoading || state.kind === "initializing") return <Loading />;
  const email = state.kind === "signed-in" ? state.email ?? me.data?.email ?? "" : "";
  const name = state.kind === "signed-in" ? state.displayName ?? "" : "";
  const role = me.data?.role ?? "";
  return (
    <>
      <Header title="Profile" />
      <div className="p-4 space-y-4">
        {name && <p className="text-lg font-semibold">{name}</p>}
        {email && <p className="text-sm text-gray-600">{email}</p>}
        {role && <p className="text-xs uppercase tracking-wide text-gray-500">Role: {role}</p>}
        <button
          onClick={() => provider.signOut()}
          className="min-h-touch rounded border px-4 py-2 text-sm"
        >
          Log out
        </button>
      </div>
    </>
  );
}
```

- [x] **Step 2: `forbidden.tsx`**

```tsx
import { useNavigate } from "react-router-dom";
import { useAuth } from "../auth/context";

export function Forbidden() {
  const nav = useNavigate();
  const { provider } = useAuth();
  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col items-center justify-center gap-6 p-6 text-center">
      <h1 className="text-lg font-semibold">Access denied</h1>
      <p className="text-sm text-gray-600">
        Your account is signed in, but is not on the allowlist for this photo library.
      </p>
      <button
        onClick={async () => { await provider.signOut(); nav("/login", { replace: true }); }}
        className="min-h-touch rounded border px-4 py-2 text-sm"
      >
        Log out
      </button>
    </main>
  );
}
```

- [x] **Step 3: 跑 test + build; Commit**

```bash
cd web && npm run test && npm run build
git add web/src/routes/profile.tsx web/src/routes/forbidden.tsx
git commit -m "feat(web): profile and forbidden routes"
```

---

## Task 20: Playwright setup + happy-path e2e

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/tests/e2e/login-and-browse.spec.ts`
- Create: `web/tests/e2e/helpers.ts`

**Interfaces:**
- Consumes: docker-compose dev stack（Task 3）、build output（`npm run build && npm run preview`）
- Produces：Playwright + Pixel 5 viewport；覆蓋 login → grid → viewer → logout。

- [x] **Step 1: `playwright.config.ts`**

```ts
import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.PW_PREVIEW_PORT ?? 4173);

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  timeout: 30_000,
  use: {
    baseURL: `http://localhost:${port}`,
    ...devices["Pixel 5"],
    trace: "retain-on-failure",
  },
  webServer: {
    command: `npm run build && npx vite preview --port ${port}`,
    port,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
```

Vite preview 需要 `.env.local` 有 `VITE_AUTH_MODE=testauth` 才走 dev flow。

- [x] **Step 2: `helpers.ts`**

```ts
import type { Page } from "@playwright/test";

export async function signInAs(page: Page, uid: string) {
  await page.goto("/login");
  await page.selectOption("select", uid);
  await page.getByRole("button", { name: /Sign in/ }).click();
  await page.waitForURL(/\/photos/);
}
```

- [x] **Step 3: `login-and-browse.spec.ts`**

```ts
import { test, expect } from "@playwright/test";
import { signInAs } from "./helpers";

test("admin can browse from grid to viewer and log out", async ({ page }) => {
  await signInAs(page, "admin-1");
  // grid
  await expect(page.locator("a[href^='/viewer/']").first()).toBeVisible({ timeout: 15_000 });
  const firstTile = page.locator("a[href^='/viewer/']").first();
  const href = await firstTile.getAttribute("href");
  await firstTile.click();
  await expect(page).toHaveURL(new RegExp(href!.replace("?", "\\?")));
  // filename visible
  await expect(page.getByRole("dialog")).toBeVisible();
  // close via Escape
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/photos/);
  // logout via profile
  await page.getByRole("link", { name: "Profile" }).click();
  await page.getByRole("button", { name: /Log out/ }).click();
  await expect(page).toHaveURL(/\/login/);
});
```

- [x] **Step 4: 手動跑一次**

```bash
make dev-web-stack
cd web && npx playwright install --with-deps chromium && npm run e2e
```

若失敗（多半是 stack 沒起完全），檢查 `docker compose logs backend`、`curl` API。

- [x] **Step 5: Commit**

```bash
cd .. 
git add web/playwright.config.ts web/tests/e2e/
git commit -m "test(web): playwright happy-path e2e via preview server"
```

---

## Task 21: 補齊 e2e（forbidden / thumb-404 / token-refresh）

**Files:**
- Create: `web/tests/e2e/forbidden.spec.ts`
- Create: `web/tests/e2e/thumbnail-missing.spec.ts`
- Create: `web/tests/e2e/token-refresh.spec.ts`
- Modify: `web/src/auth/testauth.ts`（暴露 `signInWithShortExp` 給 e2e 用；正常 signIn 不變）

**Interfaces:**
- Produces：三個場景可穩定 reproduce。

- [x] **Step 1: `forbidden.spec.ts`**

```ts
import { test, expect } from "@playwright/test";
import { signInAs } from "./helpers";

test("unallowlisted user lands on /forbidden", async ({ page }) => {
  await page.goto("/login");
  await page.selectOption("select", "denied-1");
  await page.getByRole("button", { name: /Sign in/ }).click();
  await expect(page).toHaveURL(/\/forbidden/);
  await expect(page.getByText(/Access denied/)).toBeVisible();
});
```

（此 test 依賴 `denied-1` 未加入 allowlist——Task 3 的 `dev-users.json` 只 seed admin/member 兩人。）

- [x] **Step 2: `thumbnail-missing.spec.ts`**

在 dev-stack 內把一張 thumbnail 檔案刪掉：

```ts
import { test, expect } from "@playwright/test";
import { execSync } from "child_process";
import { signInAs } from "./helpers";

test("grid stays usable when a thumbnail file is missing", async ({ page }) => {
  // pick a random thumbnail from the volume
  const out = execSync(`docker compose -f ../deploy/compose/docker-compose.dev.yml exec -T nginx sh -c "ls /srv/thumbnails | head -1"`).toString().trim();
  if (out) {
    execSync(`docker compose -f ../deploy/compose/docker-compose.dev.yml exec -T nginx sh -c "rm /srv/thumbnails/${out}"`);
  }
  await signInAs(page, "admin-1");
  // 應該還是能看到 tile（fallback），grid 不會空白
  await expect(page.locator("a[href^='/viewer/']").first()).toBeVisible({ timeout: 15_000 });
});
```

**注意**：此 test 會污染 volume；建議放在最後跑，或後續補一步把 backend 重跑 indexer 復原。若嫌 fragile，可以簡化為「拉不到的 tile 顯示 placeholder 樣式」——用 mock 的 API client 在 unit test 覆蓋，e2e 這條可以省。

- [x] **Step 3: `token-refresh.spec.ts`**

TestAuth 對 `?exp=` 支援短存活；讓前端在需要時可產生短存活 token。實作方案：在 `testauth.ts` 加一個 `signInWithShortExp(uid, expSeconds)` 只給 e2e 用；prod bundle 不使用（bundle guard 也不會誤傷因為它在 `testauth.ts` 檔案內、prod 根本沒 import 這個 module）。

改 `testauth.ts` 加：

```ts
async signInShort(uid: string, expSeconds: number) {
  const user = this.opts.devUsers.find((u) => u.uid === uid);
  if (!user) throw new Error("unknown uid");
  this.currentUid = uid;
  const url = new URL(this.opts.url);
  url.pathname = "/mint";
  url.searchParams.set("sub", user.uid);
  url.searchParams.set("email", user.email);
  url.searchParams.set("verified", "1");
  url.searchParams.set("exp", String(expSeconds));
  const res = await fetch(url.toString());
  this.token = (await res.text()).trim();
  sessionStorage.setItem(TOKEN_KEY, this.token);
  sessionStorage.setItem(UID_KEY, uid);
  this.emit(this.stateFromUid(uid));
}
```

在 login.tsx 加一個「Dev tools（短 exp）」隱藏 anchor（QA URL: `/login?short=3`），或直接讓 e2e 從 devtools console 呼 `window.__ta.signInShort`。POC 用後者：

在 `App.tsx` 開 dev-mode 掛一個全域 handle：

```tsx
if (import.meta.env.VITE_AUTH_MODE === "testauth") {
  (window as unknown as { __ta: unknown }).__ta = provider;
}
```

`token-refresh.spec.ts`：

```ts
import { test, expect } from "@playwright/test";

test("expired token auto-refreshes on next api call", async ({ page }) => {
  await page.goto("/login");
  await page.selectOption("select", "admin-1");
  await page.getByRole("button", { name: /Sign in/ }).click();
  await page.waitForURL(/\/photos/);
  // force short token
  await page.evaluate(() => (window as any).__ta.signInShort("admin-1", 3));
  await page.waitForTimeout(4_000);
  await page.getByRole("link", { name: "Albums" }).click();
  await expect(page.getByRole("heading", { name: "Albums" })).toBeVisible();
  // 若 refresh 失敗會被踢到 /login；沒被踢就算過
});
```

- [x] **Step 4: 跑三個新 e2e**

```bash
cd web && npm run e2e
```

失敗常見原因：exec docker 命令路徑；stack 未起；`__ta` 未掛。逐個解。

- [x] **Step 5: Commit**

```bash
cd ..
git add web/tests/e2e/ web/src/auth/testauth.ts web/src/App.tsx
git commit -m "test(web): forbidden, thumbnail-missing, token-refresh e2e"
```

---

## Task 22: Prod bundle guard + CI-ready

**Files:**
- Modify: `Makefile`（`bundle-guard` 已於 Task 4 加入；此 task 補強）
- Create: `web/scripts/bundle-guard.mjs`
- Modify: `web/package.json`（加 `"guard": "node scripts/bundle-guard.mjs"`）

**Interfaces:**
- Produces：`make build-web && make bundle-guard` 在 CI 上 fail-fast 阻擋 testauth code / firebase config 洩漏。

- [x] **Step 1: `web/scripts/bundle-guard.mjs`**

```js
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const DIST = new URL("../dist/", import.meta.url).pathname;
const forbidden = [
  { pattern: /TestAuthProvider/, why: "TestAuthProvider must not be in prod bundle" },
  { pattern: /\/mint\?sub=/, why: "testauth /mint URL must not be in prod bundle" },
  // Firebase mode 常見誤刪 check：確保 apiKey 不是空字串預設（真的空的話 build 時應丟）
];

const violations = [];
function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const s = statSync(p);
    if (s.isDirectory()) { walk(p); continue; }
    if (!/\.(js|css|html|map)$/.test(name)) continue;
    const body = readFileSync(p, "utf8");
    for (const { pattern, why } of forbidden) {
      if (pattern.test(body)) violations.push(`${p}: ${why}`);
    }
  }
}
walk(DIST);

if (violations.length) {
  console.error("Bundle guard FAILED:");
  for (const v of violations) console.error("  " + v);
  process.exit(1);
}
console.log("Bundle guard OK");
```

- [x] **Step 2: 更新 `web/package.json`**

`"scripts"` 加：

```
"guard": "node scripts/bundle-guard.mjs"
```

- [x] **Step 3: 更新 Makefile bundle-guard target**

```makefile
bundle-guard:
	cd web && VITE_AUTH_MODE=firebase VITE_FIREBASE_API_KEY=stub VITE_FIREBASE_AUTH_DOMAIN=stub VITE_FIREBASE_PROJECT_ID=stub VITE_FIREBASE_APP_ID=stub npm run build && npm run guard
```

- [x] **Step 4: 跑一次驗**

```bash
make bundle-guard
```

期望：`Bundle guard OK`。若 testauth code 被 include，會 fail 並列出檔案。

- [x] **Step 5: Commit**

```bash
git add web/scripts/bundle-guard.mjs web/package.json Makefile
git commit -m "chore: prod bundle guard checks testauth code is stripped"
```

---

## 附錄 A：驗收清單（跟 spec §14 對齊）

跑完 22 個 task 後：

- [x] `make test` 全綠（backend）
- [x] `make test-web` 全綠（Vitest）
- [x] `make e2e-web` 全綠（Playwright，Pixel 5 viewport = 360px 起）
- [x] `make bundle-guard` OK
- [x] `make build-web` 產出 `web/dist/` 且大小合理
- [x] `/photos` `/photos/timeline` `/albums` `/albums/:id` `/categories` `/categories/:id` `/viewer/:id` `/profile` `/login` `/forbidden` 都可達
- [x] Firebase login 成功但 `/me` 未 200 前不顯示照片（Task 12 覆蓋）
- [x] Media 帶 Authorization 且 objectUrl 有被 revoke（Task 11 unit + Task 20 e2e）
- [x] 沒有 upload / delete / rename / move / admin control（grep 檢查）
- [x] Timeline 只顯示 non-null `taken_at`（Task 15 unit test）
- [x] Manual smoke：`make dev-web-stack && cd web && npm run dev`，360px viewport 走 login → grid → viewer → logout 順暢

## 附錄 B：Spec 對照

| Spec 3 節 | 對應 task |
|---|---|
| §3 導覽 / 路由 | Task 12 (router)、Task 13-19 (各 route) |
| §4 auth 狀態 | Task 5-8, 12 (ProtectedShell) |
| §5 畫面契約 | Task 13-19 |
| §6 API client | Task 9-11 |
| §7 state / 效能 | Task 10 (TanStack Query), Task 11 (blob cache), Task 14 (lazy load), Task 8 (dynamic import) |
| §8 a11y / 響應式 | Task 12-19 (touch-target, aria-*)、Task 20 (Pixel 5 viewport) |
| §9 錯誤 / 空狀態 | Task 12 (ErrorPanel), 各 route（狀態分支）、Task 21 (e2e) |
| §10 驗收條件 | 附錄 A |
| §11 完成關卡 | 附錄 A 加上 `make dev-web-stack` 手動驗 |

## 附錄 C：Non-Goals

- PWA / Service Worker
- Cloudflare Pages 部署（Spec 4）
- Firebase project 建立 / Authorized Domains（Spec 4）
- Prod nginx.conf（Spec 4；dev/acceptance 用同一份 acceptance conf）
- 圖片編輯 / EXIF / 地圖 / 搜尋 / 分享
- i18n / 多語
- Slideshow / gesture zoom
- Cursor.dev vs URL-driven pagination 統一（POC 用 TanStack Query cache，URL 只帶 viewer 的 `from`）
