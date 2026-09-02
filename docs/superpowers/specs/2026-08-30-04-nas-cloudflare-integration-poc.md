# Spec 4 — Synology NAS 與 Cloudflare 整合 POC

**狀態：** 最終整合 Spec  
**前置依賴：** Spec 1–3  
**完成後解鎖：** POC 驗收決策

## 1. 目標

部署完整 vertical slice，讓 Internet 上的 allowlisted Family Member 能透過 Cloudflare-hosted Frontend 登入並安全瀏覽 NAS originals，同時不暴露 DSM、SMB、filesystem path 或 Backend port。

## 2. 範圍

包含 NAS architecture/codec validation、reproducible container images、Compose topology、Nginx、photo application、`cloudflared`、Cloudflare Pages、Cloudflare Tunnel、Firebase production config、Synology scheduled indexing、secrets、filesystem permissions、backup、restore、logs、health check 與 end-to-end security verification。

不包含 high availability、Cloudflare Worker backend、protected media shared edge cache、Prometheus/Grafana、Kubernetes、external database 與 public sharing。

## 3. 前置檢查關卡

### 已知目標環境

| 項目 | 已確認值 | 設計影響 |
|---|---|---|
| NAS | Synology DS716+II | 使用 x86_64 container image |
| DSM | 7.1.1-42962 Update 9 | 部署前先檢查可用的 DSM 與安全性更新 |
| CPU | Intel Celeron N3160（Braswell） | 4 cores / 4 threads、1.6 GHz、burst 2.24 GHz；不假設新式 CPU instruction set |
| 已安裝 RAM | 2,048 MB DDR3 | Thumbnail concurrency 固定為 1；避免多 worker 與高 idle-memory runtime |
| 儲存容量 | 8 TB | 部署前仍需確認 volume 可用剩餘空間；thumbnail cache 與 SQLite 不得吃滿 volume |
| Container runtime | Docker / Container Manager 已安裝 | 可使用 Docker Compose topology；實作時再確認可用 Compose command/version |
| CPU 位元 | Intel 64-bit | Go、Nginx、cloudflared、libvips 使用 `linux/amd64` build |

本機已確認只有 2 GB RAM，不以 CPU 可支援的 8 GB 上限進行容量估算。

### 此硬體的固定部署限制

- Go binary 與所有 native dependencies 必須以相容 Braswell 的保守 `linux/amd64` target 建置，不使用 `GOAMD64=v3/v4`。
- Thumbnail generation 預設 concurrency 固定為 1，完成實測前不提高。
- API 只啟動單一 process，不使用多 worker configuration。
- POC codec 僅啟用 JPEG、PNG、WebP；HEIC 必須另做 memory/codec probe。
- 初始 full scan 與 thumbnail rebuild 在離峰執行；periodic scan 每 60 分鐘。
- 必須用最大代表性照片量測 peak RSS，避免 2 GB RAM 機型觸發 swap 或 OOM。
- Thumbnail cache 沒有預先保留固定容量；部署前記錄 volume free space，並在低空間時停止產生 thumbnail、保留 original 不受影響。

Build image 前必須記錄：

- Docker/Container Manager 的實際版本與可用 Compose command；
- libvips 對 JPEG、PNG、WebP、EXIF orientation 與 WebP output 的支援；
- `/photos`、application-data 與 thumbnail volume paths；
- 8 TB volume 的實際可用剩餘空間；
- Photo 數量不要求管理者事先提供，由第一次 read-only scan 自動統計並寫入 `scan_runs.files_seen`；
- 具備 read-only Photo access 的 service UID/GID；
- Cloudflare zone 與兩個 hostnames；
- Firebase project ID 與 enabled provider；
- portrait、landscape、large image、invalid EXIF 等代表性 samples。

HEIC 不得默默進入 POC scope。Codec probe 失敗時即維持 unsupported。

## 4. 部署拓撲

```text
photos.example.com
  → Cloudflare Pages static frontend

photos-api.example.com
  → Cloudflare Tunnel
  → NAS cloudflared container
  → NAS Nginx container
  → photo-app container
  → SQLite / originals / thumbnails
```

Compose services：

```text
nginx
photo-app
cloudflared
```

Indexer 使用同一 application image，由 Synology Task Scheduler 或 one-shot Compose command 執行，不建立第四個 permanent service。

## 5. Volume 與 Permission 契約

```text
NAS photos share      → /srv/photos:ro
NAS app-data          → /srv/data:rw
NAS thumbnails        → /srv/thumbnails:rw for indexer
deployment config     → read-only where possible
```

- API runtime 讀取 originals/thumbnails，但永不修改 originals。
- Nginx 讀取 originals 與 thumbnails。
- Indexer 只寫 SQLite 與 thumbnails。
- Containers 以 non-root 執行並 drop unnecessary capabilities。
- Application、DB 與 internal Nginx port 不得 publish 到 Internet-facing host interface。
- 禁止為本 Application 設定 Router port forwarding。

## 6. Cloudflare 契約

### Pages

- Production frontend origin：`https://photos.example.com`。
- API base URL：`https://photos-api.example.com/api/v1`。
- Build 只能包含 Firebase public web config，不得包含 admin credential 或 Tunnel token。

### Tunnel

- `cloudflared` 從 NAS 主動建立 outbound connection。
- 僅一個 public hostname route 到 internal Nginx photo service。
- 不得有 ingress rule route 到 DSM、SMB、SSH、Container Manager 或 catch-all NAS origin。
- Tunnel credential 以 secret/restricted file mount，排除於 source control 與 image layer。

### Caching

- Pages assets 可使用 content hash 做 public cache。
- `/api/` 與 protected media 不得套用 Cloudflare shared cache rule。
- POC 不使用 Cloudflare Access，因 Firebase + allowlist 已定義 login flow。

## 7. Firebase Production 契約

- Firebase authorized domain 只加入 production Pages hostname。
- POC 只啟用一個已決定的 provider。
- Backend 使用 exact Firebase project ID。
- 第一位 admin 透過 local bootstrap CLI 建立。
- 建立一個 member 與一個刻意不在 allowlist 的 test identity。
- NAS 必須透過 NTP 同步時間，否則 token verification 不可靠。

## 8. 維運

### Scheduling

- Synology Task Scheduler 每 60 分鐘執行 incremental scan。
- Manual admin trigger 處理需要立即更新的 ingestion。
- Concurrent scan 回傳 `409`/already-running，不重複工作。

### Health

- Container liveness 內部使用 `/api/v1/health/live`。
- Readiness 使用 `/api/v1/health/ready`，確認 SQLite readable 且 schema compatible。
- 單一 Photo/thumbnail 缺失不得令整個 service unready。

### Logs

- Container logs 必須 bounded 並 rotation。
- 記錄 request ID、route、status、duration、scan ID、counters 與穩定但去識別的 user identity。
- 不得記錄 bearer token、Tunnel credential、Firebase service credential；public response 不得包含 SQL error 或 absolute filesystem path。

### Resource Limits

- Thumbnail concurrency 預設為 1。
- CPU/memory limits 必須在 target NAS 實測後設定，且允許一張代表性最大圖片完成 decode。
- repeated container restart、volume near-full 與 backup failure 透過 Synology alert 通知。

## 9. 備份與復原

### 備份

- Originals 使用既有 Synology backup/snapshot 流程，並保有 off-NAS copy。
- SQLite 每日使用 SQLite online backup，不直接複製 live `.db`。
- 備份 deployment config 與 recovery instructions。
- Firebase/Cloudflare identifiers 與 encrypted credentials 納入既定 secret backup process。

Thumbnails 是 derived data，不需備份。

### Restore Drill

1. 部署相同 pinned application version。
2. Restore originals 與 config。
3. Restore SQLite backup，使 allowlist 回復。
4. 若只 restore allowlist data，重建所有 derived catalogue rows。
5. Rebuild thumbnails。
6. 驗證 admin/member/unallowlisted identities。
7. 驗證 internal media URLs 仍不可直接存取。

POC recovery targets：

- Allowlist RPO：24 hours；
- Service RTO：數小時，主要取決於 thumbnail rebuild；
- Original Photo RPO：沿用既有 NAS backup policy。

## 10. 端到端驗證

### Functional Path

```text
Internet browser
  → load Cloudflare Pages
  → Firebase login
  → /me allowlist authorization
  → list album/timeline
  → authenticated thumbnail request
  → open authenticated original
  → logout
```

### Security Checks

- DSM 與 SMB 無法透過 Photo hostname 抵達。
- Backend 與 internal Nginx ports 無法從 Public Internet 連線。
- 直接 `/internal-media/...` request 失敗。
- Valid unallowlisted Firebase identity 對 API/media 都得到 `403`。
- Disabled member 從下一次 request 起失去授權。
- Traversal variants 與 stale thumbnail keys 失敗。
- Protected responses 不存在於 Cloudflare shared cache。
- Browser/network logs 的 URL 不含 token。

### Recovery Checks

- 移除 thumbnail cache 後，執行 rebuild 可恢復 thumbnails。
- 重建 filesystem-derived rows 可恢復 catalogue。
- Restore SQLite backup 可恢復 allowlist。
- Failed scan 不刪除既有 catalogue entries。

## 11. POC 驗收條件

- LAN 外的 allowlisted member 可完成完整 browse flow。
- Unallowlisted Firebase user 無法取得 metadata 或 image bytes。
- NAS 未透過 Cloudflare 暴露 DSM、SMB、filesystem、DB 或 backend management port。
- 人工複製新照片後，periodic/manual scan 能收斂資料。
- Original bytes 在 Backend authorization 後由 Nginx 傳送。
- Mobile layout 在 360 px width 可用。
- Logs 與 health endpoints 足以診斷 Tunnel、DB、auth、scan 與 disk failures。
- Backup/restore drill 不依賴 thumbnail backup 也能成功。
- 在 target NAS 記錄 idle 與 scan-time CPU/memory。

## 12. 是否進入 MVP 的決策

只有全部 acceptance criteria 都在實際 NAS 通過，POC 才能進入 MVP。若失敗，應針對實際缺口另寫小型 follow-up spec，例如 HEIC codec 或 viewer-size derivative，不擴張整體架構。
