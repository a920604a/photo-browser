# Spec 1 — 索引與儲存基礎 POC

**狀態：** 可進入實作規劃  
**前置依賴：** 無  
**完成後解鎖：** Spec 2–4

## 1. 目標

從 `/photos/{category}/{album}/{photo}` 建立可重建的本機照片索引，這一階段不提供 HTTP 服務。POC 必須證明 filesystem 的新增、修改與刪除最終能正確同步至 SQLite metadata 與單一 thumbnail cache。

## 2. 範圍

包含：

- 僅支援 Category → Album 兩層目錄；
- JPEG、PNG、WebP；
- SQLite schema 與 migration；
- 讀取 EXIF `DateTimeOriginal`；
- 單一長邊 512 px、quality 80 的 WebP thumbnail；
- 以 `(relative_path, size, mtime_ns)` 偵測變更；
- NEW、CHANGED、UNCHANGED、MISSING reconciliation；
- 單一 indexer process lock；
- scan history 與統計數字；
- 從 original 完整重建。

不包含：HEIC、RAW、video、HTTP API、Firebase、hash deduplication、filesystem watcher、任意目錄深度及多尺寸 thumbnail。

## 3. 固定決策

| 決策 | 採用值 |
|---|---|
| Source of Truth | NAS filesystem |
| Database | SQLite，WAL mode |
| Original 存取 | 唯讀 |
| Thumbnail | WebP、512 px、quality 80、不放大 |
| Fingerprint | path + byte size + nanosecond mtime |
| 無 EXIF 拍攝時間 | `taken_at = NULL` |
| 時間 fallback | 無；禁止使用 filesystem mtime |
| Rename/move | 舊 Photo missing + 新 Photo insert |
| Scan concurrency | 單一 process |

## 4. 檔案系統契約

```text
/photos/
  {category}/
    {album}/
      {photo.jpg|jpeg|png|webp}
```

Scanner 必須：

- DB 只保存 relative path；
- 忽略 hidden file、root 直屬檔案、Category 直屬檔案、第三層目錄與不支援的副檔名；
- 不追蹤 symbolic link；
- 對每類無效項目輸出有上限的 warning，並保留計數；
- root 無法讀取或 walk 中斷時將 scan 標記為 failed；
- 個別既有檔案無法讀取時，將 scan 視為 failed/partial，禁止執行 missing reconciliation。

## 5. 資料模型

### Filesystem-derived tables

```text
categories(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  last_seen_scan_id INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)

albums(
  id INTEGER PRIMARY KEY,
  category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  last_seen_scan_id INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(category_id, name)
)

photos(
  id INTEGER PRIMARY KEY,
  album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  filename TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  file_size INTEGER NOT NULL,
  file_mtime_ns INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  width INTEGER,
  height INTEGER,
  taken_at TEXT,
  thumbnail_key TEXT,
  last_seen_scan_id INTEGER,
  indexed_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(album_id, filename)
)
```

`taken_at` 是 EXIF local datetime，固定格式為 `YYYY-MM-DDTHH:MM:SS`，不得標示為 UTC。

### Operational table

```text
scan_runs(
  id INTEGER PRIMARY KEY,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed')),
  files_seen INTEGER NOT NULL DEFAULT 0,
  files_new INTEGER NOT NULL DEFAULT 0,
  files_changed INTEGER NOT NULL DEFAULT 0,
  files_unchanged INTEGER NOT NULL DEFAULT 0,
  files_removed INTEGER NOT NULL DEFAULT 0,
  warnings_count INTEGER NOT NULL DEFAULT 0,
  errors_count INTEGER NOT NULL DEFAULT 0,
  error_summary TEXT
)
```

必要 indexes：

- `photos(album_id, taken_at DESC, id DESC)`；
- `taken_at IS NOT NULL` 條件下的 partial index：`photos(taken_at DESC, id DESC)`；
- `albums(category_id, name)`。

## 6. 掃描流程

1. 取得 advisory file lock；若已被持有，以明確的「already running」結果結束。
2. 新增狀態為 `running` 的 scan row。
3. 僅 walk filesystem 兩層目錄。
4. 對每個支援的檔案取得 size 與 `mtime_ns`。
5. 與相同 relative path 的 DB row 比較：NEW 解析 metadata、產生 thumbnail 並 insert；CHANGED 重新解析並 atomic replace thumbnail；UNCHANGED 不 decode image，只標記本次已看見。
6. 每 100–500 個檔案以短 transaction commit，不得讓整次 walk 使用一個長 transaction。
7. 只有完整且無致命錯誤的 walk 才能刪除 unseen photos、其 thumbnails，以及空的 albums/categories。
8. 成功時標記 `succeeded`；其他情況標記 `failed` 並跳過所有 missing deletion。
9. 釋放 lock。

Scan 失敗時，系統必須保留上一版可用索引。

## 7. Thumbnail 契約

- Resize 前套用 EXIF orientation，保持長寬比且不放大小圖。
- 移除 EXIF、GPS 與其他 metadata。
- 先在目的目錄產生 temporary file，再 atomic rename。
- Key 格式：`{photo_id}-{file_mtime_ns}-{file_size}.webp`。
- 產生失敗時仍可保留 Photo row，但 `thumbnail_key = NULL`，UI 顯示 placeholder。
- 成功 reconciliation 或專用 rebuild command 可以刪除 orphan thumbnail。

## 8. CLI 契約

```text
photo-app index
photo-app index --rebuild-thumbnails
photo-app rebuild
```

- `index`：incremental scan。
- `--rebuild-thumbnails`：保留 metadata identity，重新產生 derived thumbnails。
- `rebuild`：重建 derived index，但保留 Spec 2 導入的 application-owned tables。

路徑與 concurrency 屬於 deployment configuration，不得成為 client 可任意提供的 command data。

## 9. 失敗語意

| Failure | 必要結果 |
|---|---|
| Root unavailable | Scan failed；不刪 metadata |
| 個別 image decode 失敗 | stat 成功時仍建立 Photo；thumbnail nullable；記錄 warning |
| 個別既有 path 無法讀取 | Scan failed；不做 missing reconciliation |
| DB write 失敗 | Scan failed；先前已 commit 的 batch 維持安全 |
| Thumbnail 時 disk full | 不留下不完整 final file |
| Process 被終止 | 下次執行將殘留 running row 標示為 abandoned/failed |
| 重複啟動 | 第二個 process 不執行工作 |

## 10. 驗收條件

- Fixture tree 能產生預期的 categories、albums、photos、EXIF time 與 thumbnails。
- 無變更的第二次 scan 不 decode image，也不重建 thumbnail。
- Size 或 mtime 改變後會重新讀 EXIF 並改變 thumbnail key。
- 只有完整 scan 成功後，移除檔案才會刪除其 metadata 與 thumbnail。
- 模擬 walk failure 時，missing rows 維持不變。
- 缺少或無效 `DateTimeOriginal` 時必須是 `NULL`，不可使用 filesystem mtime。
- Symbolic link 與第三層目錄被忽略。
- 刪除 SQLite derived rows 與 thumbnail directory 後，執行 `rebuild` 可恢復 catalogue。

## 11. 完成關卡

CLI 與 fixtures 能在沒有 HTTP server 的情況下證明 deterministic convergence，才算完成 Spec 1。Spec 2 必須沿用本 schema，不得建立平行資料模型。
