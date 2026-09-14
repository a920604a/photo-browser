# 03 — 驗收清單（spec §11）

每一項都要有證據。「看起來沒問題」不算。

---

## A. 本機可驗的部分（不需要 NAS 或 Cloudflare）

這些在開發機上就能跑完，實作期間已全部通過一次；部署前建議再跑一遍：

```
make test                 # backend
make test-web             # frontend unit
make build-prod && make verify-image
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml template
make prodcheck-up && make verify-deployment
make restore-drill
make prodcheck-down
```

- [ ] `make test` exit 0
- [ ] `make test-web` exit 0
- [ ] `make verify-image` 全部 `ok`
- [ ] `verify-tunnel-config` OK
- [ ] `make verify-deployment` 全部 `ok`
- [ ] `make restore-drill` 印出 `restore drill PASSED`
- [ ] `make build-web-prod`（帶真實 Firebase 值）exit 0 且 `Bundle guard OK`

---

## B. 對真實部署的自動化檢查

token 取得方式：瀏覽器 devtools → Network → 任一 `/api/v1` request →
Request Headers → `Authorization: Bearer ...` 複製後面那串。

```
bash scripts/verify-deployment.sh \
  --base-url https://<PUBLIC_API_HOST> \
  --member-token "<member jwt>" \
  --admin-token  "<admin jwt>" \
  --denied-token "<unallowlisted jwt>" \
  --edge
```

- [ ] 全部 `ok`，最後印出 `verify-deployment OK`

`--edge` 會額外檢查 `cf-cache-status` 不是 HIT，以及 DSM 內容不會從
API hostname 洩漏。

---

## C. 逐項驗收（spec §11）

- [ ] **LAN 外的 allowlisted member 可完成完整 browse flow**
      用手機的行動網路（**關掉 Wi-Fi**）開 `https://<PUBLIC_APP_HOST>`，
      Google 登入 → 照片格狀清單 → 開一張圖 → 登出。
- [ ] **Unallowlisted 使用者取不到 metadata 或 image bytes**
      用第三個身分登入，落在 `/forbidden`；API 層由 B 段自動檢查覆蓋。
- [ ] **NAS 未暴露 DSM／SMB／filesystem／DB／管理埠**
      B 段的 `--edge` 已檢查 DSM 內容；另外手動確認
      `https://<PUBLIC_API_HOST>/webman/` 不是 DSM 畫面，
      且 `docker compose ... ps --format '{{.Name}} {{.Ports}}'` 的 Ports 欄全空。
- [ ] **人工複製新照片後能收斂**
      在 `photos` share 的某個 album 放一張新照片 → Task Scheduler 按 **Run**
      → 重整前端 → 新照片出現。
- [ ] **Original bytes 由 Nginx 傳送（backend 授權後）**
      devtools 看 `/api/v1/photos/<id>/original` 回 200 且
      `content-type: image/jpeg`；backend log 有該請求但傳輸 body 為 0。
- [ ] **Mobile layout 在 360 px 可用**
      devtools 切 360×640，無橫向捲動、底部導覽可點。
      （Spec 3 的 Playwright 已自動驗過同一條件。）
- [ ] **Logs 足以診斷 tunnel / DB / auth / scan / disk 問題**
      三個 service 各看一次 log，確認有 request id、status、duration。
      **確認沒有 bearer token 出現：**

      ```
      docker compose -f deploy/compose/docker-compose.prod.yml \
        --env-file deploy/compose/.env.prod logs --no-color \
        | grep -ciE 'bearer [a-z0-9._-]{20,}'
      ```

      期望印 `0`。
- [ ] **Backup/restore drill 成功且不依賴 thumbnail backup**
      A 段的 `make restore-drill` 已涵蓋流程。NAS 上另外驗證一次真實備份可讀：

      ```
      docker run --rm -v /volume1/backup/photo-browser:/b photo-browser-api-acceptance \
        sqlite3 /b/<latest>.db "PRAGMA integrity_check; SELECT count(*) FROM users;"
      ```
- [ ] **在 target NAS 記錄 idle 與 scan-time CPU/memory**
      依 `docs/deploy/resource-measurements.md` 在 NAS 上跑量測，
      把結果 commit 回來，並據此調整 `.env.prod` 的 memory limit。
- [ ] **DSM 通知可用**
      依 `02-nas-deployment.md` §10 做過一次失敗通知測試並收到信。

---

## D. 簽收

| 項目 | 日期 | 執行者 | 備註 |
|---|---|---|---|
| A 本機檢查全過 | | | |
| B 自動化檢查全過 | | | |
| C 逐項驗收全過 | | | |
| 資源數據已記錄 | | | |

全部通過後，POC 才可進入 MVP 決策（spec §12）。
若有失敗項目，針對**實際缺口**另寫小型 follow-up spec（例如 HEIC codec 或
viewer-size derivative），**不要**擴張整體架構。

---

## Troubleshooting

| 症狀 | 最可能原因 | 怎麼確認 |
|---|---|---|
| 登入後一直停在 Access denied | UID 沒進 allowlist，或抄成 email 而不是 Firebase User UID | `photo-app admin list-users` 比對 |
| 所有登入都失敗、token 看起來有效 | NAS 時鐘偏移 | NAS 上 `date -u` vs 筆電 `date -u` |
| 前端載入但所有 API 都 CORS 失敗 | `ALLOWED_ORIGINS` 沒有 `https://<PUBLIC_APP_HOST>` | `.env.prod` 與 devtools console |
| API hostname 回 `error 1033` | tunnel 沒連上 | `docker compose ... logs cloudflared` |
| API hostname 直接斷線、沒有回應 | nginx `server_name` 與 `PUBLIC_API_HOST` 不一致（回 444） | 兩處設定比對 |
| nginx 起不來，log 說 `worker_processes not allowed here` | envsubst 輸出路徑錯了 | 確認 compose 有 `NGINX_ENVSUBST_OUTPUT_DIR: /etc/nginx` |
| nginx 起不來，log 說 `/run/nginx.pid permission denied` | 少了 `pid /tmp/nginx.pid;` | 看 `nginx.prod.conf` 開頭 |
| photo-app 起不來，`serve.lock permission denied` | data 目錄擁有者不是 `APP_UID` | `ls -la /volume1/docker/photo-browser/data` |
| 縮圖全空、原圖正常 | thumbnail volume 權限或空間 | `df -h` 與 index log 是否有 `low_disk_space` |
| 索引 exit 137 | 記憶體上限太低 | 提高 `APP_MEM_LIMIT` 後重跑 |
| 每小時收到 DSM 失敗通知 | 排程指到舊腳本，或真的有錯 | 看 `index.log` 是 `index skipped` 還是 `index FAILED` |
