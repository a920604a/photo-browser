# Spec 3 — 行動優先 Web 前端 POC

**狀態：** Spec 2 API 契約完成後可執行  
**前置依賴：** Spec 2  
**完成後解鎖：** Spec 4 end-to-end validation

## 1. 目標

交付最小但可用的家庭照片 Browser：Mobile Browser 可以登入、瀏覽 Photos/Albums/Categories/Timeline、開啟 Photo Viewer、查看目前帳號並登出。

## 2. 範圍

包含：

- Firebase login/logout；
- POC 先設定一個 login provider；
- 可 refresh token 的 authenticated API client；
- Photos、Albums、Categories、Profile navigation；
- All Photos grid 與 Year/Month Timeline；
- Album/Category drill-down；
- 使用 original media endpoint 的 Photo Viewer；
- loading、empty、offline/API error、401、403、missing thumbnail states；
- mobile-first responsive layout 與 accessibility basics。

不包含 Photo upload/mutation、Admin UI、client-side editing、offline library、無上限 client cache、advanced gestures、slideshow、map、search、tag、sharing 或 custom design system。

## 3. 導覽與路由

主要 Navigation：

```text
[ Photos ] [ Albums ] [ Categories ] [ Profile ]
```

Logical routes：

```text
/login
/photos
/photos/timeline
/albums
/albums/:albumId
/categories
/categories/:categoryId
/viewer/:photoId
/profile
/forbidden
```

Mobile 使用容易觸及的 bottom navigation。大螢幕可以移動位置，但 information architecture 不變。

## 4. 身分驗證狀態

1. 啟動 Firebase SDK，等待 auth initialization 完成後才決定 route。
2. Signed out 時顯示 Login。
3. Signed in 後，以新的 ID Token 請求 `/api/v1/me`。
4. `200`：進入 application。
5. `401`：強制 refresh token 並只 retry 一次；仍失敗則 logout/顯示 login error。
6. `403`：導向固定 Forbidden 頁並提供 logout。
7. ID Token 不得出現在 URL、analytics event 或 application log。

Frontend 不得將 Firebase login 成功視為已取得照片權限。

## 5. 畫面契約

### Login

- Product name 與私人家庭照片庫的簡短說明；
- POC 使用一個 provider button；
- 清楚顯示 login failure；
- 不承諾 self-registration，也不洩漏 allowlist 詳細資訊。

### Photos

- All Photos / Timeline tabs 或 segmented control；
- Responsive thumbnail grid；
- Cursor-based incremental loading；
- Thumbnail 缺失時顯示 placeholder；
- 點擊 tile 開啟 Viewer。

### Timeline

- 只使用 Timeline API；
- 依 local `taken_at` 的 Year/Month 分組；
- 保留 Backend 排序；
- 不插入沒有 `taken_at` 的照片。

### Albums

- 顯示 cover、Album name、Category name、Photo count；
- 點擊後進入 Album Photo Grid；
- 無 cover 時使用 neutral placeholder。

### Categories

- 顯示 Category name 與 Album count；
- 點擊後顯示 Album List；
- 不出現 create/edit/delete controls。

### Photo Viewer

- 使用 protected original endpoint 顯示選取照片；
- 只在目前已載入 collection 內提供 previous/next；
- 提供 close/back；
- 顯示 filename 與可用的 `taken_at`；
- 不提供 download、delete、rename、move、share 或 EXIF/GPS panel。

「唯讀」代表 application 不提供 mutation。Browser 無法阻止已授權觀看者儲存其能看到的 bytes，UI 不得宣稱具備 DRM。

### Profile

- 顯示 Firebase display name、email、可用的 avatar；
- 顯示 `/me` 回傳的 backend role；
- 提供 Logout。

## 6. API Client 契約

- Base URL 由 build-time environment configuration 提供。
- 每個 API/media request 都加上 `Authorization: Bearer <ID token>`。
- `<img>` 無法直接附加 Authorization header，因此 POC 透過 authenticated API client 將 protected media fetch 成 Blob，再用 object URL 顯示。
- Tile/Viewer unmount 時 revoke object URL，避免 memory growth。
- 限制同時 media fetch 數量，不 preload 整個 Album。
- 只對 transient network/5xx failure 做少量 bounded retry；401/403/404 不得 loop。

Blob 是為了維持 Firebase Bearer authentication 一致性。未來若改 signed cookie，必須另寫 security spec。

## 7. 狀態與效能

- 使用所選 framework 的 native component/state facility 加 Firebase SDK。
- 除非實作證明 auth context 與 route-local state 不足，不新增 global state library。
- Memory 只保留 current page 與 Viewer 相鄰少量照片。
- Lazy-load fold 以下 thumbnails。
- Grid key 使用 stable photo ID。
- 增量 render server pages，禁止一次載入 100K Photo rows。

## 8. 無障礙與響應式要求

- 所有 controls 可用 keyboard 操作，且有 visible focus。
- Images 依情境提供 meaningful 或 empty alternative text。
- Icon-only control 具 accessible name。
- Touch target 約至少 44×44 CSS pixels。
- Viewer 依 platform convention 支援 Escape/back 關閉。
- Layout 從 360 px width 起可用。
- Loading state 不造成破壞性 layout shift。
- Respect reduced motion；POC 不要求動畫。

## 9. 錯誤與空白狀態

| Condition | UI 結果 |
|---|---|
| 無照片 | Empty library message |
| Empty Album/Category | Empty collection message |
| Thumbnail 404 | 顯示 placeholder，Grid 維持可用 |
| Original 404 | Viewer 顯示 Photo unavailable |
| Refresh 後仍 401 | 回到 Login |
| 403 | Forbidden page + Logout |
| API unavailable | Retry action；Navigation 維持可用 |
| Pagination failure | Inline retry，不丟棄已載入 items |

## 10. 驗收條件

- 360 px wide Browser 可完成 login → browse → viewer → logout。
- Firebase login 後，必須等 `/me` 授權成功才進入 Application。
- 所有 navigation sections 符合 route contract。
- Timeline 只依 Year/Month 分組 non-null `taken_at` results。
- Album 與 Category drill-down 能到正確 Photo Grid。
- Media request 帶 authentication，且 object URLs 會被 revoke。
- Pagination 不重新載入或保留整個 library。
- 401、403、empty、thumbnail failure、API unavailable 狀態都可重現。
- 不存在 upload/delete/rename/move/admin photo controls。
- Keyboard focus、accessible labels 與 minimum touch targets 通過 manual checks。

## 11. 完成關卡

Frontend 在 local environment 能與 Spec 2 API 完整運作，才算完成 Spec 3。Cloudflare deployment 與真實 NAS validation 只屬於 Spec 4。
