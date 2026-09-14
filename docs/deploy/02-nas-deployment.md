# 02 — NAS 部署

**前置：** `01-external-services.md` 全部完成，且 `docs/deploy/preflight-report.md`
已在這台 NAS 上跑過且沒有 BLOCKING 項目。

以下指令都在 NAS 上經 SSH 執行，除非另外註明。

---

## 1. DSM 準備

1. **Control Panel → Terminal & SNMP → Enable SSH service**（部署完可關掉）。
2. **Control Panel → Regional Options → Time**：
   - Time zone 設好
   - **Synchronize with NTP server** 打開，選 `pool.ntp.org`

   這不是可選項。Firebase token 的 `exp`/`iat` 驗證依賴正確時間；NAS 時鐘偏移
   超過幾分鐘會讓**所有登入失敗**，而錯誤訊息看起來像「token 無效」，
   很難聯想到時鐘。

   確認：`date -u`，與你筆電的 `date -u` 相差不超過數秒。

3. **Control Panel → Shared Folder**：確認照片 share（例如 `photos`）存在，
   且目錄結構是 `<category>/<album>/<photo>.jpg` **兩層**。
   索引器不接受任意深度——單層或三層的目錄會被記成 warning 並略過。
4. 建立應用目錄：

   ```
   sudo mkdir -p /volume1/docker/photo-browser/{data,thumbnails,repo}
   sudo mkdir -p /volume1/docker/photo-browser/data/cloudflared
   ```

5. 決定服務身分。用一個**只有照片 share 唯讀權限**的 DSM 使用者：

   ```
   id <service-user>
   ```

   把 uid/gid 記下來，稍後填進 `.env.prod` 的 `APP_UID` / `APP_GID`。

   ```
   sudo chown -R <uid>:<gid> /volume1/docker/photo-browser/{data,thumbnails}
   sudo chmod 750 /volume1/docker/photo-browser/data
   ```

   照片目錄**不要**改擁有者——它以 `:ro` 掛載，服務只需要讀取權限。

---

## 2. 取得 repo 與填設定

```
cd /volume1/docker/photo-browser/repo
git clone <repo-url> .
cp deploy/compose/.env.prod.example deploy/compose/.env.prod
vi deploy/compose/.env.prod     # 填入 01 文件的值 + 上一步的 uid/gid
```

安裝 tunnel credential：

```
sudo mv /tmp/<tunnel-id>.json /volume1/docker/photo-browser/data/cloudflared/
sudo chown <uid>:<gid> /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
sudo chmod 0400        /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
```

確認設定沒有漏填：

```
docker compose -f deploy/compose/docker-compose.prod.yml \
  --env-file deploy/compose/.env.prod config | grep -c '\${'
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed
```

期望：第一個印 `0`（沒有未展開的變數）；第二個印 `verify-tunnel-config OK`。

---

## 3. 建置映像

```
make test          # 先確認在 NAS 的 CPU 上測試全過
make build-prod
make verify-image
docker build --target api-acceptance -t photo-browser-api-acceptance .
```

期望：前三個 exit 0，`verify-image OK`。
最後一行是備份腳本的完整性檢查需要的（DSM 沒有 `sqlite3` 執行檔）。

`make test` 在 Braswell 上會比開發機慢很多，十分鐘等級是正常的。

---

## 4. 首次索引（離峰執行）

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  run --rm --no-deps photo-app index
```

期望：輸出 `scan: seen=N new=N ...`，`warnings=0`。
把 `seen` 的數字抄進 `docs/deploy/preflight-report.md` 的 Photo count 欄位。

| 症狀 | 原因 | 處理 |
|---|---|---|
| `seen=0` | 目錄不是兩層 `<category>/<album>/<photo>` | 整理目錄後重跑 |
| exit 137 | 被 OOM kill | 提高 `.env.prod` 的 `APP_MEM_LIMIT` 後重跑；索引可重入，已完成的不會重做 |
| `warning: low_disk_space` | thumbnail volume 剩餘空間低於 `THUMBNAIL_MIN_FREE_BYTES` | 清空間；照片仍會被編目，只是沒有縮圖 |

---

## 5. 建立 allowlist

用 `01-external-services.md` 抄下來的 Firebase **User UID**：

```
CF="-f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod"
docker compose $CF run --rm --no-deps photo-app admin add-user --uid=<admin-uid>  --email=<admin-email>  --role=admin
docker compose $CF run --rm --no-deps photo-app admin add-user --uid=<member-uid> --email=<member-email> --role=member
```

**第三個身分不要加**——它就是用來驗 403 的。

確認：

```
docker compose $CF run --rm --no-deps photo-app admin list-users
```

---

## 6. 啟動

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod up -d
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod ps
```

期望：三個 service 都在跑，`photo-app` 狀態 `healthy`。

**確認沒有任何 host port 被開出來：**

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  ps --format '{{.Name}} {{.Ports}}'
```

期望：Ports 欄全部是空的。只要看到 `0.0.0.0:...->...`，**立刻停下**——
spec 明令 application、DB 與 internal nginx port 不得 publish 到對外介面。

---

## 7. 排程每小時索引

**Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script**

- User: `root`
- Schedule: 每天、每小時重複一次，**起始時間設在離峰**
- Run command:

  ```
  bash /volume1/docker/photo-browser/repo/scripts/nas-index.sh
  ```

確認：手動按 **Run** 一次，然後看
`/volume1/docker/photo-browser/data/index.log`，最後幾行應該有 `=== index ok ===`。

腳本把「鎖被佔住」（exit 3）當成正常結果並回傳 0，所以首次長時間索引期間的
每小時排程不會一直寄失敗通知。

---

## 8. 排程每日備份

同樣用 Task Scheduler，每天一次（RPO 24 小時）：

```
bash /volume1/docker/photo-browser/repo/scripts/backup-sqlite.sh \
  --compose /volume1/docker/photo-browser/repo/deploy/compose/docker-compose.prod.yml \
  --dest /volume1/backup/photo-browser --keep 7
```

把 `/volume1/backup/photo-browser` 納入既有的 Synology off-NAS 備份流程。
Thumbnails **不要**備份——它是 derived data，restore 時重建。

---

## 9. 機密與識別值的備份

備份壓縮檔**刻意不含**任何憑證。下列項目要放進你既有的密碼管理／機密備份流程
（spec §9：Firebase/Cloudflare identifiers 與 encrypted credentials 納入既定
secret backup process）：

| 項目 | 從哪裡來 | 遺失的後果 |
|---|---|---|
| Tunnel credential `<tunnel-id>.json` | 01 文件 §2 | 要重建 tunnel 並改 DNS |
| Cloudflare 帳號與 zone | 01 文件 §1 | 需要重新接管網域 |
| Tunnel ID | 01 文件 §2 | 可從 dashboard 查回 |
| Firebase project ID 與四個 `VITE_FIREBASE_*` | 01 文件 §5 | 可從 console 查回 |
| `deploy/compose/.env.prod` | 本文件 §2 | 可重建，但含 uid/gid 與路徑，重建費時 |
| 三個測試身分的 Firebase User UID | 01 文件 §5 | 要重新登入取得 |

確認方式：把上表填好存進密碼管理器後，**假裝 NAS 已毀**，只用密碼管理器裡的
內容 + off-NAS 備份，能不能回答「tunnel 叫什麼名字、credential 在哪」。
答不出來就是還沒備份完整。

---

## 10. DSM 通知

Spec §8 要求 repeated container restart、volume near-full 與 backup failure
都要通知。

1. **Control Panel → Notification → Email**：設好收件信箱並按
   **Send a test message** 確認收得到。
2. **Control Panel → Notification → Rules → Storage**：開啟
   **Volume usage exceeds** 並設在 **85%**。
   thumbnail 的低空間保護要到剩 2 GiB 才啟動，85% 的警示讓你早很多知道。
3. **Task Scheduler** 的兩個任務（index 與 backup）都在 **Settings** 分頁勾選
   **Send run details by email**，並選「only when the script terminates
   abnormally」。`nas-index.sh` 把鎖被佔住回傳 0，所以正常運作時不會吵你。
4. Container 反覆重啟：DSM 的 Container Manager 不會主動通知，用一個每日排程補上。
   Run command：

   ```
   cd /volume1/docker/photo-browser/repo && \
   docker compose -f deploy/compose/docker-compose.prod.yml \
     --env-file deploy/compose/.env.prod \
     ps --format '{{.Name}} {{.Status}}' | grep -v 'Up ' && exit 1 || exit 0
   ```

   任何 service 不是 `Up`（含 `Restarting`）時以 exit 1 結束，DSM 就會寄信。

確認方式：手動 `docker compose ... stop photo-app`，按 **Run** 跑上面那個任務，
應該收到失敗通知；再 `start` 回來。
