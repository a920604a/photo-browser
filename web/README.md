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
