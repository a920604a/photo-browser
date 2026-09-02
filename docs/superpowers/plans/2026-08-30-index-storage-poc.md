# 索引與儲存基礎 POC Implementation Plan

> **給 agentic workers：** REQUIRED SUB-SKILL：逐 Task 實作時使用 `superpowers:subagent-driven-development`（建議）或 `superpowers:executing-plans`。每個步驟使用 checkbox（`- [ ]`）追蹤。

**目標：** 建立一個 Go CLI，能將固定兩層照片目錄增量同步至 SQLite，讀取 EXIF `DateTimeOriginal`、產生單一 WebP thumbnail，並在失敗時保留上一版可用索引。

**架構：** 一個 `photo-app` binary 提供 `index`、`index --rebuild-thumbnails`、`rebuild` 三個 CLI flow。Scanner 只負責列舉合法 filesystem entries；Indexer 負責 fingerprint 決策與短 transaction；SQLite 是 query index；`vipsthumbnail` 是唯一影像轉換程序，且永遠單工執行。

**技術棧：** Go 1.26、`modernc.org/sqlite`、`github.com/rwcarlsen/goexif/exif`、`golang.org/x/sys/unix`、libvips `vipsthumbnail`、Docker multi-stage build。

**Spec：** `docs/superpowers/specs/2026-08-30-01-index-storage-poc.md`

## Global Constraints

- Source of Truth 固定為 NAS filesystem；original mount 永遠唯讀。
- 僅接受 `/photos/{category}/{album}/{photo}`，不追蹤 symbolic link 或第三層目錄。
- MVP input 只支援 JPEG、PNG、WebP；不加入 HEIC、RAW、video。
- Fingerprint 固定為 `(relative_path, file_size, file_mtime_ns)`；不計算 hash。
- `taken_at` 只取 EXIF `DateTimeOriginal`；不存在或無效時為 `NULL`，禁止 fallback mtime。
- Thumbnail 固定為 WebP、長邊 512 px、quality 80、不放大、移除 metadata。
- Thumbnail concurrency 固定為 1；不得建立 worker pool。
- SQLite 必須使用 WAL、`foreign_keys=ON` 與 bounded `busy_timeout`。
- 只有成功完成 filesystem walk 才可執行 MISSING deletion。
- 每 200 筆寫入一次短 transaction；不得用單一 transaction 包住完整 scan。
- 不新增 HTTP、Firebase、queue、watcher、ORM、migration framework 或 logging framework。
- `test-photos/` 是使用者提供的 read-only sample；測試只能 copy/link 到 temporary fixture，不得移動、改名或覆寫原檔。
- Target NAS 為 `linux/amd64` Braswell；production build 使用 `GOAMD64=v1`。

---

## 檔案配置

```text
.
├── go.mod                         # Go module 與最少 dependencies
├── go.sum
├── Dockerfile                     # test/build/runtime stages
├── Makefile                       # docker-based test/build commands
├── cmd/photo-app/main.go          # CLI argument parsing 與 exit code
├── internal/config/config.go      # environment-only paths 與 defaults
├── internal/database/database.go  # SQLite open、pragma、migration
├── internal/database/schema.sql   # embedded idempotent schema
├── internal/database/database_test.go
├── internal/catalog/models.go     # Category、Album、Photo、ScanRun value types
├── internal/catalog/store.go      # typed SQL operations 與 batch transaction
├── internal/catalog/store_test.go
├── internal/scanner/scanner.go    # exactly-two-level filesystem walk
├── internal/scanner/scanner_test.go
├── internal/media/metadata.go     # dimensions、MIME、DateTimeOriginal
├── internal/media/metadata_test.go
├── internal/media/thumbnail.go    # vipsthumbnail temp + atomic rename
├── internal/media/thumbnail_test.go
├── internal/indexer/indexer.go    # NEW/CHANGED/UNCHANGED orchestration
├── internal/indexer/indexer_test.go
├── internal/indexer/reconcile.go  # successful-scan-only missing cleanup
├── internal/indexer/reconcile_test.go
├── internal/filelock/filelock_unix.go
├── internal/filelock/filelock_test.go
├── internal/app/run.go             # index/rebuild use cases
├── internal/app/run_test.go
└── scripts/acceptance.sh           # end-to-end disposable fixture check
```

不建立 repository interface、service interface、dependency injection container 或平行 domain DTO；每個 package 只保留實際使用的一個 implementation。

---

### Task 1：建立可重現的 Go 與 libvips 執行環境

**Files：**

- Create: `go.mod`
- Create: `Dockerfile`
- Create: `Makefile`
- Create: `internal/config/config.go`
- Create: `cmd/photo-app/main.go`

**Interfaces：**

- Produces: `config.Load() (config.Config, error)`
- Produces: `app.Run(ctx context.Context, args []string, stdout, stderr io.Writer) int`（Task 7 完成實作）
- Consumes: none

- [ ] **Step 1：寫 config failing test**

建立 `internal/config/config_test.go`：

```go
package config

import "testing"

func TestLoadRequiresAbsolutePaths(t *testing.T) {
	for _, tc := range []struct{ photos, data, thumbs string }{
		{"photos", "/data", "/thumbs"},
		{"/photos", "data", "/thumbs"},
		{"/photos", "/data", "thumbs"},
	} {
		t.Setenv("PHOTO_ROOT", tc.photos)
		t.Setenv("DATA_DIR", tc.data)
		t.Setenv("THUMBNAIL_DIR", tc.thumbs)
		if _, err := Load(); err == nil {
			t.Fatalf("Load(%q, %q, %q) succeeded", tc.photos, tc.data, tc.thumbs)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("THUMBNAIL_DIR", "")
	cfg, err := Load()
	if err != nil { t.Fatal(err) }
	if cfg.PhotoRoot != "/photos" || cfg.DataDir != "/data" || cfg.ThumbnailDir != "/thumbnails" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}
```

- [ ] **Step 2：建立 module 與 container test stage，確認 test 先失敗**

`go.mod`：

```go
module photo-browser

go 1.25.0

require (
	github.com/rwcarlsen/goexif v0.0.0-20190401172101-9e8deecbddbd
	golang.org/x/sys v0.35.0
	modernc.org/sqlite v1.38.2
)
```

先由 container 產生可重現的 `go.sum`：

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.7-bookworm go mod tidy
```

`Dockerfile` 起始內容：

```dockerfile
FROM golang:1.26.7-bookworm AS test
RUN apt-get update && apt-get install -y --no-install-recommends libvips-tools libimage-exiftool-perl sqlite3 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./...

FROM test AS build
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOAMD64=v1
RUN go build -trimpath -ldflags='-s -w' -o /out/photo-app ./cmd/photo-app

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libvips-tools && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/photo-app /usr/local/bin/photo-app
USER 65532:65532
ENTRYPOINT ["photo-app"]
```

Run：`docker build --target test -t photo-browser-test .`  
Expected：FAIL，因 `config.Load` 尚未定義。

- [ ] **Step 3：實作最小 config**

`internal/config/config.go`：

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	PhotoRoot, DataDir, ThumbnailDir string
}

func Load() (Config, error) {
	c := Config{value("PHOTO_ROOT", "/photos"), value("DATA_DIR", "/data"), value("THUMBNAIL_DIR", "/thumbnails")}
	for name, path := range map[string]string{"PHOTO_ROOT": c.PhotoRoot, "DATA_DIR": c.DataDir, "THUMBNAIL_DIR": c.ThumbnailDir} {
		if !filepath.IsAbs(path) { return Config{}, fmt.Errorf("%s must be absolute", name) }
	}
	return c, nil
}

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" { return v }
	return fallback
}
```

`cmd/photo-app/main.go` 先只呼叫 `config.Load()`，遇錯印至 stderr 並 exit 2；未知 command 也 exit 2。

- [ ] **Step 4：加入 Makefile 並驗證**

```make
.PHONY: test build
test:
	docker build --target test -t photo-browser-test .

build:
	docker build --platform linux/amd64 --target runtime -t photo-browser:local .
```

Run：`make test`  
Expected：PASS。

- [ ] **Step 5：Commit**

```bash
git add go.mod go.sum Dockerfile Makefile cmd internal/config
git commit -m "chore: bootstrap photo indexer"
```

若 workspace 仍不是 Git repository，略過 commit command，但不可略過 test。

---

### Task 2：建立 SQLite schema 與 typed store

**Files：**

- Create: `internal/database/schema.sql`
- Create: `internal/database/database.go`
- Create: `internal/database/database_test.go`
- Create: `internal/catalog/models.go`
- Create: `internal/catalog/store.go`
- Create: `internal/catalog/store_test.go`

**Interfaces：**

- Produces: `database.Open(path string) (*sql.DB, error)`
- Produces: `catalog.NewStore(db *sql.DB) *catalog.Store`
- Produces: `StartScan`, `FinishScan`, `FindPhoto`, `UpsertCategory`, `UpsertAlbum`, `InsertPhoto`, `UpdatePhoto`, `MarkPhotoSeen`
- Consumes: `database/sql`

- [ ] **Step 1：寫 migration failing test**

`internal/database/database_test.go`：

```go
func TestOpenCreatesSchemaAndPragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	for _, table := range []string{"categories", "albums", "photos", "scan_runs"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil || name != table { t.Fatalf("missing table %s: %v", table, err) }
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
}
```

Run：`make test`  
Expected：FAIL，因 `database.Open` 尚未存在。

- [ ] **Step 2：加入 embedded schema**

`schema.sql` 必須逐字實作 Spec 1 四張 tables、foreign keys、checks 與三個 indexes，並加上單一 migration table：

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);
```

`database.Open`：

```go
//go:embed schema.sql
var schema string

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { return nil, err }
	db, err := sql.Open("sqlite", path)
	if err != nil { return nil, err }
	db.SetMaxOpenConns(4)
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA synchronous=NORMAL`,
	} {
		if _, err := db.Exec(stmt); err != nil { db.Close(); return nil, err }
	}
	if _, err := db.Exec(schema); err != nil { db.Close(); return nil, err }
	return db, nil
}
```

- [ ] **Step 3：寫 Store transaction test**

測試必須建立 scan、category、album、photo，然後以相同 relative path 查回；`taken_at` 使用 `sql.NullString`。另外驗證刪除 Category 時 Album/Photo cascade delete。

核心 value type：

```go
type Fingerprint struct { Size, MTimeNS int64 }

type Photo struct {
	ID, AlbumID int64
	Filename, RelativePath, MIMEType string
	FileSize, FileMTimeNS int64
	Width, Height int
	TakenAt sql.NullString
	ThumbnailKey sql.NullString
	LastSeenScanID int64
}

type ScanCounts struct { Seen, New, Changed, Unchanged, Removed, Warnings, Errors int64 }
```

Run：`make test`  
Expected：FAIL，因 Store methods 尚未定義。

- [ ] **Step 4：實作最小 SQL Store**

Store 直接持有 `*sql.DB`，不建立 interface。所有時間以 injected `now func() time.Time` 或 method parameter 傳入，測試不得依賴 wall clock。

`StartScan` 必須先將舊的 `running` rows 更新成 `failed`，`error_summary='abandoned by next run'`，再 insert 新 row。`FinishScan` 必須設定 finished time、status 與全部 counters。

- [ ] **Step 5：驗證 schema 與 SQL**

Run：`make test`  
Expected：所有 database/catalog tests PASS。

- [ ] **Step 6：Commit**

```bash
git add internal/database internal/catalog go.mod go.sum
git commit -m "feat: add sqlite photo catalogue"
```

---

### Task 3：建立 exactly-two-level scanner

**Files：**

- Create: `internal/scanner/scanner.go`
- Create: `internal/scanner/scanner_test.go`

**Interfaces：**

- Produces: `scanner.Walk(root string, warn func(scanner.Warning)) ([]scanner.Entry, error)`
- Produces types:

```go
type Entry struct {
	CategoryName, CategoryPath string
	AlbumName, AlbumPath string
	Filename, RelativePath string
	Size, MTimeNS int64
}

type Warning struct { Path, Code string }
```

- Consumes: none

- [ ] **Step 1：寫合法與非法目錄 failing tests**

測試在 `t.TempDir()` 建立：

```text
旅遊/日本/a.jpg                 accepted
旅遊/日本/B.WEBP                accepted
旅遊/日本/deeper/c.jpg          ignored + too_deep warning
旅遊/root.jpg                   ignored + category_file warning
root.jpg                        ignored + root_file warning
.hidden/album/a.jpg             ignored
旅遊/.hidden/a.jpg              ignored
旅遊/日本/note.txt              ignored + unsupported warning
```

另以 `os.Symlink` 建立 file 與 directory links；平台不允許 symlink 時 skip 該 assertion。Expected entries 必須依 relative path lexicographic order，讓 scan deterministic。

Run：`make test`  
Expected：FAIL，因 `scanner.Walk` 尚未存在。

- [ ] **Step 2：實作 scanner**

只使用 `os.ReadDir`，不要用遞迴 `filepath.WalkDir`：

```go
func Walk(root string, warn func(Warning)) ([]Entry, error) {
	categories, err := os.ReadDir(root)
	if err != nil { return nil, err }
	var out []Entry
	for _, category := range categories {
		if ignore(category) { continue }
		if category.Type()&os.ModeSymlink != 0 || !category.IsDir() { warn(...); continue }
		// Read category once, then each album once; never recurse below album.
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.RelativePath, b.RelativePath) })
	return out, nil
}
```

支援 extension 必須以 lowercase exact set 判斷：`.jpg`、`.jpeg`、`.png`、`.webp`。`RelativePath` 使用 `filepath.ToSlash`；絕不呼叫 `EvalSymlinks` 後跟入 link。

- [ ] **Step 3：加入 root/unreadable failure test**

不存在 root 必須回 error。Linux container 中建立 permission `000` directory；因 test container 可能以 root 執行，測試改用 injected `readDir func(string) ([]os.DirEntry,error)` 模擬 album `permission denied`，並驗證整個 Walk 回 error，而非默默略過。

- [ ] **Step 4：驗證**

Run：`make test`  
Expected：scanner tests PASS，沒有讀取第三層檔案。

- [ ] **Step 5：Commit**

```bash
git add internal/scanner
git commit -m "feat: scan fixed photo hierarchy"
```

---

### Task 4：讀取 metadata 並原子產生 thumbnail

**Files：**

- Create: `internal/media/metadata.go`
- Create: `internal/media/metadata_test.go`
- Create: `internal/media/thumbnail.go`
- Create: `internal/media/thumbnail_test.go`

**Interfaces：**

- Produces: `media.ReadMetadata(path string) (media.Metadata, error)`
- Produces: `(*media.Thumbnailer).Generate(ctx context.Context, source, key string) error`
- Consumes: filesystem path and `vipsthumbnail`

```go
type Metadata struct {
	MIMEType string
	Width, Height int
	TakenAt sql.NullString
}

type Thumbnailer struct {
	Dir string
	Command string // production default: vipsthumbnail
}
```

- [ ] **Step 1：寫 metadata tests**

測試涵蓋：

- 一張 programmatically encoded JPEG：正確 width/height、MIME `image/jpeg`、`TakenAt.Valid=false`；
- PNG 與 WebP header：正確 MIME/dimensions，無 EXIF 時 `NULL`；
- invalid bytes：回 decode error；
- EXIF date parser：`"2017:06:27 14:03:02"` → `"2017-06-27T14:03:02"`；空值、無效日期 → `NULL`。

把 date parsing 拆成 unexported `parseEXIFDate(string) sql.NullString`，直接 unit test；`ReadMetadata` 用 `goexif.Decode` 讀 `DateTimeOriginal`，`io.EOF`/missing tag 僅代表 NULL，不是 image failure。

Run：`make test`  
Expected：FAIL。

- [ ] **Step 2：實作 metadata reader**

先用 `image.DecodeConfig` 取得 format/dimensions，再將檔案 seek 回開頭讀 EXIF。MIME mapping 固定：jpeg → `image/jpeg`、png → `image/png`、webp → `image/webp`；其他 format 回 unsupported error。

日期解析：

```go
func parseEXIFDate(v string) sql.NullString {
	t, err := time.Parse("2006:01:02 15:04:05", strings.TrimSpace(v))
	if err != nil { return sql.NullString{} }
	return sql.NullString{String: t.Format("2006-01-02T15:04:05"), Valid: true}
}
```

- [ ] **Step 3：寫 thumbnail failing tests**

使用 fake executable shell script 記錄 argv 並產生 output，驗證：

- output 先落在 ThumbnailDir temporary file；
- 最後以 atomic rename 成 `{key}.webp`；
- argv 含 size 512、down-only、quality 80、strip metadata 與 auto-rotate 行為；
- command failure 不留下 final/temporary file；
- key 含 `/` 或 `..` 時拒絕。

Production command contract 固定為：

```text
vipsthumbnail <source> --size 512x512 --no-rotate=false --output <temp>[Q=80,strip]
```

實作前在 Docker test stage 執行 `vipsthumbnail --help` 核對該版本 option；若 `--no-rotate=false` 不被接受，省略 `--no-rotate` 即代表依 orientation 自動旋轉。最終 acceptance test 必須以直式 EXIF sample 驗證，不可只相信參數名稱。

- [ ] **Step 4：實作 Thumbnailer**

使用 `os.CreateTemp(Dir, ".thumb-*.webp")` 保留 temp path，關閉並移除空檔後交給 `vipsthumbnail` 寫入；command 成功後 `os.Rename(temp, final)`。永遠以 `exec.CommandContext` 的 argv 傳參數，不透過 shell。

- [ ] **Step 5：以真實 libvips 驗證**

Run：

```bash
docker build --target test -t photo-browser-test .
docker run --rm photo-browser-test vipsthumbnail --version
```

Expected：tests PASS；version command exit 0。

- [ ] **Step 6：Commit**

```bash
git add internal/media go.mod go.sum Dockerfile
git commit -m "feat: extract metadata and thumbnails"
```

---

### Task 5：實作 NEW、CHANGED、UNCHANGED indexing

**Files：**

- Create: `internal/indexer/indexer.go`
- Create: `internal/indexer/indexer_test.go`
- Modify: `internal/catalog/store.go`

**Interfaces：**

- Produces: `(*indexer.Indexer).Run(ctx context.Context, options indexer.Options) (catalog.ScanCounts, error)`
- Consumes: concrete Store、scanner function、metadata reader、Thumbnailer

```go
type Options struct { RebuildThumbnails bool }

type Indexer struct {
	Store *catalog.Store
	PhotoRoot string
	Thumbnailer *media.Thumbnailer
	Walk func(string, func(scanner.Warning)) ([]scanner.Entry, error)
	ReadMetadata func(string) (media.Metadata, error)
	BatchSize int // production: 200
}
```

- [ ] **Step 1：寫三種 fingerprint path tests**

以 temporary SQLite 加 fake Walk/ReadMetadata/Thumbnailer 驗證：

- NEW 呼叫 metadata 與 thumbnail 各一次，insert row，counts.New=1；
- CHANGED（size 或 mtime 任一不同）各呼叫一次，更新 fingerprint/key，counts.Changed=1；
- UNCHANGED 不呼叫 metadata/thumbnail，只更新 `last_seen_scan_id`，counts.Unchanged=1；
- `RebuildThumbnails=true` 對 unchanged photo 只重建 thumbnail，不重新讀 EXIF，identity 不變。

Thumbnail key helper：

```go
func thumbnailKey(photoID, mtimeNS, size int64) string {
	return fmt.Sprintf("%d-%d-%d", photoID, mtimeNS, size)
}
```

Run：`make test`  
Expected：FAIL。

- [ ] **Step 2：實作 batch orchestration**

每 200 entries 開一個 transaction。Category/Album 以 relative path upsert 並更新 `last_seen_scan_id`。NEW Photo 先 insert 取得 ID，再生成 thumbnail；thumbnail failure 記 warning、保留 nullable key，不讓該 Photo 從 catalogue 消失。

CHANGED thumbnail 必須先成功產生新 key，再更新 DB；舊 thumbnail 留待成功 reconciliation 清除，避免 DB 指向不存在的 final file。

- [ ] **Step 3：加入 partial decode behavior test**

Image decode/thumbnail error 必須增加 warning/error counter，但已 stat 的 Photo row仍存在。Scanner/walk error 則整次 `Run` 回 error，後續 Task 6 不得 reconcile。

- [ ] **Step 4：驗證 batch boundary**

以 401 fake entries、`BatchSize=200`，在 Store 加 test-only transaction hook，驗證 commit 次數為 3，不是 1 或 401。

- [ ] **Step 5：Run full tests**

Run：`make test`  
Expected：indexer tests PASS。

- [ ] **Step 6：Commit**

```bash
git add internal/indexer internal/catalog
git commit -m "feat: incrementally index photos"
```

---

### Task 6：安全 reconciliation 與 orphan thumbnail cleanup

**Files：**

- Create: `internal/indexer/reconcile.go`
- Create: `internal/indexer/reconcile_test.go`
- Modify: `internal/indexer/indexer.go`
- Modify: `internal/catalog/store.go`

**Interfaces：**

- Produces: `catalog.Store.ListUnseenPhotos(scanID int64) ([]catalog.Photo, error)`
- Produces: `catalog.Store.DeleteUnseen(scanID int64) (removed int64, obsoleteKeys []string, err error)`
- Produces: `indexer.RemoveThumbnail(dir, key string) error`

- [ ] **Step 1：寫 success-only deletion tests**

建立兩張 Photo，只讓一張 `last_seen_scan_id=current`：

- complete walk → unseen row、其 thumbnail、empty Album/Category 被刪除；
- Walk 回 error → unseen row 與 thumbnail 都保留；
- DB deletion failure → thumbnail 不刪，因 DB 仍可能指向它；
- thumbnail `os.Remove` 回 not-exist → 視為成功；其他錯誤計 warning。

Run：`make test`  
Expected：FAIL。

- [ ] **Step 2：實作單一 reconciliation transaction**

順序固定：

1. Query unseen photo keys。
2. 在一個短 transaction 中 delete unseen Photos。
3. Delete empty Albums。
4. Delete empty Categories。
5. Commit。
6. Commit 成功後才逐一刪除 obsolete thumbnail files。
7. 掃描 ThumbnailDir，刪除不在 DB current key set 的 orphan `.webp`；忽略其他檔案。

任何 client path 都不得進入 `RemoveThumbnail`；只接受 DB key，並以 `filepath.Base(key)==key` 驗證。

- [ ] **Step 3：確保 ScanRun 最終狀態**

`Indexer.Run` 必須以 defer 保證 started scan 最終成為 `succeeded` 或 `failed`：

```go
scanID, err := store.StartScan(now)
if err != nil { return counts, err }
succeeded := false
defer func() {
	status := "failed"
	if succeeded { status = "succeeded" }
	_ = store.FinishScan(scanID, status, counts, runErr)
}()
```

實作時使用 named return `runErr error`，確保 failure summary 接到真實 error；不要在 defer 裡遮蔽 error。

- [ ] **Step 4：驗證 interrupted semantics**

測試先留下一筆 `running` scan，再 StartScan；舊 row 必須變成 failed/abandoned，新 scan 可正常開始。Failed scan 的既有 catalogue rows 不變。

- [ ] **Step 5：Run full tests**

Run：`make test`  
Expected：所有 tests PASS。

- [ ] **Step 6：Commit**

```bash
git add internal/indexer internal/catalog
git commit -m "feat: reconcile missing photos safely"
```

---

### Task 7：加入 single-run lock 與 CLI commands

**Files：**

- Create: `internal/filelock/filelock_unix.go`
- Create: `internal/filelock/filelock_test.go`
- Create: `internal/app/run.go`
- Create: `internal/app/run_test.go`
- Modify: `cmd/photo-app/main.go`

**Interfaces：**

- Produces: `filelock.Acquire(path string) (*filelock.Lock, error)`
- Produces sentinel: `filelock.ErrAlreadyLocked`
- Produces: `app.Run(ctx context.Context, args []string, stdout, stderr io.Writer) int`
- Consumes: config、database、catalog、indexer

- [ ] **Step 1：寫 lock failing test**

同 process 對同 path 連續 Acquire：第一次成功，第二次回 `ErrAlreadyLocked`；Release 後第三次成功。Lock file 本身可以保留，ownership 由 advisory lock 決定。

Run：`make test`  
Expected：FAIL。

- [ ] **Step 2：實作 Unix advisory lock**

使用 `unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)`；busy error mapping 到 `ErrAlreadyLocked`。`Release` 先 unlock 再 close。不要以 PID file 判斷 stale process。

- [ ] **Step 3：寫 CLI failing tests**

以 injected `runIndex func(context.Context, indexer.Options) error` 驗證：

| Args | Options/Result | Exit code |
|---|---|---:|
| `index` | normal | 0 |
| `index --rebuild-thumbnails` | `RebuildThumbnails=true` | 0 |
| `rebuild` | reset derived catalogue + full index | 0 |
| unknown/extra arg | usage error | 2 |
| lock busy | concise already-running message | 3 |
| runtime failure | error on stderr | 1 |

Run：`make test`  
Expected：FAIL。

- [ ] **Step 4：實作 CLI wiring**

`app.Run` 流程：load config → ensure data/thumbnail dirs → acquire `${DATA_DIR}/index.lock` → open `${DATA_DIR}/photo.db` → create Store/Thumbnailer/Indexer → dispatch command。

`rebuild` 只清空 `categories`、`albums`、`photos`、`scan_runs` 與 thumbnails；未來 Spec 2 的 `users` table 不在 deletion list。禁止以刪除整個 DB file 實作 rebuild。

`cmd/photo-app/main.go`：

```go
func main() {
	os.Exit(app.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 5：驗證 production build**

Run：

```bash
make test
make build
docker run --rm photo-browser:local unknown
```

Expected：tests/build PASS；unknown command exit code 2 並只顯示 usage。

- [ ] **Step 6：Commit**

```bash
git add cmd internal/app internal/filelock
git commit -m "feat: expose safe indexer commands"
```

---

### Task 8：端到端驗收與可重建證據

**Files：**

- Create: `scripts/acceptance.sh`
- Modify: `Makefile`
- Modify: `Dockerfile`

**Interfaces：**

- Produces: `make acceptance`
- Consumes: built `photo-browser:local` image and read-only `test-photos/`

- [ ] **Step 1：寫會失敗的 acceptance script**

Script 使用 `mktemp -d` 建立 disposable root，並以 cleanup trap 回收。它必須：

1. 建立 `photos/旅遊/20170627阿里山二日遊`。
2. Copy `test-photos/..._1.jpg`、`..._2.jpg`、`..._3.jpg`，不修改 originals。
3. 以 `exiftool -overwrite_original -DateTimeOriginal='2017:06:27 14:03:02'` 修改 temporary copy `_1.jpg`。
4. Run `photo-app index`。
5. 以一個小型 `photo-app inspect` 不在 Spec 內，故不得新增；改用 container 內 `sqlite3` CLI 查 count。Test stage 安裝 `sqlite3`，runtime 不安裝。
6. 驗證 1 Category、1 Album、3 Photos、3 thumbnails，且一筆 `taken_at='2017-06-27T14:03:02'`。
7. 第二次 index，驗證 latest scan `files_unchanged=3`。
8. 修改 temporary `_2.jpg` mtime，驗證 changed=1。
9. 刪除 temporary `_3.jpg`，驗證 removed=1、Photo count=2。
10. 暫時讓 root path 不存在並執行 index，驗證 exit non-zero 且 Photo count 仍為 2。
11. 執行 `rebuild`，驗證 catalogue 與 thumbnails 回到 2。

Run：`make acceptance`  
Expected：FAIL，直到 Makefile/Docker test runner wiring 完成。

- [ ] **Step 2：加入 acceptance runner**

Makefile：

```make
.PHONY: test build acceptance
acceptance: build
	bash scripts/acceptance.sh
```

Acceptance 使用 bind-mounted temporary directories：

- `photo-browser-test` image 內的 `exiftool` 準備 temporary fixture，並以 `sqlite3` 查詢 DB；
- `photo-browser:local` runtime image 只執行 `photo-app`；
- originals fixture mount 必須 `:ro`；data/thumbnails temp directories 必須預先授權 UID 65532 寫入；
- 每次 run 使用唯一 container name，不依賴 daemon 中既有 container。

- [ ] **Step 3：加入 orientation 與 no-upscale 驗證**

在 temporary copy 寫入 EXIF Orientation=6，產生 thumbnail 後用 `vipsheader -f width -f height` 驗證 portrait orientation。另建立 100×80 PNG，驗證輸出不超過 100×80。

- [ ] **Step 4：執行完整 verification**

Run：

```bash
make test
make acceptance
make build
docker image inspect photo-browser:local --format '{{.Architecture}} {{.Config.User}}'
```

Expected：

```text
all Go tests pass
acceptance script exits 0
runtime image builds
amd64 65532:65532
```

- [ ] **Step 5：記錄 target NAS 尚待 Spec 4 驗證的項目**

在 acceptance output 印出而非新增架構文件：scan duration、Photo count、thumbnail bytes、peak RSS 尚未於 DS716+II 量測。這些不是 Spec 1 local completion blocker，但 Spec 4 Go/No-Go 必須完成。

- [ ] **Step 6：Commit**

```bash
git add scripts/acceptance.sh Makefile Dockerfile
git commit -m "test: verify indexer end to end"
```

---

## 最終驗證矩陣

| Spec 1 驗收要求 | 實作 Task | 證據 |
|---|---:|---|
| 固定兩層 schema | 3 | Scanner unit tests |
| JPEG/PNG/WebP | 3、4 | Scanner + metadata tests |
| SQLite WAL/schema | 2 | Database tests |
| EXIF DateTimeOriginal | 4、8 | Parser + acceptance fixture |
| 512 px WebP、quality 80、不放大 | 4、8 | Thumbnail unit/integration checks |
| NEW/CHANGED/UNCHANGED | 5、8 | Indexer tests + scan counters |
| MISSING | 6、8 | Reconciliation tests |
| 失敗時不刪 metadata | 6、8 | Failed-walk test |
| Single process lock | 7 | File-lock test |
| Scan history/counters | 2、5、6 | Store/indexer tests |
| Full rebuild | 7、8 | Acceptance rebuild check |
| Original 永不修改 | 全域、8 | Read-only mount + checksum-safe copy |

## 執行順序

依序完成 Task 1 → 8。Task 4 與 Task 3 在 Task 2 完成後技術上可平行，但本 workspace 目前不是 Git repository，且計劃要求每個 checkpoint 易於審查，因此預設循序執行。

## 明確延後

- HTTP API、Firebase、Nginx media serving：Spec 2。
- NAS Compose、Cloudflare、resource measurement：Spec 4。
- HEIC/RAW/video、多尺寸 image、hash deduplication、watcher：沒有實際需求前不建立。
