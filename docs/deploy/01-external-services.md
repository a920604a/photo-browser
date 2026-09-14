# 01 — 外部服務設定（網域 / Cloudflare / Firebase）

**這份文件要人工執行。** 做完之後，你手上會有下面這張表的所有值；
`02-nas-deployment.md` 會逐一用到。

| 值 | 填在哪裡 | 你的值 |
|---|---|---|
| `PUBLIC_APP_HOST` | `.env.prod`、Firebase authorized domain | |
| `PUBLIC_API_HOST` | `.env.prod`、`cloudflared/config.yml`、Pages 的 `VITE_API_BASE_URL` | |
| Tunnel ID | `cloudflared/config.yml`（兩處） | |
| Tunnel credential 檔名 | `<tunnel-id>.json`，放 NAS | |
| `FIREBASE_PROJECT_ID` | `.env.prod` | |
| `VITE_FIREBASE_API_KEY` | Pages 環境變數 | |
| `VITE_FIREBASE_AUTH_DOMAIN` | Pages 環境變數 | |
| `VITE_FIREBASE_PROJECT_ID` | Pages 環境變數 | |
| `VITE_FIREBASE_APP_ID` | Pages 環境變數 | |
| admin / member / denied 三個 Firebase User UID | `photo-app admin add-user` | |

時間預估：40–60 分鐘，其中 DNS 生效可能要等更久。

---

## 1. 取得網域並接上 Cloudflare

1. 註冊一個網域。可以用 Cloudflare Registrar 直接買（少一次 nameserver 搬遷），
   或在任何註冊商買再搬過來。
2. Cloudflare dashboard → **Add a site** → 輸入網域 → 選 **Free** 方案。
3. Cloudflare 會給你兩個 nameserver。到註冊商把 nameserver 改成這兩個。
4. **怎麼知道成功了：** zone 狀態從 `Pending Nameserver Update` 變成 **Active**
   （幾分鐘到幾小時）。用指令確認：

   ```
   dig +short NS <your-domain>
   ```

   輸出應該是兩個 `*.ns.cloudflare.com`。

5. 決定兩個子網域並填進上面的表：
   - `PUBLIC_APP_HOST` — 前端，例如 `photos.<your-domain>`
   - `PUBLIC_API_HOST` — API 與圖片，例如 `photos-api.<your-domain>`

   **兩者必須不同。** 前端是靜態檔案走 Pages，API 走 Tunnel 進 NAS；
   混在同一個 hostname 會讓快取規則難以分離，而 spec 明令 `/api/` 與
   protected media 不得進共用快取。

---

## 2. 建立 Cloudflare Tunnel

在**你的筆電**上做（不是 NAS），因為需要瀏覽器登入：

1. 安裝 cloudflared：`brew install cloudflared`
2. 登入並授權 zone：

   ```
   cloudflared tunnel login
   ```

   瀏覽器會開啟，選你剛才的網域。成功後會寫入 `~/.cloudflared/cert.pem`。

3. 建立 tunnel：

   ```
   cloudflared tunnel create photo-browser
   ```

   輸出包含 tunnel id（UUID）與 credential 檔路徑
   `~/.cloudflared/<tunnel-id>.json`。**把 id 填進上面的表。**

4. 建立 DNS route：

   ```
   cloudflared tunnel route dns photo-browser <PUBLIC_API_HOST>
   ```

5. **怎麼知道成功了：**

   ```
   cloudflared tunnel list
   dig +short <PUBLIC_API_HOST>
   ```

   前者列出 `photo-browser` 與其 id；後者回 Cloudflare 的 IP（104.x / 172.6x 等），
   **不是**你家的對外 IP。如果回的是你家 IP，表示建成了 A record 而不是 tunnel
   route——刪掉重來，不要繼續。

6. 把 credential 檔案安全地搬到 NAS（`02-nas-deployment.md` 會用）。
   **不要**放進 git、不要用 email 或聊天軟體傳。建議直接 `scp`：

   ```
   scp ~/.cloudflared/<tunnel-id>.json <you>@<nas>:/tmp/
   ```

7. 填 `deploy/cloudflared/config.yml` 的 placeholder：
   `REPLACE_WITH_TUNNEL_ID`（兩處）與 `REPLACE_WITH_PUBLIC_API_HOST`（兩處）。
   填完在 repo 根目錄跑：

   ```
   bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed
   ```

   必須印出 `verify-tunnel-config OK`。這支腳本會擋下沒換掉的 placeholder、
   任何指向 DSM/SMB/SSH 的 ingress rule、缺少 catch-all，以及內嵌的憑證。

---

## 3. Cloudflare Pages（前端）

1. Cloudflare dashboard → **Workers & Pages** → **Create** → **Pages** →
   **Connect to Git** → 選這個 repo。
2. Build 設定：

   | 欄位 | 值 |
   |---|---|
   | Framework preset | None |
   | Build command | `npm ci && npm run build && npm run guard` |
   | Build output directory | `dist` |
   | Root directory | `web` |

   另外在 Environment variables 加 `NODE_VERSION=20`。

   `npm run guard` 放進 build command 是刻意的：它會在 bundle 含有 testauth
   程式碼時讓 **build 失敗**，而不是默默部署出去。這個 guard 在開發時就抓過一次
   真實錯誤（production 標籤配 dev 模式的 bundle）。

3. Environment variables（Production）照 `web/.env.production.example` 填。
   `VITE_TESTAUTH_URL` 與 `VITE_DEV_USERS` **留空**。
   `VITE_API_BASE_URL` 填 `https://<PUBLIC_API_HOST>/api/v1`。
4. 部署完成後，Pages → **Custom domains** → 加上 `PUBLIC_APP_HOST`。
5. **怎麼知道成功了：**

   ```
   curl -sI https://<PUBLIC_APP_HOST> | head -3
   curl -s https://<PUBLIC_APP_HOST> | grep -o '<title>[^<]*</title>'
   ```

   回 `200` 且 title 是 `Photo Browser`。此時開網頁會停在 login 畫面，
   而 **Google 登入會失敗**——Firebase 還沒設 authorized domain，下一節處理。

---

## 4. 快取規則

Cloudflare 預設不會快取 `/api/` 這類無副檔名的回應，但 spec 要求明確保證，
而且來源已經送 `Cache-Control: no-store`（JSON）與 `private`（媒體）。
加一條規則做第二道防線：

1. dashboard → 你的 zone → **Caching** → **Cache Rules** → **Create rule**
2. 條件：`Hostname equals <PUBLIC_API_HOST>` → 動作：**Bypass cache**
3. **怎麼知道成功了：** 驗收時跑
   `bash scripts/verify-deployment.sh ... --edge`，其中的 `cf-cache-status`
   檢查會確認 API 回應沒有來自共用快取。

---

## 5. Firebase 專案

1. [console.firebase.google.com](https://console.firebase.google.com) → **Add project**。
   不需要 Google Analytics。
2. **Build → Authentication → Get started → Sign-in method → Google → Enable。**
   只開這一個 provider（spec：POC 只啟用一個已決定的 provider）。
   前端用的是 `signInWithPopup(GoogleAuthProvider)`，所以必須是 Google。
3. **Authentication → Settings → Authorized domains**：
   - 加入 `<PUBLIC_APP_HOST>`
   - **不要**加 `PUBLIC_API_HOST`（API 不做瀏覽器登入）
4. **Project settings → General → Your apps → Add app → Web**：
   取得 `apiKey` / `authDomain` / `projectId` / `appId` 四個值，填進上面的表，
   再貼進 Pages 的環境變數，**重新 deploy 一次 Pages**。

   這四個值是公開的設計：它們只識別專案，不授權任何事。
   **絕對不要**把 service account key 放進前端環境變數。
5. **Project settings → General → Project ID**：這個值就是 `FIREBASE_PROJECT_ID`，
   backend 用它推導 issuer `https://securetoken.google.com/<project-id>`。
6. 準備三個測試身分（用你自己的 Google 帳號家族即可）：
   - 一個會成為 **admin**
   - 一個會成為 **member**
   - 一個**刻意不加進 allowlist**，用來驗 403

   三個都先在前端登入一次（會被擋在 `/forbidden`，這是正常的），
   然後到 **Authentication → Users** 抄下三個 **User UID** 填進上面的表。

   注意抄的是 **User UID**（一串英數字），不是 email。allowlist 比對的是 UID。
7. **怎麼知道成功了：** 開 `https://<PUBLIC_APP_HOST>`，按 Sign in with Google，
   popup 能完成登入且畫面落在 **Access denied**（`/forbidden`）。
   這代表 Firebase 認證成功、但 allowlist 還沒有你——正是此刻該有的狀態。

   若 popup 報 `auth/unauthorized-domain`，回到步驟 3。
