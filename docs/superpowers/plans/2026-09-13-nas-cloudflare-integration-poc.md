# Spec 4 NAS + Cloudflare Integration POC — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Spec 1–3 的成果部署成一條真正可用的 vertical slice——Internet 上的 allowlisted 家人透過 Cloudflare Pages 前端登入，經 Cloudflare Tunnel 抵達 DS716+II 上的 nginx + photo-app，安全瀏覽 NAS originals；同時 DSM／SMB／filesystem path／backend port 全部不對外暴露，並具備可演練的備份復原與可量測的資源足跡。

**Architecture:** 兩軌並行。**Track A（repo，可在本機驗證）**：production 映像與 `photo-app healthcheck` / `photo-app backup` 兩個新子命令、production nginx／compose／cloudflared 設定、備份與復原演練腳本、資源量測腳本、部署驗證腳本，以及一份「prod-like」本機 compose overlay——它跑的是 **production 映像與 production nginx.conf**，只把 Firebase 換成 testauth，因此所有驗證腳本在沒有 NAS、沒有 Cloudflare 帳號的情況下也能真的跑起來。**Track B（runbook，人工執行）**：註冊網域、建立 Cloudflare zone／Tunnel／Pages、建立 Firebase project、在 DSM 上建 share 與 Task Scheduler、跑首次索引與驗收。Track B 寫成 repo 內的逐步文件，每一步都附可貼上的指令與「怎麼知道成功了」的驗證方式。

**Tech Stack:** Go 1.25（既有 backend，新增兩個子命令）、Docker Compose v2、nginx（`nginxinc/nginx-unprivileged`）、`cloudflare/cloudflared`、SQLite（`modernc.org/sqlite`，`VACUUM INTO` 做 online backup）、Cloudflare Pages / Tunnel、Firebase Authentication、Synology DSM 7.1 Task Scheduler、bash。

**Spec:** `docs/superpowers/specs/2026-08-30-04-nas-cloudflare-integration-poc.md`

## Global Constraints

以下數值逐字取自 spec，每個 task 都隱含適用：

- 目標機器 **Synology DS716+II**、DSM **7.1.1-42962 Update 9**、**Intel Celeron N3160（Braswell）** 4 core / 4 thread、**2,048 MB DDR3**、8 TB volume。容量估算一律以 **2 GB RAM** 為準，不可用 CPU 規格上限的 8 GB。
- 所有 Go binary 與 native dependency 必須以保守的 `linux/amd64` 建置，**不得**使用 `GOAMD64=v3/v4`（既有 Dockerfile 已固定 `GOAMD64=v1`，不得放寬）。
- **Thumbnail concurrency 固定為 1**，完成 NAS 實測前不得提高。（現行 indexer 是單執行緒循序處理，本 plan 不得引入平行化。）
- API **只啟動單一 process**，不使用多 worker configuration。
- POC codec **僅** JPEG、PNG、WebP。**HEIC 不得默默進入 scope**；codec probe 失敗即維持 unsupported。
- 初始 full scan 與 thumbnail rebuild 在離峰執行；**periodic scan 每 60 分鐘**。
- Thumbnail cache 不預留固定容量；部署前記錄 volume free space，低空間時停止產生 thumbnail，**original 不受影響**（Task 3 的 `THUMBNAIL_MIN_FREE_BYTES`，預設 2 GiB）。
- Volume 契約：`NAS photos share → /srv/photos:ro`、`app-data → /srv/data:rw`、`thumbnails → /srv/thumbnails:rw`（僅 indexer 寫）。API **永不修改 originals**。
- Container 以 **non-root** 執行並 drop unnecessary capabilities。Application、DB 與 internal nginx port **不得** publish 到 Internet-facing host interface。**禁止**為本應用設定 router port forwarding。
- Cloudflare：**僅一個** public hostname route 到 internal nginx photo service；**不得**有 ingress rule 指向 DSM、SMB、SSH、Container Manager 或 catch-all NAS origin。Tunnel credential 以 secret/restricted file mount，**排除於 source control 與 image layer**。
- `/api/` 與 protected media **不得**套用 Cloudflare shared cache rule。POC **不使用** Cloudflare Access。
- Firebase：authorized domain **只**加入 production Pages hostname；**只啟用一個** provider（本 POC 為 Google，因 Spec 3 前端已實作 `signInWithPopup(GoogleAuthProvider)`）；第一位 admin 透過 local bootstrap CLI 建立；另建一個 member 與一個**刻意不在 allowlist** 的 test identity。**NAS 必須透過 NTP 同步時間**。
- Log **不得**記錄 bearer token、Tunnel credential、Firebase service credential；public response **不得**包含 SQL error 或 absolute filesystem path。Container log 必須 bounded 並 rotation。
- Thumbnails 是 derived data，**不需備份**。SQLite **每日**以 online backup 備份，**不得**直接複製 live `.db`。
- Recovery targets：allowlist **RPO 24 小時**、service **RTO 數小時**、original photo RPO 沿用既有 NAS backup policy。
- Commit message 用 conventional style（`feat:` / `fix:` / `test:` / `docs:` / `chore:`），每個 task 至少一個 commit。

## 佔位主機名稱

網域尚未註冊，因此全案以兩個變數表示，所有設定檔透過 `.env` 注入，**不得**在檔案裡寫死：

| 變數 | 意義 | 範例 |
|---|---|---|
| `PUBLIC_APP_HOST` | Cloudflare Pages 前端 | `photos.example.com` |
| `PUBLIC_API_HOST` | Tunnel 對外的 API/media | `photos-api.example.com` |

Runbook（Task 14）會在網域確定後，指示把真實值填進 `deploy/compose/.env.prod`（gitignored）。

## File Structure

**Track A — repo（本 plan 產出的程式與設定）**

| 檔案 | 責任 |
|---|---|
| `internal/app/healthcheck.go` + `_test.go` | `photo-app healthcheck` 子命令：對自己的 `/api/v1/health/ready` 發請求，exit 0/1。讓 runtime image 不必安裝 curl 就能做 container healthcheck。 |
| `internal/app/backup.go` + `_test.go` | `photo-app backup --out <path>` 子命令：用 SQLite `VACUUM INTO` 做 online backup，不碰 live `.db` 檔案。 |
| `internal/app/run.go` | 新增兩個子命令的 dispatch 與 usage 字串。 |
| `internal/indexer/diskspace.go` + `_test.go` | `FreeBytes` via `statfs`；indexer 在 thumbnail volume 低空間時停止產生縮圖，originals 與 catalogue 不受影響。 |
| `internal/scanner/scanner.go` | 新增 `CodeLowDiskSpace` warning code。 |
| `internal/config/config.go` + `_test.go` | 新增 `THUMBNAIL_MIN_FREE_BYTES`（預設 2 GiB，0 停用）。 |
| `internal/httpapi/errors.go` + `_test.go` | `WriteJSON` / `WriteError` 加 `Cache-Control: no-store`，確保 per-user JSON 永不進共用快取。 |
| `deploy/nginx/nginx.prod.conf` | Production nginx：unprivileged、Host 白名單、bounded log、internal media location、media 的 `private` cache header。 |
| `deploy/compose/docker-compose.prod.yml` | 三個 service（nginx / photo-app / cloudflared），non-root、cap_drop、read-only rootfs、mem/cpu limit、log rotation、無 host port。 |
| `deploy/compose/docker-compose.prodcheck.yml` | Prod overlay 的本機版：同樣的映像與 nginx.prod.conf，但把 cloudflared 換成一個 host port、Firebase 換成 testauth，讓驗證腳本可在筆電上真的跑。 |
| `deploy/compose/.env.prod.example` | Production 環境變數樣板（hostname、版本、路徑、limits）。 |
| `deploy/cloudflared/config.yml` | Tunnel ingress：單一 hostname → `http://nginx:8080`，catch-all `http_status:404`。 |
| `scripts/nas-preflight.sh` | Spec §3 的前置檢查：DSM/Docker/Compose 版本、libvips codec、HEIC probe、volume 剩餘空間、UID/GID、CPU flags，輸出報告。 |
| `scripts/backup-sqlite.sh` | 呼叫 `photo-app backup`，加上 config 備份、retention、完整性驗證。 |
| `scripts/restore-drill.sh` | 在本機 docker 完整演練 spec §9 的 7 個 restore 步驟。 |
| `scripts/measure-resources.sh` | 取樣 idle 與 scan 期間的 CPU / RSS，輸出報告。 |
| `scripts/verify-deployment.sh` | Spec §10 的 functional + security checks，可對 prodcheck 或真實 hostname 執行。 |
| `scripts/nas-index.sh` | DSM Task Scheduler 用的 one-shot 索引腳本（含 409/lock 處理與 log）。 |
| `web/.env.production.example` | Pages build 用的 `VITE_*` 樣板。 |
| `Makefile` | 新 target：`build-prod`、`prodcheck-up/down`、`verify-deployment`、`restore-drill`、`measure`。 |

**Track B — runbook（人工執行的文件）**

| 檔案 | 責任 |
|---|---|
| `docs/deploy/01-external-services.md` | 註冊網域 → Cloudflare zone → Tunnel → Pages → Firebase project。 |
| `docs/deploy/02-nas-deployment.md` | DSM share/權限/NTP → secrets → compose up → 首次索引 → bootstrap admin → Task Scheduler。 |
| `docs/deploy/03-acceptance.md` | Spec §11 驗收清單與簽收表，含 troubleshooting 與資源實測記錄欄位。 |
| `docs/deploy/preflight-report.md` | Task 1 腳本產生的實測報告（先放空樣板，實跑後填）。 |

---

## Task 1: NAS preflight probe

Spec §3 要求「build image 前必須記錄」一整串環境事實，且 HEIC 必須明確 probe 而非默默略過。這個 task 產出一支可在 NAS 上直接跑的腳本，把這些事實一次抓齊寫成報告。

**Files:**
- Create: `scripts/nas-preflight.sh`
- Create: `docs/deploy/preflight-report.md`（空樣板，實跑後覆寫）
- Modify: `Makefile`（加 `preflight` target）

**Interfaces:**
- Consumes: 既有 `photo-browser:local` runtime image（`make build`）與 `photo-browser-test` image（含 libvips-tools）。
- Produces: `scripts/nas-preflight.sh [--photo-root PATH] [--data-dir PATH] [--thumb-dir PATH] [--out FILE]`，退出碼 0=全部通過、1=有 blocking 問題（例如 volume 空間不足）。報告為 markdown。

- [ ] **Step 1: 寫腳本**

```bash
cat > scripts/nas-preflight.sh <<'SH'
#!/usr/bin/env bash
# Records everything spec 4 section 3 requires before any image is built.
#
# Safe to run repeatedly; it only reads. Run it ON THE NAS over SSH — the
# numbers that matter (CPU flags, free space, libvips build) are the NAS's,
# not your laptop's.
set -uo pipefail

PHOTO_ROOT="/volume1/photos"
DATA_DIR="/volume1/docker/photo-browser/data"
THUMB_DIR="/volume1/docker/photo-browser/thumbnails"
OUT="preflight-report.md"
IMAGE="${PREFLIGHT_IMAGE:-photo-browser-test:latest}"

while [ $# -gt 0 ]; do
  case "$1" in
    --photo-root) PHOTO_ROOT="$2"; shift 2 ;;
    --data-dir)   DATA_DIR="$2";   shift 2 ;;
    --thumb-dir)  THUMB_DIR="$2";  shift 2 ;;
    --out)        OUT="$2";        shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

blocking=0
say() { printf '%s\n' "$*" >> "$OUT"; }
note_block() { blocking=1; say "- **BLOCKING:** $*"; }

: > "$OUT"
say "# NAS preflight report"
say ""
say "Generated: $(date -Iseconds) on \`$(hostname)\`"
say ""

say "## Platform"
say ""
say '```'
say "uname: $(uname -a)"
say "DSM:   $(cat /etc/VERSION 2>/dev/null | tr '\n' ' ' || echo 'not a DSM host')"
say "CPU:   $(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ //')"
say "cores: $(nproc 2>/dev/null || echo '?')"
say "mem:   $(free -m 2>/dev/null | awk '/^Mem:/{print $2" MB total, "$7" MB available"}')"
say '```'
say ""

# Braswell has no AVX2. Recording the flags proves why GOAMD64 stays at v1.
if grep -qw avx2 /proc/cpuinfo 2>/dev/null; then
  say "- CPU reports avx2 (unexpected for Braswell) — keep \`GOAMD64=v1\` anyway."
else
  say "- CPU has no avx2, as expected for Braswell. \`GOAMD64=v1\` is required."
fi
say ""

say "## Container runtime"
say ""
say '```'
say "docker:  $(docker --version 2>&1 | head -1)"
say "compose: $(docker compose version 2>&1 | head -1)"
say '```'
say ""
if ! docker compose version >/dev/null 2>&1; then
  note_block "\`docker compose\` (v2) is unavailable; the prod topology needs it."
fi
say ""

say "## Volumes"
say ""
say "| path | exists | writable | free |"
say "|---|---|---|---|"
for spec_line in "photos:$PHOTO_ROOT:ro" "data:$DATA_DIR:rw" "thumbnails:$THUMB_DIR:rw"; do
  name="${spec_line%%:*}"; rest="${spec_line#*:}"
  path="${rest%%:*}"; mode="${rest##*:}"
  exists=no; writable=n/a; free="?"
  [ -d "$path" ] && exists=yes
  if [ "$exists" = yes ]; then
    free="$(df -h "$path" 2>/dev/null | awk 'NR==2{print $4}')"
    if [ "$mode" = rw ]; then
      if [ -w "$path" ]; then writable=yes; else writable=no; fi
    fi
  fi
  say "| \`$path\` ($name, $mode) | $exists | $writable | $free |"
  if [ "$exists" = no ]; then note_block "$path does not exist."; fi
  if [ "$mode" = rw ] && [ "$writable" = no ]; then note_block "$path is not writable."; fi
done
say ""

# Thumbnails plus SQLite must not fill the volume. 5 GB is a deliberately low
# floor: a 22k-photo library of 512px WebP thumbnails lands near 1 GB.
free_kb="$(df -Pk "$THUMB_DIR" 2>/dev/null | awk 'NR==2{print $4}')"
if [ -n "${free_kb:-}" ] && [ "$free_kb" -lt 5242880 ]; then
  note_block "thumbnail volume has less than 5 GiB free (${free_kb} KiB)."
fi

say "## Service identity"
say ""
say '```'
say "id: $(id)"
say '```'
say "Record the UID/GID that has read-only access to the photo share; the compose file pins it."
say ""

say "## libvips codecs"
say ""
say "Probed inside \`$IMAGE\` (same libvips build the app uses)."
say ""
say '```'
docker run --rm --entrypoint sh "$IMAGE" -c '
  vips --version
  for f in jpegload pngload webpload webpsave; do
    if vips -l 2>/dev/null | grep -q "^ *$f"; then echo "$f: yes"; else echo "$f: NO"; fi
  done
' 2>&1 | while IFS= read -r line; do say "$line"; done
say '```'
say ""

say "## HEIC probe (must stay unsupported unless this passes)"
say ""
say '```'
heic_out="$(docker run --rm --entrypoint sh "$IMAGE" -c 'vips -l 2>/dev/null | grep -c heifload' 2>&1)"
say "heifload operations found: $heic_out"
say '```'
if [ "$heic_out" = "0" ]; then
  say "- HEIC is **unsupported**, as specified. Do not add HEIC files to the library."
else
  say "- libvips exposes heifload, but HEIC stays **out of POC scope** until a separate"
  say "  memory probe on this 2 GB machine proves a full-size HEIC decode fits."
fi
say ""

say "## EXIF orientation"
say ""
say '```'
docker run --rm --entrypoint sh "$IMAGE" -c 'exiftool -ver' 2>&1 | while IFS= read -r line; do say "exiftool: $line"; done
say '```'
say ""

say "## Time sync (Firebase token verification depends on it)"
say ""
say '```'
say "date: $(date -Iseconds)"
say "ntp:  $(timedatectl 2>/dev/null | tr '\n' ' ' || synotime --get 2>/dev/null || echo 'check DSM > Control Panel > Regional Options > Time')"
say '```'
say ""

say "## Photo count"
say ""
say "Not collected here. The first read-only \`photo-app index\` records it in"
say "\`scan_runs.files_seen\`; copy that number into this report afterwards."
say ""

if [ "$blocking" -ne 0 ]; then
  echo "preflight FAILED — see $OUT" >&2
  exit 1
fi
echo "preflight OK — wrote $OUT"
SH
chmod +x scripts/nas-preflight.sh
```

- [ ] **Step 2: 在本機驗證腳本能跑完並產出報告**

本機不是 DSM，`/etc/VERSION` 與 photo share 都不存在，所以用 repo 內的暫存路徑跑：

```bash
make build >/dev/null
mkdir -p /tmp/pf/{photos,data,thumbs}
bash scripts/nas-preflight.sh \
  --photo-root /tmp/pf/photos --data-dir /tmp/pf/data --thumb-dir /tmp/pf/thumbs \
  --out /tmp/pf/report.md
echo "exit=$?"
cat /tmp/pf/report.md
```

期望：exit 0；報告含 Platform / Container runtime / Volumes / libvips codecs / HEIC probe 五個段落；HEIC 段落明確寫出 unsupported 或「需另做 memory probe」。

- [ ] **Step 3: 驗證 blocking 路徑真的會擋**

```bash
bash scripts/nas-preflight.sh \
  --photo-root /tmp/pf/photos --data-dir /nonexistent-dir --thumb-dir /tmp/pf/thumbs \
  --out /tmp/pf/bad.md
echo "exit=$? (expect 1)"
grep BLOCKING /tmp/pf/bad.md
```

期望：exit 1，且報告列出 `/nonexistent-dir does not exist.`。這一步是必要的——只驗證成功路徑的檢查腳本等於沒有檢查。

- [ ] **Step 4: 建報告樣板**

```bash
mkdir -p docs/deploy
cat > docs/deploy/preflight-report.md <<'MD'
# NAS preflight report

> 尚未在目標 NAS 上執行。

執行方式（在 NAS 上，經 SSH）：

```
git clone <repo> && cd photo-browser
make test                     # 先確認 image 能在 NAS 上 build
bash scripts/nas-preflight.sh \
  --photo-root /volume1/photos \
  --data-dir   /volume1/docker/photo-browser/data \
  --thumb-dir  /volume1/docker/photo-browser/thumbnails \
  --out docs/deploy/preflight-report.md
```

跑完後把產生的內容 commit 進來，並在 `docs/deploy/03-acceptance.md` 勾掉對應項目。
若腳本以 exit 1 結束，**先解決 BLOCKING 項目再往下做**——後面每個 task 都假設這些前提成立。
MD
```

- [ ] **Step 5: 加 Makefile target**

```makefile
preflight:
	bash scripts/nas-preflight.sh --out docs/deploy/preflight-report.md
```

- [ ] **Step 6: Commit**

```bash
git add scripts/nas-preflight.sh docs/deploy/preflight-report.md Makefile
git commit -m "feat: nas preflight probe recording spec 4 prerequisites"
```

---

## Task 2: `photo-app healthcheck` 與 `photo-app backup` 子命令

Production runtime image 是 `debian:bookworm-slim`，**沒有 curl、wget 或 sqlite3**。Container healthcheck 與 SQLite online backup 都需要工具，而在映像裡裝 curl/sqlite3 會擴大攻擊面。兩個小子命令解決這件事，而且都可以用 Go test 覆蓋。

**Files:**
- Create: `internal/app/healthcheck.go`
- Create: `internal/app/healthcheck_test.go`
- Create: `internal/app/backup.go`
- Create: `internal/app/backup_test.go`
- Modify: `internal/app/run.go`（usage 字串 + dispatch）
- Modify: `internal/app/run_test.go`（dispatch 測試）

**Interfaces:**
- Consumes: `config.Config.HTTPListen`、`config.Config.DataDir`；`database.Open`。
- Produces:
  - `func RunHealthcheck(ctx context.Context, url string, client *http.Client, stderr io.Writer) int` — 200 → 0；其他 → 1。
  - `func RunBackup(ctx context.Context, db *sql.DB, outPath string, stdout io.Writer) error` — 用 `VACUUM INTO`。
  - CLI：`photo-app healthcheck [--url URL]`（預設 `http://127.0.0.1<HTTP_LISTEN>/api/v1/health/ready`）、`photo-app backup --out PATH`。
  - `Commands` struct 新增欄位 `Healthcheck func(ctx context.Context, args []string, stdout, stderr io.Writer) int` 與 `Backup func(ctx context.Context, args []string, stdout, stderr io.Writer) int`。

- [ ] **Step 1: 先寫 healthcheck 的失敗測試**

```go
// internal/app/healthcheck_test.go
package app_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"photo-browser/internal/app"
)

func TestHealthcheckOKWhenReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health/ready" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	var stderr bytes.Buffer
	code := app.RunHealthcheck(context.Background(), srv.URL+"/api/v1/health/ready", srv.Client(), &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestHealthcheckFailsOnNotReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	var stderr bytes.Buffer
	if code := app.RunHealthcheck(context.Background(), srv.URL+"/api/v1/health/ready", srv.Client(), &stderr); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a diagnostic on stderr")
	}
}

func TestHealthcheckFailsWhenUnreachable(t *testing.T) {
	var stderr bytes.Buffer
	// Port 1 is never listening; this must fail fast rather than hang.
	if code := app.RunHealthcheck(context.Background(), "http://127.0.0.1:1/api/v1/health/ready", http.DefaultClient, &stderr); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

```
docker run --rm -v "$PWD":/src -w /src photo-browser-test:latest go test ./internal/app/ 2>&1 | grep -E "FAIL|undefined"
```

期望：`app.RunHealthcheck undefined`。

- [ ] **Step 3: 實作 healthcheck.go**

```go
package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RunHealthcheck probes the readiness endpoint and maps it onto a process exit
// code, so the container healthcheck needs no HTTP client in the image.
func RunHealthcheck(ctx context.Context, url string, client *http.Client, stderr io.Writer) int {
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: status %d\n", res.StatusCode)
		return 1
	}
	return 0
}

// healthcheckURL builds the default self-probe URL from HTTP_LISTEN, which is
// typically ":8080" — a bare port with no host.
func healthcheckURL(listen string) string {
	if listen == "" {
		listen = ":8080"
	}
	if listen[0] == ':' {
		listen = "127.0.0.1" + listen
	}
	return "http://" + listen + "/api/v1/health/ready"
}
```

- [ ] **Step 4: 跑測試確認通過**

```
docker run --rm -v "$PWD":/src -w /src photo-browser-test:latest go test ./internal/app/ -run Healthcheck -v 2>&1 | tail -10
```

- [ ] **Step 5: 寫 backup 的失敗測試**

```go
// internal/app/backup_test.go
package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"photo-browser/internal/app"
	"photo-browser/internal/database"
)

func TestBackupProducesReadableCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.db")
	db, err := database.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (firebase_uid, role, enabled, created_at, updated_at)
	                      VALUES ('uid-a','admin',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "backup", "photo-2026-01-01.db")
	var stdout bytes.Buffer
	if err := app.RunBackup(context.Background(), db, out, &stdout); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	// The copy must be a usable database, not just bytes on disk.
	restored, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err := restored.QueryRow(`SELECT count(*) FROM users WHERE firebase_uid='uid-a'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restored user count=%d want 1", n)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := filepath.Join(dir, "b.db")
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := app.RunBackup(context.Background(), db, out, &stdout); err == nil {
		t.Fatal("expected an error rather than clobbering an existing backup")
	}
}
```

**注意**：測試需要 `_ "modernc.org/sqlite"` 的 driver 註冊——`database` package 已經 import 它，所以 `sql.Open("sqlite", ...)` 在同一個 binary 內可用。

- [ ] **Step 6: 跑測試確認失敗，然後實作 backup.go**

```go
package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunBackup writes a consistent snapshot of the live database with
// VACUUM INTO. Copying the .db file directly is unsafe under WAL — the
// snapshot would miss whatever is still in the write-ahead log.
func RunBackup(ctx context.Context, db *sql.DB, outPath string, stdout io.Writer) error {
	if outPath == "" {
		return fmt.Errorf("backup: --out is required")
	}
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("backup: %s already exists", outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return fmt.Errorf("backup: mkdir: %w", err)
	}
	// VACUUM INTO takes no parameters, so the path is quoted by hand.
	quoted := "'" + strings.ReplaceAll(outPath, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	fmt.Fprintf(stdout, "backup: wrote %s (%d bytes)\n", outPath, info.Size())
	return nil
}
```

Import 區塊需要 `"strings"`（`ReplaceAll`）、`"database/sql"`、`"os"`、`"path/filepath"`、`"context"`、`"fmt"`、`"io"`。

- [ ] **Step 7: 接上 CLI dispatch**

`internal/app/run.go` 的 `usage` 改成：

```go
const usage = `usage:
  photo-app index [--rebuild-thumbnails]
  photo-app rebuild
  photo-app admin <sub-command>
  photo-app serve
  photo-app healthcheck [--url=<url>]
  photo-app backup --out=<path>`
```

`Commands` struct 加兩個欄位：

```go
	Healthcheck func(ctx context.Context, args []string, stdout, stderr io.Writer) int
	Backup      func(ctx context.Context, args []string, stdout, stderr io.Writer) int
```

`Run` 的 switch 加兩個 case。healthcheck **不取任何 lock**（它只是 HTTP 探測，而且會在 serve 持有 `serve.lock` 時被呼叫）；backup **取 `index.lock`**，避免與索引同時寫入：

```go
	case "healthcheck":
		if cmds.Healthcheck == nil {
			fmt.Fprintln(stderr, "healthcheck not wired")
			return 1
		}
		// No lock: this runs while serve holds serve.lock, and it only reads.
		return cmds.Healthcheck(ctx, args[1:], stdout, stderr)
	case "backup":
		if cmds.Backup == nil {
			fmt.Fprintln(stderr, "backup not wired")
			return 1
		}
		lock, err := cmds.Lock()
		if err != nil {
			if errors.Is(err, filelock.ErrAlreadyLocked) {
				fmt.Fprintln(stderr, "photo-app is already running")
				return 3
			}
			fmt.Fprintf(stderr, "lock: %v\n", err)
			return 1
		}
		defer lock.Close()
		return cmds.Backup(ctx, args[1:], stdout, stderr)
```

`Main` 裡把兩個 closure 接上：

```go
		Healthcheck: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			url := healthcheckURL(cfg.HTTPListen)
			for _, a := range args {
				if strings.HasPrefix(a, "--url=") {
					url = strings.TrimPrefix(a, "--url=")
				} else {
					fmt.Fprintf(stderr, "unknown flag: %s\n%s\n", a, usage)
					return 2
				}
			}
			return RunHealthcheck(ctx, url, nil, stderr)
		},
		Backup: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			out := ""
			for _, a := range args {
				if strings.HasPrefix(a, "--out=") {
					out = strings.TrimPrefix(a, "--out=")
				} else {
					fmt.Fprintf(stderr, "unknown flag: %s\n%s\n", a, usage)
					return 2
				}
			}
			db, err := env.database()
			if err != nil {
				fmt.Fprintf(stderr, "backup: %v\n", err)
				return 1
			}
			if err := RunBackup(ctx, db, out, stdout); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return 1
			}
			return 0
		},
```

**注意**：`prodEnv` 目前沒有公開的 `database()` accessor——先讀 `internal/app/run.go` 的 `prodEnv` 定義，沿用它既有的 lazy-open 慣例加一個回傳 `*sql.DB` 的方法，不要自己另開一個連線（會拿到第二個 WAL writer）。

- [ ] **Step 8: 加 dispatch 測試**

```go
// internal/app/run_test.go 追加
func TestRunHealthcheckDispatches(t *testing.T) {
	called := false
	code := app.Run(context.Background(), []string{"healthcheck"}, io.Discard, io.Discard, app.Commands{
		Healthcheck: func(context.Context, []string, io.Writer, io.Writer) int { called = true; return 0 },
	})
	if code != 0 || !called {
		t.Fatalf("code=%d called=%v", code, called)
	}
}

func TestRunBackupTakesTheIndexLock(t *testing.T) {
	locked := false
	code := app.Run(context.Background(), []string{"backup", "--out=/tmp/x.db"}, io.Discard, io.Discard, app.Commands{
		Lock:   func() (io.Closer, error) { locked = true; return io.NopCloser(nil), nil },
		Backup: func(context.Context, []string, io.Writer, io.Writer) int { return 0 },
	})
	if code != 0 || !locked {
		t.Fatalf("code=%d locked=%v", code, locked)
	}
}

func TestRunHealthcheckTakesNoLock(t *testing.T) {
	code := app.Run(context.Background(), []string{"healthcheck"}, io.Discard, io.Discard, app.Commands{
		Lock:        func() (io.Closer, error) { t.Fatal("healthcheck must not take the index lock"); return nil, nil },
		Healthcheck: func(context.Context, []string, io.Writer, io.Writer) int { return 0 },
	})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
}
```

- [ ] **Step 9: 全套測試綠燈**

```
make test
```

期望：全部 `ok`，exit 0。

- [ ] **Step 10: 在真的 runtime image 上驗證**

```bash
make build
docker run --rm photo-browser:local healthcheck --url=http://127.0.0.1:1/x ; echo "exit=$? (expect 1)"
```

期望 exit 1——這證明子命令真的打包進 runtime image 且能執行（runtime image 沒有 shell 以外的工具，所以這一步是必要的煙霧測試）。

- [ ] **Step 11: Commit**

```bash
git add internal/app/
git commit -m "feat: photo-app healthcheck and backup subcommands"
```

---

## Task 3: 低磁碟空間時停止產生 thumbnail

Spec §3 的固定限制之一：「Thumbnail cache 沒有預先保留固定容量；部署前記錄 volume free space，並在低空間時停止產生 thumbnail、保留 original 不受影響。」目前 indexer 會無條件產生縮圖，在 volume 接近滿的時候會把最後的空間吃光——而 originals 與 SQLite 都在同一顆 volume 上，那會連帶讓資料庫寫不進去。

順帶補上 spec §11 的另一個缺口：目前 walk 產生的 warning 只被 `counts.Warnings++` 計數，內容完全看不到，無法「診斷 scan 與 disk failures」。這個 task 加一個可選的 `Warn` hook，讓 warning 真的印得出來。

**Files:**
- Create: `internal/indexer/diskspace.go`
- Create: `internal/indexer/diskspace_test.go`
- Modify: `internal/scanner/scanner.go`（加一個 warning code）
- Modify: `internal/indexer/indexer.go`（`Indexer` 加 `FreeSpace`、`MinFreeBytes`、`Warn`）
- Modify: `internal/indexer/indexer_test.go`
- Modify: `internal/config/config.go` 與 `internal/config/config_test.go`
- Modify: `internal/app/run.go`（wiring）

**Interfaces:**
- Consumes: 既有 `Indexer.Thumbnail ThumbnailFn`、`Indexer.ThumbnailDir`、`scanner.Warning{Path, Code string}`。
- Produces:
  - `func FreeBytes(path string) (uint64, error)` — 包 `golang.org/x/sys/unix.Statfs`（`golang.org/x/sys` 已在 go.mod，`internal/filelock` 已在用）。
  - `scanner.CodeLowDiskSpace = "low_disk_space"`。
  - `Indexer.FreeSpace func(path string) (uint64, error)`、`Indexer.MinFreeBytes int64`、`Indexer.Warn func(scanner.Warning)`（三者皆可為零值／nil）。
  - `config.Config.ThumbnailMinFreeBytes int64`，環境變數 `THUMBNAIL_MIN_FREE_BYTES`，預設 `2147483648`（2 GiB），`0` 表示停用檢查。
  - 行為：每次 run **只檢查一次**空間；不足時跳過所有縮圖產生並記一個 warning，photo row 照常寫入（`thumbnail_key` 留 NULL），既有縮圖與 originals 都不受影響。

**注意**：`internal/indexer/indexer_test.go` 是 **internal test package**（`package indexer`），所以測試裡直接用 `Indexer`、`DefaultBatchSize` 等未匯出的名字，不加 `indexer.` 前綴。新的 `diskspace_test.go` 也照這個慣例。

- [ ] **Step 1: 先讀既有 fixture**

讀 `internal/indexer/indexer_test.go:19-70`。你會用到：

- `newIndexer(t, entries, walkErr) (*Indexer, *catalog.Store, *spies)`
- `entry(rel string, size, mtime int64) scanner.Entry`
- `spies.thumbCalls`（`int32`，用 `atomic.LoadInt32` 讀）

**不要**自己另建一套 fixture。

- [ ] **Step 2: 寫 `diskspace_test.go`**

```go
package indexer

import "testing"

func TestFreeBytesReportsSomethingForTempDir(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("free bytes = 0 for a writable temp dir")
	}
}

func TestFreeBytesFailsForMissingPath(t *testing.T) {
	if _, err := FreeBytes("/definitely/not/a/path"); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}
```

- [ ] **Step 3: 寫行為測試（追加到 `indexer_test.go`）**

```go
func TestRunSkipsThumbnailsWhenDiskIsLow(t *testing.T) {
	idx, _, sp := newIndexer(t, []scanner.Entry{entry("旅遊/日本/a.jpg", 10, 1)}, nil)
	idx.MinFreeBytes = 2 << 30
	idx.FreeSpace = func(string) (uint64, error) { return 1 << 30, nil } // 1 GiB < 2 GiB

	counts, err := idx.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&sp.thumbCalls); got != 0 {
		t.Fatalf("thumbnail generated %d times despite low disk", got)
	}
	// Losing thumbnails is acceptable; losing the catalogue is not.
	if counts.New != 1 {
		t.Fatalf("new=%d want 1; low disk must not stop cataloguing", counts.New)
	}
	if counts.Warnings == 0 {
		t.Fatal("expected a warning recording the skipped thumbnails")
	}
}

func TestRunReportsTheLowDiskWarning(t *testing.T) {
	idx, _, _ := newIndexer(t, []scanner.Entry{entry("旅遊/日本/a.jpg", 10, 1)}, nil)
	idx.MinFreeBytes = 2 << 30
	idx.FreeSpace = func(string) (uint64, error) { return 1 << 30, nil }
	var seen []scanner.Warning
	idx.Warn = func(w scanner.Warning) { seen = append(seen, w) }

	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Code != scanner.CodeLowDiskSpace {
		t.Fatalf("warnings=%+v want one %s", seen, scanner.CodeLowDiskSpace)
	}
}

func TestRunGeneratesThumbnailsWhenDiskIsFine(t *testing.T) {
	idx, _, sp := newIndexer(t, []scanner.Entry{entry("旅遊/日本/a.jpg", 10, 1)}, nil)
	idx.MinFreeBytes = 2 << 30
	idx.FreeSpace = func(string) (uint64, error) { return 100 << 30, nil }

	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&sp.thumbCalls) == 0 {
		t.Fatal("no thumbnails generated with plenty of free space")
	}
}

func TestRunChecksFreeSpaceOncePerRun(t *testing.T) {
	entries := []scanner.Entry{
		entry("旅遊/日本/a.jpg", 10, 1),
		entry("旅遊/日本/b.jpg", 11, 2),
		entry("旅遊/日本/c.jpg", 12, 3),
	}
	idx, _, _ := newIndexer(t, entries, nil)
	calls := 0
	idx.MinFreeBytes = 1 << 30
	idx.FreeSpace = func(string) (uint64, error) { calls++; return 100 << 30, nil }

	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("FreeSpace called %d times; must be once per run", calls)
	}
}

func TestRunProceedsWhenFreeSpaceIsUnknown(t *testing.T) {
	idx, _, sp := newIndexer(t, []scanner.Entry{entry("旅遊/日本/a.jpg", 10, 1)}, nil)
	idx.MinFreeBytes = 2 << 30
	idx.FreeSpace = func(string) (uint64, error) { return 0, errors.New("statfs failed") }

	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	// An unreadable statfs must not silently disable thumbnails.
	if atomic.LoadInt32(&sp.thumbCalls) == 0 {
		t.Fatal("thumbnails were skipped because free space could not be read")
	}
}

func TestZeroMinFreeBytesDisablesTheCheck(t *testing.T) {
	idx, _, _ := newIndexer(t, []scanner.Entry{entry("旅遊/日本/a.jpg", 10, 1)}, nil)
	called := false
	idx.MinFreeBytes = 0
	idx.FreeSpace = func(string) (uint64, error) { called = true; return 0, nil }

	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("FreeSpace was consulted even though the check is disabled")
	}
}
```

- [ ] **Step 4: 跑測試確認失敗**

```
docker run --rm -v "$PWD":/src -w /src photo-browser-test:latest go test ./internal/indexer/ 2>&1 | grep -E "FAIL|undefined"
```

期望：`FreeBytes undefined`、`idx.MinFreeBytes undefined`、`scanner.CodeLowDiskSpace undefined`。

- [ ] **Step 5: 實作 `diskspace.go`**

```go
package indexer

import "golang.org/x/sys/unix"

// FreeBytes reports the space available to an unprivileged writer at path.
// Bavail rather than Bfree: Bfree counts blocks reserved for root, which the
// non-root service user cannot actually use.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
```

- [ ] **Step 6: 加 warning code**

`internal/scanner/scanner.go` 的 const 區塊追加：

```go
	// Emitted by the indexer, not by Walk: the thumbnail volume is too full to
	// keep generating derivatives.
	CodeLowDiskSpace = "low_disk_space"
```

- [ ] **Step 7: 改 `indexer.go`**

`Indexer` struct 追加三個欄位：

```go
	// Thumbnail generation stops when the thumbnail volume drops below
	// MinFreeBytes, so a filling disk costs thumbnails rather than the
	// catalogue or the originals. Zero disables the check.
	FreeSpace    func(path string) (uint64, error)
	MinFreeBytes int64

	// Optional sink for warnings. Without it warnings are only counted, which
	// is not enough to diagnose a scan or a filling disk.
	Warn func(scanner.Warning)
```

在 `RunWithScanID` 內，`idx.Walk` 呼叫處把 warning 一併轉給 `Warn`：

```go
	entries, walkErr := idx.Walk(idx.PhotoRoot, func(w scanner.Warning) {
		counts.Warnings++
		idx.warn(w)
	})
```

`counts.Seen = int64(len(entries))` 之後、進入 per-entry 迴圈之前，插入一次性檢查：

```go
	thumbsAllowed := idx.thumbnailsAllowed(&counts)
```

新增兩個小 helper：

```go
// warn forwards a warning to the optional sink. Counting happens at the call
// site, because walk warnings and indexer warnings are counted differently.
func (idx *Indexer) warn(w scanner.Warning) {
	if idx.Warn != nil {
		idx.Warn(w)
	}
}

// thumbnailsAllowed is consulted once per run: a syscall per photo would be
// wasted work, and a decision that flips mid-run would be harder to explain.
func (idx *Indexer) thumbnailsAllowed(counts *catalog.ScanCounts) bool {
	if idx.MinFreeBytes <= 0 {
		return true
	}
	probe := idx.FreeSpace
	if probe == nil {
		probe = FreeBytes
	}
	free, err := probe(idx.ThumbnailDir)
	if err != nil {
		// Cannot tell. Keep generating — a broken statfs must not quietly turn
		// thumbnails off for good.
		return true
	}
	if free >= uint64(idx.MinFreeBytes) {
		return true
	}
	counts.Warnings++
	idx.warn(scanner.Warning{Path: idx.ThumbnailDir, Code: scanner.CodeLowDiskSpace})
	return false
}
```

`indexer.go:175`、`:209`、`:232` 三處 `idx.Thumbnail(...)` 各包一層 `if thumbsAllowed { ... }`。**跳過時不要**把既有的 `thumbnail_key` 清成 NULL——舊縮圖還在磁碟上，仍然可以服務；只有新的不產生。`thumbsAllowed` 需要傳進這三處所在的函式（目前它們是 `RunWithScanID` 呼叫的 helper），照既有的參數傳遞方式加一個 `bool` 參數。

- [ ] **Step 8: 加 config**

`internal/config/config.go` 的 `Config` 加欄位、`Load` 加一行、檔尾加 helper：

```go
	ThumbnailMinFreeBytes int64
```

```go
	// Below this much free space on the thumbnail volume, an index run stops
	// generating thumbnails. 2 GiB leaves SQLite and its WAL room to breathe on
	// a volume shared with the originals.
	c.ThumbnailMinFreeBytes = intValue("THUMBNAIL_MIN_FREE_BYTES", 2*1024*1024*1024)
```

```go
func intValue(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
```

需要 import `"strconv"`。

`internal/config/config_test.go` 追加（沿用該檔既有的 env 設定慣例，先讀一遍）：

```go
func TestThumbnailMinFreeBytesDefaultsAndParses(t *testing.T) {
	const def = 2 * 1024 * 1024 * 1024

	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ThumbnailMinFreeBytes != def {
		t.Fatalf("default=%d want %d", c.ThumbnailMinFreeBytes, def)
	}

	t.Setenv("THUMBNAIL_MIN_FREE_BYTES", "0")
	if c, err = config.Load(); err != nil {
		t.Fatal(err)
	} else if c.ThumbnailMinFreeBytes != 0 {
		t.Fatalf("explicit 0 should disable the check, got %d", c.ThumbnailMinFreeBytes)
	}

	t.Setenv("THUMBNAIL_MIN_FREE_BYTES", "not-a-number")
	if c, err = config.Load(); err != nil {
		t.Fatal(err)
	} else if c.ThumbnailMinFreeBytes != def {
		t.Fatalf("garbage should fall back to the default, got %d", c.ThumbnailMinFreeBytes)
	}
}
```

- [ ] **Step 9: 接上 wiring**

`internal/app/run.go` 的 `prodEnv.indexer()` 建 `&indexer.Indexer{...}` 處加三行：

```go
		MinFreeBytes: cfg.ThumbnailMinFreeBytes,
		FreeSpace:    indexer.FreeBytes,
		Warn: func(w scanner.Warning) {
			fmt.Fprintf(stdout, "warning: %s %s\n", w.Code, w.Path)
		},
```

`prodEnv` 目前不一定持有 `stdout`——先讀 `prodEnv` 的定義，沿用它既有的欄位；若沒有，就把 `stdout` 加進 struct（`Main` 已經有這個值）。需要 import `"photo-browser/internal/scanner"`。

- [ ] **Step 10: 跑全套測試**

```
make test
```

期望：全部 `ok`，exit 0。

- [ ] **Step 11: 真實驗證——`statfs` 與跳過行為**

單元測試用的是 fake。實際 syscall 行為要真的驗一次（此步驟需要 Task 8 的 prodcheck stack；若尚未做到 Task 8，把這步留到 Task 8 完成後補跑，並在此打勾時註明）：

```bash
make prodcheck-up
# 門檻設到大於任何磁碟的剩餘空間，強迫觸發
docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps \
  -e THUMBNAIL_MIN_FREE_BYTES=999999999999999 photo-app index 2>&1 | tail -5
```

期望：輸出含 `warning: low_disk_space /srv/thumbnails`，且 `scan:` 那行的 `warnings=` 大於 0，`seen` 正常。原圖仍可服務：

```bash
TOKEN=$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1")
PID=$(curl -s -H "Host: photos-api.localhost" -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8088/api/v1/photos?limit=1" | python3 -c "import sys,json;print(json.load(sys.stdin)['items'][0]['id'])")
curl -s -o /dev/null -w "orig=%{http_code}\n" -H "Host: photos-api.localhost" \
  -H "Authorization: Bearer $TOKEN" "http://localhost:8088/api/v1/photos/$PID/original"
```

期望：`orig=200`——低空間**只**影響縮圖產生，不影響原圖服務。

- [ ] **Step 12: 加進 production 設定**

`deploy/compose/.env.prod.example` 追加：

```
# Below this many free bytes on the thumbnail volume, an index run stops
# generating thumbnails (originals and the catalogue are unaffected).
# 0 disables the check. Default 2 GiB.
THUMBNAIL_MIN_FREE_BYTES=2147483648
```

`deploy/compose/docker-compose.prod.yml` 的 `photo-app` environment 追加：

```yaml
      THUMBNAIL_MIN_FREE_BYTES: ${THUMBNAIL_MIN_FREE_BYTES}
```

**注意**：Task 5 才會建立這兩個檔案。若你是照順序執行，這一步在 Task 5 完成後回頭補；在此打勾時註明「已於 Task 5 併入」。

- [ ] **Step 13: Commit**

```bash
git add internal/indexer/ internal/scanner/scanner.go internal/config/ internal/app/run.go
git commit -m "feat: stop generating thumbnails when the volume runs low"
```

---

## Task 4: API 回應標記 no-store + production nginx 設定

Spec §6 要求 `/api/` 與 protected media 不得進 Cloudflare shared cache。Cloudflare 端的 cache rule 由 runbook 設定，但**來源本身**也必須說清楚：per-user JSON 標 `no-store`，media 標 `private`。這樣就算哪天 cache rule 設錯，共用快取仍不會保留別人的資料。

**Files:**
- Modify: `internal/httpapi/errors.go`
- Modify: `internal/httpapi/errors_test.go`
- Create: `deploy/nginx/nginx.prod.conf`

**Interfaces:**
- Consumes: 既有 `WriteJSON` / `WriteError`、`deploy/nginx/nginx.conf`（acceptance 版，作為基底）。
- Produces: 所有 JSON 回應帶 `Cache-Control: no-store`；`nginx.prod.conf` 監聽 8080（unprivileged）、只接受 `PUBLIC_API_HOST` 的 Host、originals 帶 `private, max-age=3600`。

- [ ] **Step 1: 先寫失敗測試**

```go
// internal/httpapi/errors_test.go 追加
func TestWriteJSONMarksResponsesNoStore(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteJSON(rec, 200, map[string]string{"a": "b"})
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", got)
	}
}

func TestWriteErrorMarksResponsesNoStore(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, 403, httpapi.CodeForbidden, "nope")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", got)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

```
docker run --rm -v "$PWD":/src -w /src photo-browser-test:latest go test ./internal/httpapi/ -run NoStore 2>&1 | tail -5
```

期望：兩個測試都因為 `Cache-Control=""` 而失敗。

- [ ] **Step 3: 實作**

在 `errors.go` 的兩個函式各加一行，並說明原因：

```go
// WriteError renders a fixed JSON envelope. Callers must NOT pass SQL, stack,
// or filesystem paths in message — only safe, opaque wording.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Every JSON response is scoped to one authenticated user, so it must never
	// land in a shared cache — including Cloudflare's.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// WriteJSON serializes v as JSON with the given status. Internal only.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

**注意**：media handler 走 `X-Accel-Redirect`，body 為空且由 nginx 內部 location 覆寫 header，因此不受這行影響——但要在 Step 6 實測確認縮圖仍帶得到 `private, max-age=86400`。

- [ ] **Step 4: 跑測試確認通過**

```
make test
```

- [ ] **Step 5: 寫 `deploy/nginx/nginx.prod.conf`**

```nginx
# Production nginx. Differences from the acceptance config:
#   * listens on 8080 as a non-root user (nginxinc/nginx-unprivileged)
#   * only answers for the tunnel's public hostname; anything else gets 444,
#     so a stray ingress rule can never surface this service under another name
#   * access log carries no Authorization header and no query string
#   * originals carry a private cache-control so no shared cache retains them
worker_processes 1;

events {
  worker_connections 512;
}

http {
  include       /etc/nginx/mime.types;
  default_type  application/octet-stream;
  sendfile      on;
  server_tokens off;

  # $request would include the query string; $uri deliberately does not, and no
  # token ever appears in a URL anyway (see Task 8's check).
  log_format  main '$remote_addr - $status "$request_method $uri" '
                   'reqid=$http_x_request_id bytes=$body_bytes_sent rt=$request_time';
  access_log /var/log/nginx/access.log main;
  error_log  /var/log/nginx/error.log warn;

  client_max_body_size 1m;
  client_body_timeout 10s;
  client_header_timeout 10s;

  upstream photo_backend {
    server photo-app:8080;
    keepalive 8;
  }

  # Any Host we did not expect is dropped without a response.
  server {
    listen 8080 default_server;
    server_name _;
    return 444;
  }

  server {
    listen 8080;
    server_name ${PUBLIC_API_HOST};

    location / {
      proxy_pass http://photo_backend;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Request-ID $http_x_request_id;
      proxy_read_timeout 60s;
      # Preserve backend cache-control / etag headers verbatim.
      proxy_pass_header ETag;
      proxy_pass_header Cache-Control;
    }

    location /internal-media/originals/ {
      internal;
      autoindex off;
      alias /srv/photos/;
      add_header X-Content-Type-Options nosniff always;
      # private keeps originals out of Cloudflare's shared cache while still
      # letting the user's own browser reuse them within a session.
      add_header Cache-Control "private, max-age=3600" always;
    }

    location /internal-media/thumbnails/ {
      internal;
      autoindex off;
      alias /srv/thumbnails/;
      add_header X-Content-Type-Options nosniff always;
      add_header Cache-Control "private, max-age=86400, immutable" always;
    }
  }
}
```

`${PUBLIC_API_HOST}` 由 nginx image 的 `envsubst` 樣板機制展開：檔案要放進容器的 `/etc/nginx/templates/nginx.conf.template`，官方 entrypoint 會輸出到 `/etc/nginx/nginx.conf`。Task 5 的 compose 會照這個路徑掛載。

- [ ] **Step 6: 手動驗證 media header 沒被 no-store 影響**

這一步要等 Task 7 的 prodcheck stack 才能跑完整版；先在既有 dev stack 上驗證 Step 3 的改動沒有破壞縮圖：

```bash
make dev-web-stack
deadline=$((SECONDS+90)); until curl -sf http://localhost:8081/api/v1/health/live >/dev/null; do [ $SECONDS -gt $deadline ] && break; done
TOKEN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")
echo "--- JSON ---"
curl -s -D- -o /dev/null -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/v1/categories | grep -i cache-control
PID=$(curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/v1/photos?limit=1" | python3 -c "import sys,json;p=json.load(sys.stdin)['items'][0];print(p['id'],p['thumbnail_key'])")
set -- $PID
echo "--- thumbnail ---"
curl -s -D- -o /dev/null -H "Authorization: Bearer $TOKEN" "http://localhost:8081/api/v1/photos/$1/thumbnail/$2" | grep -i cache-control
make dev-web-stack-down
```

期望：JSON 回 `Cache-Control: no-store`；縮圖回 `Cache-Control: private, max-age=86400, immutable`（**不是** no-store）。若縮圖也變成 no-store，表示 `X-Accel-Redirect` 的 header 繼承出了問題，停下來修再繼續。

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi/errors.go internal/httpapi/errors_test.go deploy/nginx/nginx.prod.conf
git commit -m "feat: no-store on api json and production nginx config"
```

---

## Task 5: Production compose topology

Spec §4／§5／§8 的所有硬性要求（三個 service、non-root、cap_drop、無 host port、log rotation、resource limit）都落在這一份檔案。

**Files:**
- Create: `deploy/compose/docker-compose.prod.yml`
- Create: `deploy/compose/.env.prod.example`
- Modify: `.gitignore`（排除 `deploy/compose/.env.prod` 與 `deploy/cloudflared/*.json`）

**Interfaces:**
- Consumes: `photo-browser:${APP_VERSION}` 映像（Task 6 的 `make build-prod` 產生）、`deploy/nginx/nginx.prod.conf`、`deploy/cloudflared/config.yml`（Task 7）。
- Produces: 可 `docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod up -d` 的三 service 拓撲；服務名 `nginx` / `photo-app` / `cloudflared`。

- [ ] **Step 1: 寫 `.env.prod.example`**

```bash
cat > deploy/compose/.env.prod.example <<'ENV'
# Copy to .env.prod on the NAS and fill in. .env.prod is gitignored — it holds
# hostnames and paths, never credentials (the tunnel credential is a mounted
# file, see deploy/cloudflared/).

# Pinned application version. Must match an image built by `make build-prod`.
APP_VERSION=0.1.0

# Public hostnames (Task 14 assigns the real values).
PUBLIC_APP_HOST=photos.example.com
PUBLIC_API_HOST=photos-api.example.com

# Firebase project id — the backend derives the issuer from it.
FIREBASE_PROJECT_ID=

# NAS paths. PHOTO_ROOT is mounted read-only and is never written to.
NAS_PHOTOS=/volume1/photos
NAS_DATA=/volume1/docker/photo-browser/data
NAS_THUMBS=/volume1/docker/photo-browser/thumbnails

# Service identity: the UID/GID that can read the photo share. Task 1's
# preflight report records it.
APP_UID=1026
APP_GID=100

# Resource ceilings. Start here and re-tune from Task 12's measurements; the
# box has 2 GB total, so these must leave DSM room to breathe.
APP_MEM_LIMIT=384m
NGINX_MEM_LIMIT=64m
CLOUDFLARED_MEM_LIMIT=128m

# Pinned image tags — never :latest, so a restart can't silently upgrade.
NGINX_IMAGE=nginxinc/nginx-unprivileged:1.27-alpine
CLOUDFLARED_IMAGE=cloudflare/cloudflared:2024.8.3
ENV
```

- [ ] **Step 2: 寫 compose 檔**

```yaml
# Production topology for the NAS. Three services, no published ports:
# cloudflared dials out to Cloudflare, so nothing listens on a host interface
# and no router port forwarding is needed (or permitted).
services:
  photo-app:
    image: photo-browser:${APP_VERSION}
    command: ["serve"]
    user: "${APP_UID}:${APP_GID}"
    environment:
      PHOTO_ROOT: /srv/photos
      DATA_DIR: /srv/data
      THUMBNAIL_DIR: /srv/thumbnails
      HTTP_LISTEN: ":8080"
      FIREBASE_PROJECT_ID: ${FIREBASE_PROJECT_ID}
      ALLOWED_ORIGINS: https://${PUBLIC_APP_HOST}
      INTERNAL_MEDIA_ORIGINALS: /internal-media/originals
      INTERNAL_MEDIA_THUMBNAILS: /internal-media/thumbnails
    volumes:
      - ${NAS_PHOTOS}:/srv/photos:ro
      - ${NAS_DATA}:/srv/data:rw
      - ${NAS_THUMBS}:/srv/thumbnails:rw
    read_only: true
    tmpfs:
      - /tmp:size=16m
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    mem_limit: ${APP_MEM_LIMIT}
    restart: unless-stopped
    healthcheck:
      # The runtime image has no curl; the binary probes itself instead.
      test: ["CMD", "photo-app", "healthcheck"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 20s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

  nginx:
    image: ${NGINX_IMAGE}
    user: "${APP_UID}:${APP_GID}"
    environment:
      PUBLIC_API_HOST: ${PUBLIC_API_HOST}
    volumes:
      # The official entrypoint runs envsubst over /etc/nginx/templates/*.template.
      - ../../deploy/nginx/nginx.prod.conf:/etc/nginx/templates/nginx.conf.template:ro
      - ${NAS_PHOTOS}:/srv/photos:ro
      - ${NAS_THUMBS}:/srv/thumbnails:ro
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    mem_limit: ${NGINX_MEM_LIMIT}
    restart: unless-stopped
    depends_on:
      photo-app:
        condition: service_healthy
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

  cloudflared:
    image: ${CLOUDFLARED_IMAGE}
    command: ["tunnel", "--no-autoupdate", "--config", "/etc/cloudflared/config.yml", "run"]
    user: "${APP_UID}:${APP_GID}"
    volumes:
      - ../../deploy/cloudflared/config.yml:/etc/cloudflared/config.yml:ro
      # Credential file, mode 0400, owned by APP_UID. Never in git, never in an
      # image layer — see deploy/cloudflared/README.md.
      - ${NAS_DATA}/cloudflared:/etc/cloudflared/creds:ro
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    mem_limit: ${CLOUDFLARED_MEM_LIMIT}
    restart: unless-stopped
    depends_on:
      - nginx
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

**沒有 `ports:` 區塊是刻意的**，不是遺漏——spec 明令 application、DB 與 internal nginx port 不得 publish 到 Internet-facing host interface。

- [ ] **Step 3: 更新 `.gitignore`**

```bash
cat >> .gitignore <<'EOF'
deploy/compose/.env.prod
deploy/cloudflared/*.json
EOF
```

- [ ] **Step 4: 驗證 compose 檔語法與變數展開**

```bash
cp deploy/compose/.env.prod.example /tmp/env.prod
docker compose -f deploy/compose/docker-compose.prod.yml --env-file /tmp/env.prod config > /tmp/resolved.yml
echo "exit=$?"
grep -c "ports:" /tmp/resolved.yml || echo "no published ports (expected)"
grep -A2 "read_only" /tmp/resolved.yml | head
grep "image:" /tmp/resolved.yml
```

期望：exit 0；`ports:` 出現 0 次；三個 image 都有具體 tag、沒有 `:latest`、沒有未展開的 `${...}`。

- [ ] **Step 5: 驗證沒有寫死的 hostname**

```bash
grep -nE "example\.com" deploy/compose/docker-compose.prod.yml deploy/nginx/nginx.prod.conf && echo "FAIL: hardcoded hostname" || echo "OK: hostnames come from env"
```

期望：印出 `OK`。

- [ ] **Step 6: Commit**

```bash
git add deploy/compose/docker-compose.prod.yml deploy/compose/.env.prod.example .gitignore
git commit -m "feat: hardened production compose topology"
```

---

## Task 6: Pinned production image build

Spec 要求 reproducible container image、保守的 `linux/amd64` build，以及 pinned version（restart 不得默默升級）。既有 `make build` 產生 `photo-browser:local`，不足以做版本化部署。

**Files:**
- Create: `deploy/VERSION`
- Modify: `Makefile`（`build-prod`、`verify-image`）
- Create: `scripts/verify-image.sh`

**Interfaces:**
- Consumes: 既有 `Dockerfile` 的 `runtime` target（已 `USER 65532:65532`、`GOAMD64=v1`）。
- Produces: `make build-prod` → `photo-browser:$(cat deploy/VERSION)` 與 `photo-browser:latest-prod`；`make verify-image` 檢查映像的硬性性質。

- [ ] **Step 1: 建版本檔**

```bash
echo "0.1.0" > deploy/VERSION
```

- [ ] **Step 2: 加 Makefile target**

```makefile
APP_VERSION := $(shell cat deploy/VERSION)

.PHONY: build-prod verify-image

# Reproducible, conservatively targeted image for the Braswell NAS.
# GOAMD64=v1 is set in the Dockerfile and must not be relaxed.
build-prod:
	docker build --platform linux/amd64 --target runtime \
	  -t photo-browser:$(APP_VERSION) -t photo-browser:latest-prod .
	@echo "built photo-browser:$(APP_VERSION)"

verify-image:
	bash scripts/verify-image.sh photo-browser:$(APP_VERSION)
```

- [ ] **Step 3: 寫 `scripts/verify-image.sh`**

```bash
cat > scripts/verify-image.sh <<'SH'
#!/usr/bin/env bash
# Asserts the production image's non-negotiable properties. Cheap enough to run
# on every build; catches a Dockerfile edit that quietly loosens the target.
set -euo pipefail

IMAGE="${1:?usage: verify-image.sh <image>}"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1: got '$2' want '$3'"; fail=1; fi; }

arch="$(docker image inspect -f '{{.Architecture}}' "$IMAGE")"
check "architecture is amd64" "$arch" "amd64"

os="$(docker image inspect -f '{{.Os}}' "$IMAGE")"
check "os is linux" "$os" "linux"

user="$(docker image inspect -f '{{.Config.User}}' "$IMAGE")"
check "runs as non-root" "$user" "65532:65532"

entry="$(docker image inspect -f '{{index .Config.Entrypoint 0}}' "$IMAGE")"
check "entrypoint is photo-app" "$entry" "photo-app"

# GOAMD64 is a build arg, so it is not visible on the image. Assert it at the
# source instead — a relaxed value here is the failure we actually care about.
if grep -qE 'GOAMD64=v[234]' Dockerfile; then
  echo "FAIL  Dockerfile raises GOAMD64 above v1 (Braswell has no AVX2)"
  fail=1
else
  echo "ok    Dockerfile keeps GOAMD64=v1"
fi

# The subcommands the prod compose file depends on must exist in this image.
for sub in healthcheck backup; do
  if docker run --rm "$IMAGE" 2>&1 | grep -q "photo-app $sub"; then
    echo "ok    image exposes '$sub'"
  else
    echo "FAIL  image does not expose '$sub'"
    fail=1
  fi
done

# Nothing that could leak a credential should be baked in.
if docker image inspect -f '{{json .Config.Env}}' "$IMAGE" | grep -qiE 'token|secret|password|credential'; then
  echo "FAIL  image env looks like it carries a credential"
  fail=1
else
  echo "ok    image env carries no credential-shaped variables"
fi

[ "$fail" -eq 0 ] || { echo "verify-image FAILED"; exit 1; }
echo "verify-image OK"
SH
chmod +x scripts/verify-image.sh
```

- [ ] **Step 4: 建置並驗證**

```bash
make build-prod
make verify-image
echo "exit=$?"
```

期望：每一行都是 `ok`，最後 `verify-image OK`，exit 0。

- [ ] **Step 5: 驗證檢查真的會擋（red check）**

```bash
sed -i.bak 's/GOAMD64=v1/GOAMD64=v3/' Dockerfile
bash scripts/verify-image.sh photo-browser:$(cat deploy/VERSION); echo "exit=$? (expect 1)"
mv Dockerfile.bak Dockerfile
bash scripts/verify-image.sh photo-browser:$(cat deploy/VERSION) >/dev/null && echo "restored, still OK"
```

期望：改壞時 exit 1 並印出 `Dockerfile raises GOAMD64 above v1`；還原後回到 OK。

- [ ] **Step 6: Commit**

```bash
git add deploy/VERSION scripts/verify-image.sh Makefile
git commit -m "chore: pinned production image build with hardening checks"
```

---

## Task 7: cloudflared ingress 設定與 credential 契約

**Files:**
- Create: `deploy/cloudflared/config.yml`
- Create: `deploy/cloudflared/README.md`
- Create: `scripts/verify-tunnel-config.sh`

**Interfaces:**
- Consumes: Task 5 compose 的 service name `nginx`（port 8080）。
- Produces: 單一 ingress rule + catch-all 404；credential 檔案路徑契約 `/etc/cloudflared/creds/<tunnel-id>.json`。

- [ ] **Step 1: 寫 `config.yml`**

```yaml
# Cloudflare Tunnel ingress. Exactly one hostname reaches exactly one service.
#
# The catch-all below is what keeps DSM, SMB, SSH and Container Manager
# unreachable: anything that is not the API hostname is answered by cloudflared
# itself with a 404 and never touches the NAS network.
#
# TUNNEL_ID and the hostname are filled in by docs/deploy/01-external-services.md.
tunnel: REPLACE_WITH_TUNNEL_ID
credentials-file: /etc/cloudflared/creds/REPLACE_WITH_TUNNEL_ID.json

# No metrics port is published; cloudflared logs to stdout and compose rotates it.
loglevel: info

ingress:
  - hostname: REPLACE_WITH_PUBLIC_API_HOST
    service: http://nginx:8080
    originRequest:
      connectTimeout: 10s
      # Large originals stream through; do not cut them off early.
      noTLSVerify: false
      httpHostHeader: REPLACE_WITH_PUBLIC_API_HOST
  - service: http_status:404
```

**注意**：`httpHostHeader` 是必要的——`nginx.prod.conf` 用 `server_name` 過濾 Host，若 cloudflared 傳其他 Host 會被 444 掉。

- [ ] **Step 2: 寫 credential 契約文件**

```bash
cat > deploy/cloudflared/README.md <<'MD'
# Tunnel credentials

`cloudflared tunnel create <name>` writes a JSON credential to
`~/.cloudflared/<tunnel-id>.json`. That file is the tunnel's private key.

Rules:

- It is **never** committed. `.gitignore` excludes `deploy/cloudflared/*.json`.
- It is **never** copied into an image layer. The compose file mounts it at
  runtime from `${NAS_DATA}/cloudflared/`.
- On the NAS it must be `chmod 0400` and owned by `APP_UID:APP_GID`.

Install it like this (on the NAS, after Task 14's runbook creates the tunnel):

```
mkdir -p /volume1/docker/photo-browser/data/cloudflared
cp <tunnel-id>.json /volume1/docker/photo-browser/data/cloudflared/
chown 1026:100 /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
chmod 0400     /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
```

Then replace the three `REPLACE_WITH_*` placeholders in `config.yml`.

To rotate: delete the tunnel in the Cloudflare dashboard, create a new one,
replace the file and the id, and restart the `cloudflared` service. Nothing
else in the deployment changes.
MD
```

- [ ] **Step 3: 寫設定檢查腳本**

```bash
cat > scripts/verify-tunnel-config.sh <<'SH'
#!/usr/bin/env bash
# Guards the two ways a tunnel config goes wrong: a placeholder shipped to
# production, or an ingress rule that reaches something it must not.
set -euo pipefail

CFG="${1:-deploy/cloudflared/config.yml}"
MODE="${2:-template}"   # template | deployed
fail=0

# Any ingress service must be the nginx photo service or the closing 404.
while read -r svc; do
  case "$svc" in
    "http://nginx:8080"|"http_status:404") ;;
    *) echo "FAIL ingress routes to '$svc' — only the nginx photo service is allowed"; fail=1 ;;
  esac
done < <(grep -E '^\s+(- )?service:' "$CFG" | sed -E 's/.*service:\s*//')

# A catch-all must terminate the list, or an unmatched host falls through.
if ! tail -3 "$CFG" | grep -q 'service: http_status:404'; then
  echo "FAIL config does not end with a http_status:404 catch-all"; fail=1
fi

# Nothing may point at DSM, SMB, SSH or Container Manager.
if grep -qiE '5000|5001|445|139|22|synology|dsm|container-manager' "$CFG"; then
  echo "FAIL config mentions a DSM/SMB/SSH/Container Manager target"; fail=1
fi

# A credential must never be inline.
if grep -qiE 'credentials-json|AccountTag|TunnelSecret' "$CFG"; then
  echo "FAIL credential material is inline in the config"; fail=1
fi

if [ "$MODE" = deployed ]; then
  if grep -q 'REPLACE_WITH' "$CFG"; then
    echo "FAIL placeholders are still present in a deployed config"; fail=1
  fi
fi

[ "$fail" -eq 0 ] || { echo "verify-tunnel-config FAILED ($CFG, mode=$MODE)"; exit 1; }
echo "verify-tunnel-config OK ($CFG, mode=$MODE)"
SH
chmod +x scripts/verify-tunnel-config.sh
```

- [ ] **Step 4: 驗證（兩個方向都要）**

```bash
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml template; echo "exit=$? (expect 0)"
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed; echo "exit=$? (expect 1 — placeholders)"

# 造一個違規設定，確認會被擋
cp deploy/cloudflared/config.yml /tmp/bad.yml
python3 - <<'PY'
import pathlib
p = pathlib.Path("/tmp/bad.yml"); s = p.read_text()
s = s.replace("  - service: http_status:404",
              "  - hostname: nas.example.com\n    service: http://192.168.1.10:5001\n  - service: http_status:404")
p.write_text(s)
PY
bash scripts/verify-tunnel-config.sh /tmp/bad.yml template; echo "exit=$? (expect 1 — DSM route)"
```

期望：template 模式 0；deployed 模式 1（因為還有 placeholder）；加了 DSM route 的版本 1。

- [ ] **Step 5: Commit**

```bash
git add deploy/cloudflared/ scripts/verify-tunnel-config.sh
git commit -m "feat: cloudflared ingress config with single-route guard"
```

---

## Task 8: 本機 prod-like stack（prodcheck）

沒有這個 task，後面所有腳本都只能「寫出來但沒跑過」。prodcheck 跑的是 **production 映像 + production nginx.conf + production 的 volume 與權限設定**，只把 cloudflared 換成一個 host port、Firebase 換成 testauth，因此驗證腳本能在筆電上真的執行。

**Files:**
- Create: `deploy/compose/docker-compose.prodcheck.yml`
- Modify: `Makefile`（`prodcheck-up`、`prodcheck-down`）

**Interfaces:**
- Consumes: `photo-browser:${APP_VERSION}`（Task 6）、`deploy/nginx/nginx.prod.conf`（Task 4）、`deploy/compose/dev-fixtures.sh` 與 `dev-users.json`（Spec 3 既有）。
- Produces: `make prodcheck-up` 起一個對外 `http://localhost:8088` 的 stack（Host header 為 `photos-api.localhost`），testauth 在 `:8090`。後續 Task 9/9/10/11 都以它為驗證目標。

- [ ] **Step 1: 寫 prodcheck compose**

```yaml
# Production stack, locally runnable. Same image, same nginx.prod.conf, same
# read-only mounts and non-root user as production. Two deliberate swaps:
#   * cloudflared is replaced by a published port, since there is no tunnel here
#   * Firebase is replaced by testauth, since there is no Firebase project here
# Everything else must stay identical, or verifying against it proves nothing.
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

  photo-app:
    image: photo-browser:${APP_VERSION}
    command: ["serve"]
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
      - ${PHOTO_FIXTURES:-../../.dev-fixtures/photos}:/srv/photos:ro
      - prodcheck-data:/srv/data:rw
      - prodcheck-thumbs:/srv/thumbnails:rw
    read_only: true
    tmpfs:
      - /tmp:size=16m
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "photo-app", "healthcheck"]
      interval: 5s
      timeout: 5s
      retries: 10
      start_period: 5s
    depends_on:
      - testauth

  nginx:
    image: ${NGINX_IMAGE:-nginxinc/nginx-unprivileged:1.27-alpine}
    environment:
      PUBLIC_API_HOST: photos-api.localhost
    volumes:
      - ../../deploy/nginx/nginx.prod.conf:/etc/nginx/templates/nginx.conf.template:ro
      - ${PHOTO_FIXTURES:-../../.dev-fixtures/photos}:/srv/photos:ro
      - prodcheck-thumbs:/srv/thumbnails:ro
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
    # Stands in for the tunnel. Production publishes nothing.
    ports:
      - "8088:8080"
    depends_on:
      photo-app:
        condition: service_healthy

volumes:
  prodcheck-data:
  prodcheck-thumbs:
```

**注意**：prodcheck 沒有指定 `user:`。production 用 `${APP_UID}:${APP_GID}` 對應 NAS share 的擁有者；本機 volume 是 docker 管理的，沿用映像內建的 `65532:65532` 即可，強行指定反而會因為 volume 權限失敗。這個差異要寫在檔案註解裡。

- [ ] **Step 2: 索引與 allowlist 的一次性初始化**

prodcheck 的 photo-app 是 `serve`，不會自動索引。用 one-shot `run` 完成（這正是 production 的做法，所以值得在這裡先走一次）：

```bash
cat >> Makefile <<'MK'

.PHONY: prodcheck-up prodcheck-down

# Production image + production nginx.conf, runnable on a laptop.
prodcheck-up: build-prod
	bash deploy/compose/dev-fixtures.sh
	docker compose -f deploy/compose/docker-compose.prodcheck.yml up -d
	docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps photo-app index
	docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps photo-app admin add-user --uid=admin-1 --email=admin@example.com --role=admin
	docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps photo-app admin add-user --uid=member-1 --email=member@example.com --role=member
	@echo "prodcheck on http://localhost:8088 (Host: photos-api.localhost), testauth on :8090"

prodcheck-down:
	docker compose -f deploy/compose/docker-compose.prodcheck.yml down -v
MK
```

`APP_VERSION` 需要傳給 compose，所以在 Makefile 頂部匯出：

```makefile
export APP_VERSION
```

- [ ] **Step 3: 起 stack 並驗證 Host 過濾真的生效**

```bash
make prodcheck-up
TOKEN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")

echo "--- correct Host ---"
curl -s -o /dev/null -w "%{http_code}\n" -H "Host: photos-api.localhost" \
  -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/v1/me

echo "--- wrong Host (must be refused) ---"
curl -s -o /dev/null -w "%{http_code}\n" -H "Host: nas.local" \
  -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/v1/me
```

期望：正確 Host → `200`；錯誤 Host → curl 因為 nginx `return 444`（直接斷線）回報 `000` 或非 2xx。若錯誤 Host 也回 200，表示 `server_name` 過濾沒生效，**停下來修 Task 4 再繼續**。

- [ ] **Step 4: 驗證 read-only rootfs 沒有擋到正常運作**

```bash
docker compose -f deploy/compose/docker-compose.prodcheck.yml logs photo-app --tail=20
docker compose -f deploy/compose/docker-compose.prodcheck.yml ps
```

期望：photo-app 狀態為 `healthy`（證明 Task 2 的 healthcheck 在 read-only + cap_drop 下能跑），log 沒有 `read-only file system` 錯誤。

- [ ] **Step 5: 驗證媒體路徑完整可用**

```bash
TOKEN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")
H="-H Host:photos-api.localhost -H Authorization:Bearer\ $TOKEN"
read -r PID KEY <<<"$(curl -s -H "Host: photos-api.localhost" -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8088/api/v1/photos?limit=1" \
  | python3 -c "import sys,json;p=json.load(sys.stdin)['items'][0];print(p['id'],p['thumbnail_key'])")"
curl -s -o /dev/null -w "thumb=%{http_code} type=%{content_type}\n" -H "Host: photos-api.localhost" \
  -H "Authorization: Bearer $TOKEN" "http://localhost:8088/api/v1/photos/$PID/thumbnail/$KEY"
curl -s -o /dev/null -w "orig=%{http_code} type=%{content_type}\n" -H "Host: photos-api.localhost" \
  -H "Authorization: Bearer $TOKEN" "http://localhost:8088/api/v1/photos/$PID/original"
```

期望：`thumb=200 type=image/webp`、`orig=200 type=image/jpeg`。這證明 `X-Accel-Redirect` 在 production nginx 設定下仍然正確。

- [ ] **Step 6: Commit**

```bash
git add deploy/compose/docker-compose.prodcheck.yml Makefile
git commit -m "test: prod-like local stack for verifying the production config"
```

---

## Task 9: 部署驗證腳本（spec §10 的 functional + security checks）

Spec §10 列了 8 條 security check 與一條 functional path。這個 task 把它們變成一支可重複執行的腳本——對 prodcheck 跑可以在筆電上驗證邏輯，對真實 hostname 跑則是驗收證據。

**Files:**
- Create: `scripts/verify-deployment.sh`
- Modify: `Makefile`（`verify-deployment`）

**Interfaces:**
- Consumes: Task 8 的 prodcheck stack，或真實 `https://${PUBLIC_API_HOST}`。
- Produces: `scripts/verify-deployment.sh --base-url URL [--host HOST] --member-token T --admin-token T --denied-token T [--edge]`，每條檢查印 `ok`/`FAIL`，任一失敗 exit 1。`--edge` 額外跑只有在真實 Cloudflare 前面才成立的檢查。

- [ ] **Step 1: 寫腳本**

```bash
cat > scripts/verify-deployment.sh <<'SH'
#!/usr/bin/env bash
# Spec 4 section 10 checks, runnable against either the local prodcheck stack
# or the real deployment. Everything here is read-only except the deliberate
# disable/enable of a member, which is restored before the script exits.
set -uo pipefail

BASE_URL=""; HOST=""; MEMBER=""; ADMIN=""; DENIED=""; EDGE=0; COMPOSE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --base-url) BASE_URL="$2"; shift 2 ;;
    --host) HOST="$2"; shift 2 ;;
    --member-token) MEMBER="$2"; shift 2 ;;
    --admin-token) ADMIN="$2"; shift 2 ;;
    --denied-token) DENIED="$2"; shift 2 ;;
    --compose) COMPOSE="$2"; shift 2 ;;   # compose file, for the disable/enable check
    --edge) EDGE=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
[ -n "$BASE_URL" ] || { echo "--base-url is required" >&2; exit 2; }

fail=0
hdr=(); [ -n "$HOST" ] && hdr=(-H "Host: $HOST")
ok()   { echo "ok    $1"; }
bad()  { echo "FAIL  $1"; fail=1; }
code() { curl -s -o /dev/null -w '%{http_code}' "${hdr[@]}" "$@"; }
body() { curl -s "${hdr[@]}" "$@"; }
head_of() { curl -s -D- -o /dev/null "${hdr[@]}" "$@"; }

echo "== functional path =="

c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
[ "$c" = 200 ] && ok "/me authorizes an allowlisted member" || bad "/me returned $c for a member"

c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/timeline?limit=5")
[ "$c" = 200 ] && ok "timeline is listable" || bad "timeline returned $c"

read -r PID KEY <<<"$(body -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos?limit=1" \
  | python3 -c "import sys,json
d=json.load(sys.stdin)['items']
print(d[0]['id'], d[0].get('thumbnail_key','')) if d else print('', '')")"
if [ -z "$PID" ]; then
  bad "library is empty — index before verifying"
else
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/$KEY")
  [ "$c" = 200 ] && ok "authenticated thumbnail is served" || bad "thumbnail returned $c"
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/original")
  [ "$c" = 200 ] && ok "authenticated original is served" || bad "original returned $c"
fi

echo "== security =="

# Direct internal-media access must never work: nginx marks it `internal`, so
# only an X-Accel-Redirect subrequest can reach it.
for path in "/internal-media/originals/" "/internal-media/thumbnails/" \
            "/internal-media/originals/travel/alishan-2017/photo_00.jpg"; do
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL$path")
  [ "$c" = 404 ] && ok "direct $path is refused ($c)" || bad "direct $path returned $c, want 404"
done

# A valid Firebase identity that is not on the allowlist gets 403 everywhere.
if [ -n "$DENIED" ]; then
  for path in "/api/v1/me" "/api/v1/photos" "/api/v1/photos/$PID/original"; do
    c=$(code -H "Authorization: Bearer $DENIED" "$BASE_URL$path")
    [ "$c" = 403 ] && ok "unallowlisted identity gets 403 on $path" || bad "unallowlisted got $c on $path"
  done
fi

# No token, bad token, and admin-only routes.
c=$(code "$BASE_URL/api/v1/me");                         [ "$c" = 401 ] && ok "no token gets 401" || bad "no token got $c"
c=$(code -H "Authorization: Bearer not.a.token" "$BASE_URL/api/v1/me"); [ "$c" = 401 ] && ok "malformed token gets 401" || bad "malformed token got $c"
c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/admin/users"); [ "$c" = 403 ] && ok "member cannot reach admin routes" || bad "member got $c on admin route"

# Path traversal, in the encodings a proxy might normalise differently.
for t in "../../etc/passwd" "..%2f..%2fetc%2fpasswd" "%2e%2e/%2e%2e/etc/passwd"; do
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/$t")
  case "$c" in 200) bad "traversal '$t' returned 200" ;; *) ok "traversal '$t' refused ($c)" ;; esac
done

# A thumbnail key that no longer matches the row must not serve bytes.
c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/stale-key-that-never-existed")
[ "$c" = 404 ] && ok "stale thumbnail key refused ($c)" || bad "stale thumbnail key returned $c"

# Per-user JSON must be marked no-store; media must be private, never public.
cc=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me" | tr -d '\r' | awk -F': ' 'tolower($1)=="cache-control"{print $2}')
[ "$cc" = "no-store" ] && ok "api json is no-store" || bad "api json Cache-Control is '$cc', want no-store"
cc=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/$KEY" | tr -d '\r' | awk -F': ' 'tolower($1)=="cache-control"{print $2}')
case "$cc" in *private*) ok "thumbnail cache-control is private ($cc)" ;; *) bad "thumbnail Cache-Control is '$cc', want private" ;; esac

# A disabled member must lose access on the very next request.
if [ -n "$COMPOSE" ]; then
  docker compose -f "$COMPOSE" run --rm --no-deps photo-app admin disable --uid=member-1 >/dev/null 2>&1
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
  [ "$c" = 403 ] && ok "disabled member loses access immediately" || bad "disabled member still got $c"
  docker compose -f "$COMPOSE" run --rm --no-deps photo-app admin enable --uid=member-1 >/dev/null 2>&1
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
  [ "$c" = 200 ] && ok "re-enabled member regains access" || bad "re-enabled member got $c"
fi

if [ "$EDGE" -eq 1 ]; then
  echo "== edge (real Cloudflare only) =="

  # Protected responses must not sit in a shared cache.
  cs=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me" | tr -d '\r' | awk -F': ' 'tolower($1)=="cf-cache-status"{print $2}')
  case "${cs:-none}" in
    HIT|STALE|REVALIDATED) bad "api response came from Cloudflare's shared cache ($cs)" ;;
    *) ok "api response is not shared-cached (cf-cache-status=${cs:-absent})" ;;
  esac

  # DSM / SMB / Container Manager must not be reachable under this hostname.
  for path in "/webman/index.cgi" "/webapi/entry.cgi" "/webman/3rdparty/" "/" ; do
    out=$(body "$BASE_URL$path" | head -c 400)
    if echo "$out" | grep -qiE 'synology|diskstation|dsm|container manager'; then
      bad "$path exposes DSM content"
    else
      ok "$path exposes no DSM content"
    fi
  done
fi

[ "$fail" -eq 0 ] || { echo; echo "verify-deployment FAILED"; exit 1; }
echo; echo "verify-deployment OK"
SH
chmod +x scripts/verify-deployment.sh
```

- [ ] **Step 2: 加 Makefile target**

```makefile
PRODCHECK := deploy/compose/docker-compose.prodcheck.yml

verify-deployment:
	@MEMBER=$$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1"); \
	 ADMIN=$$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1"); \
	 DENIED=$$(curl -s "http://localhost:8090/mint?sub=denied-1&email=denied@example.com&verified=1"); \
	 bash scripts/verify-deployment.sh --base-url http://localhost:8088 --host photos-api.localhost \
	   --member-token "$$MEMBER" --admin-token "$$ADMIN" --denied-token "$$DENIED" \
	   --compose $(PRODCHECK)
```

- [ ] **Step 3: 對 prodcheck 執行**

```bash
make prodcheck-up
make verify-deployment
echo "exit=$?"
```

期望：所有行 `ok`，最後 `verify-deployment OK`，exit 0。若 `direct /internal-media/...` 回的不是 404，**立刻停下**——這是整個安全模型的核心。

- [ ] **Step 4: 驗證檢查會擋（red check）**

把 nginx 的 `internal;` 拿掉，確認腳本真的抓得到：

```bash
python3 - <<'PY'
import pathlib
p = pathlib.Path("deploy/nginx/nginx.prod.conf"); s = p.read_text()
p.write_text(s.replace("      internal;\n      autoindex off;\n      alias /srv/thumbnails/;",
                       "      autoindex off;\n      alias /srv/thumbnails/;", 1))
PY
docker compose -f deploy/compose/docker-compose.prodcheck.yml up -d --force-recreate nginx
sleep 3
make verify-deployment; echo "exit=$? (expect 1)"
git checkout deploy/nginx/nginx.prod.conf
docker compose -f deploy/compose/docker-compose.prodcheck.yml up -d --force-recreate nginx
sleep 3
make verify-deployment >/dev/null && echo "restored, back to OK"
```

期望：拿掉 `internal;` 後腳本 FAIL 並指出 `direct /internal-media/thumbnails/ returned ...`；還原後回到 OK。**沒有做過這一步，就不能宣稱這支腳本在保護任何東西。**

- [ ] **Step 5: Commit**

```bash
git add scripts/verify-deployment.sh Makefile
git commit -m "test: deployment verification covering spec 4 security checks"
```

---

## Task 10: SQLite 備份腳本

Spec §9：每日 online backup、不直接複製 live `.db`、備份 deployment config、thumbnails 不備份。

**Files:**
- Create: `scripts/backup-sqlite.sh`

**Interfaces:**
- Consumes: `photo-app backup --out=`（Task 2）；production 或 prodcheck compose 檔。
- Produces: `scripts/backup-sqlite.sh --compose FILE --dest DIR [--keep N]`，產出 `photo-<UTC-date>.db` 與 `config-<UTC-date>.tar.gz`，保留最近 N 份（預設 7，滿足 24 小時 RPO 且留一週餘裕）。

- [ ] **Step 1: 寫腳本**

```bash
cat > scripts/backup-sqlite.sh <<'SH'
#!/usr/bin/env bash
# Daily backup: an online SQLite snapshot plus the deployment config needed to
# rebuild the stack. Thumbnails are derived data and are deliberately not
# backed up — `photo-app index --rebuild-thumbnails` regenerates them.
set -euo pipefail

COMPOSE=""; DEST=""; KEEP=7
while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --dest) DEST="$2"; shift 2 ;;
    --keep) KEEP="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
[ -n "$COMPOSE" ] && [ -n "$DEST" ] || { echo "usage: backup-sqlite.sh --compose FILE --dest DIR [--keep N]" >&2; exit 2; }

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$DEST"

# The snapshot is written inside the container (to the data volume) and then
# copied out, because only the container can see /srv/data.
IN_CONTAINER="/srv/data/backup/photo-$STAMP.db"
echo "[backup] snapshotting to $IN_CONTAINER"
docker compose -f "$COMPOSE" run --rm --no-deps photo-app backup --out="$IN_CONTAINER"

CID="$(docker compose -f "$COMPOSE" run -d --no-deps --entrypoint sleep photo-app 30)"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT
docker cp "$CID:$IN_CONTAINER" "$DEST/photo-$STAMP.db"
docker exec "$CID" rm -f "$IN_CONTAINER"

# Verify the copy is a real database before trusting it. A backup nobody has
# opened is a backup nobody knows is broken.
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$DEST/photo-$STAMP.db" "PRAGMA integrity_check;" | grep -qx ok \
    && echo "[backup] integrity_check ok" \
    || { echo "[backup] integrity_check FAILED" >&2; exit 1; }
else
  docker run --rm -v "$(cd "$DEST" && pwd)":/b photo-browser-api-acceptance \
    sqlite3 "/b/photo-$STAMP.db" "PRAGMA integrity_check;" | grep -qx ok \
    && echo "[backup] integrity_check ok" \
    || { echo "[backup] integrity_check FAILED" >&2; exit 1; }
fi

# Deployment config travels with the data; a restore needs both.
tar -czf "$DEST/config-$STAMP.tar.gz" \
  deploy/compose/docker-compose.prod.yml \
  deploy/nginx/nginx.prod.conf \
  deploy/cloudflared/config.yml \
  deploy/VERSION \
  docs/deploy/
echo "[backup] wrote config-$STAMP.tar.gz"

# Retention. Credentials are NOT in these archives — the tunnel credential is
# covered by the existing secret backup process (spec section 9).
for pattern in "photo-*.db" "config-*.tar.gz"; do
  # shellcheck disable=SC2012
  ls -1t "$DEST"/$pattern 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do
    echo "[backup] pruning $old"
    rm -f "$old"
  done
done

echo "[backup] done: $DEST/photo-$STAMP.db"
SH
chmod +x scripts/backup-sqlite.sh
```

**注意**：DSM 上沒有 `sqlite3` 執行檔，所以 integrity check 會走到 `else` 分支，用 `photo-browser-api-acceptance` 映像（它裝了 sqlite3）跑。這表示 NAS 上必須先 `make api-acceptance` 或 `docker build --target api-acceptance -t photo-browser-api-acceptance .` 一次；Task 15 的 runbook 會提醒。

- [ ] **Step 2: 對 prodcheck 執行**

```bash
make prodcheck-up
bash scripts/backup-sqlite.sh --compose deploy/compose/docker-compose.prodcheck.yml --dest /tmp/pb-backups --keep 3
echo "exit=$?"
ls -la /tmp/pb-backups
```

期望：exit 0；`/tmp/pb-backups` 有 `photo-*.db` 與 `config-*.tar.gz`；log 出現 `integrity_check ok`。

- [ ] **Step 3: 驗證備份內容真的含 allowlist**

```bash
docker run --rm -v /tmp/pb-backups:/b photo-browser-api-acceptance \
  sqlite3 "/b/$(ls -1t /tmp/pb-backups/photo-*.db | head -1 | xargs basename)" \
  "SELECT firebase_uid, role, enabled FROM users ORDER BY id;"
```

期望：列出 `admin-1|admin|1` 與 `member-1|member|1`。

- [ ] **Step 4: 驗證 retention 真的會刪**

```bash
for i in 1 2 3 4; do
  bash scripts/backup-sqlite.sh --compose deploy/compose/docker-compose.prodcheck.yml --dest /tmp/pb-backups --keep 2 >/dev/null
  sleep 1
done
ls -1 /tmp/pb-backups/photo-*.db | wc -l
```

期望：印出 `2`。

- [ ] **Step 5: Commit**

```bash
git add scripts/backup-sqlite.sh
git commit -m "feat: daily sqlite online backup with retention and integrity check"
```

---

## Task 11: 復原演練腳本

Spec §9 的 restore drill 有 7 個步驟，且明文要求「不依賴 thumbnail backup 也能成功」。這支腳本把整套演練自動化，跑一次就是一次證據。

**Files:**
- Create: `scripts/restore-drill.sh`
- Modify: `Makefile`（`restore-drill`）

**Interfaces:**
- Consumes: Task 10 的備份產物、Task 8 的 prodcheck stack、`photo-app rebuild` 與 `photo-app index --rebuild-thumbnails`。
- Produces: `make restore-drill` 一鍵完成備份 → 毀滅 → 復原 → 驗證，並印出每一步的結果。

- [ ] **Step 1: 寫腳本**

```bash
cat > scripts/restore-drill.sh <<'SH'
#!/usr/bin/env bash
# Walks spec 4 section 9's restore drill end to end against the prodcheck
# stack. Deliberately destroys the data and thumbnail volumes, so it must only
# ever run against prodcheck — never against production.
set -euo pipefail

COMPOSE="deploy/compose/docker-compose.prodcheck.yml"
DEST="${1:-/tmp/pb-restore-drill}"
step() { echo; echo "=== $* ==="; }
dcr() { docker compose -f "$COMPOSE" run --rm --no-deps photo-app "$@"; }

rm -rf "$DEST"; mkdir -p "$DEST"

step "1. deploy the pinned version and seed state"
make prodcheck-up >/dev/null
dcr admin add-user --uid=restore-probe --email=probe@example.com --role=member >/dev/null 2>&1 || true
before_users="$(dcr admin list-users | sort)"
echo "$before_users"

step "2. back up (sqlite online backup + config)"
bash scripts/backup-sqlite.sh --compose "$COMPOSE" --dest "$DEST" --keep 5
BACKUP="$(ls -1t "$DEST"/photo-*.db | head -1)"
echo "backup: $BACKUP"

step "3. destroy the data and thumbnail volumes"
docker compose -f "$COMPOSE" down -v >/dev/null
docker compose -f "$COMPOSE" up -d >/dev/null
sleep 3
if dcr admin list-users | grep -q restore-probe; then
  echo "FAIL: volumes were not actually destroyed"; exit 1
fi
echo "confirmed: allowlist and thumbnails are gone"

step "4. restore the SQLite backup (allowlist comes back)"
CID="$(docker compose -f "$COMPOSE" run -d --no-deps --entrypoint sleep photo-app 60)"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT
docker compose -f "$COMPOSE" stop photo-app >/dev/null
docker cp "$BACKUP" "$CID:/srv/data/photo.db"
docker rm -f "$CID" >/dev/null; trap - EXIT
docker compose -f "$COMPOSE" up -d >/dev/null
sleep 3
after_users="$(dcr admin list-users | sort)"
if [ "$after_users" != "$before_users" ]; then
  echo "FAIL: allowlist differs after restore"; echo "before: $before_users"; echo "after:  $after_users"; exit 1
fi
echo "allowlist restored identically"

step "5. rebuild derived catalogue rows from the filesystem"
dcr rebuild
dcr index

step "6. rebuild thumbnails (never restored from backup)"
dcr index --rebuild-thumbnails

step "7. verify all three identities and the media path"
MEMBER=$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1")
ADMIN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")
DENIED=$(curl -s "http://localhost:8090/mint?sub=denied-1&email=denied@example.com&verified=1")
bash scripts/verify-deployment.sh --base-url http://localhost:8088 --host photos-api.localhost \
  --member-token "$MEMBER" --admin-token "$ADMIN" --denied-token "$DENIED" --compose "$COMPOSE"

echo
echo "restore drill PASSED — allowlist restored from backup, catalogue and"
echo "thumbnails rebuilt from the filesystem, all identities behave correctly."
SH
chmod +x scripts/restore-drill.sh
```

- [ ] **Step 2: 加 Makefile target**

```makefile
restore-drill:
	bash scripts/restore-drill.sh
```

- [ ] **Step 3: 執行並確認全程通過**

```bash
make restore-drill 2>&1 | tail -40
echo "exit=$?"
```

期望：七個 `===` 段落依序出現，第 3 步確認資料真的被毀掉，第 4 步 allowlist 逐字相同，最後 `verify-deployment OK` + `restore drill PASSED`，exit 0。

- [ ] **Step 4: 驗證「失敗的 scan 不刪既有 catalogue」**

Spec §10 recovery checks 有這一條，用一個不存在的 photo root 觸發失敗：

```bash
before=$(docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps photo-app index | tail -1)
echo "before: $before"
docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps \
  -e PHOTO_ROOT=/srv/does-not-exist photo-app index; echo "failed-scan exit=$? (expect non-zero)"
TOKEN=$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1")
curl -s -H "Host: photos-api.localhost" -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8088/api/v1/photos?limit=1" | python3 -c "import sys,json;print('items after failed scan:', len(json.load(sys.stdin)['items']))"
```

期望：失敗的 scan 以非 0 結束，但 `items after failed scan: 1`——既有 catalogue 沒有被清空。若被清空了，這是 backend bug，記錄下來並停止。

- [ ] **Step 5: 收拾**

```bash
make prodcheck-down
```

- [ ] **Step 6: Commit**

```bash
git add scripts/restore-drill.sh Makefile
git commit -m "test: automated restore drill covering spec 4 recovery checks"
```

---

## Task 12: 資源量測腳本

Spec §8 與 §11 都要求「在 target NAS 記錄 idle 與 scan-time CPU/memory」，並據此設定 CPU/memory limit——而且 limit 必須寬到讓一張代表性最大圖片能完成 decode。這個 task 產出量測工具；**數字要在 NAS 上跑才算數**（Task 15 runbook 會要求）。

**Files:**
- Create: `scripts/measure-resources.sh`
- Create: `docs/deploy/resource-measurements.md`（樣板）
- Modify: `Makefile`（`measure`）

**Interfaces:**
- Consumes: 任一 compose 檔（prodcheck 或 prod）。
- Produces: `scripts/measure-resources.sh --compose FILE [--out FILE] [--idle-seconds N]`，輸出 idle 與 scan 期間每個 service 的 peak / mean CPU% 與 peak RSS。

- [ ] **Step 1: 寫腳本**

```bash
cat > scripts/measure-resources.sh <<'SH'
#!/usr/bin/env bash
# Samples container CPU and memory while idle and while indexing, and reports
# the peaks. On a 2 GB machine the peak RSS during a scan is the number that
# decides the memory limits — it must leave DSM room to breathe.
set -euo pipefail

COMPOSE="deploy/compose/docker-compose.prodcheck.yml"
OUT="docs/deploy/resource-measurements.md"
IDLE=60
while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --idle-seconds) IDLE="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# docker stats --no-stream is a point sample; loop it to build a series.
sample_for() {
  local seconds="$1" file="$2" i=0
  : > "$file"
  while [ "$i" -lt "$seconds" ]; do
    docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' >> "$file" 2>/dev/null || true
    i=$((i + 1))
  done
}

summarize() {
  python3 - "$1" "$2" <<'PY'
import re, sys
from collections import defaultdict

path, label = sys.argv[1], sys.argv[2]
cpu, mem = defaultdict(list), defaultdict(list)

def to_mib(text):
    m = re.match(r'([\d.]+)\s*([KMG]i?B)', text.strip(), re.I)
    if not m:
        return 0.0
    v, unit = float(m.group(1)), m.group(2).upper().rstrip('B').rstrip('I')
    return {'K': v / 1024, 'M': v, 'G': v * 1024}.get(unit, v)

for line in open(path):
    parts = line.rstrip('\n').split('\t')
    if len(parts) != 3:
        continue
    name, c, m = parts
    cpu[name].append(float(c.rstrip('%') or 0))
    mem[name].append(to_mib(m.split('/')[0]))

print(f'### {label}')
print()
print('| service | samples | mean CPU% | peak CPU% | peak RSS (MiB) |')
print('|---|---|---|---|---|')
for name in sorted(cpu):
    c, m = cpu[name], mem[name]
    print(f'| `{name}` | {len(c)} | {sum(c)/len(c):.1f} | {max(c):.1f} | {max(m):.0f} |')
print()
total_peak = sum(max(v) for v in mem.values()) if mem else 0
print(f'Summed peak RSS across services: **{total_peak:.0f} MiB** '
      f'(the NAS has 2048 MiB total; DSM itself needs several hundred).')
print()
PY
}

echo "[measure] idle sampling for ${IDLE}s"
sample_for "$IDLE" "$TMP/idle.tsv"

echo "[measure] sampling during an index run"
( docker compose -f "$COMPOSE" run --rm --no-deps photo-app index >"$TMP/index.log" 2>&1 ) &
INDEX_PID=$!
: > "$TMP/scan.tsv"
while kill -0 "$INDEX_PID" 2>/dev/null; do
  docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' >> "$TMP/scan.tsv" 2>/dev/null || true
done
wait "$INDEX_PID" || true

{
  echo "# Resource measurements"
  echo
  echo "Host: \`$(hostname)\`  "
  echo "Compose file: \`$COMPOSE\`  "
  echo "Generated: $(date -Iseconds)"
  echo
  echo '> Numbers taken anywhere other than the DS716+II are indicative only.'
  echo '> The acceptance record requires a run on the NAS itself.'
  echo
  summarize "$TMP/idle.tsv" "Idle (${IDLE} samples)"
  summarize "$TMP/scan.tsv" "During \`photo-app index\`"
  echo "### Index run output"
  echo
  echo '```'
  tail -5 "$TMP/index.log"
  echo '```'
} > "$OUT"

echo "[measure] wrote $OUT"
SH
chmod +x scripts/measure-resources.sh
```

- [ ] **Step 2: 對 prodcheck 執行**

```bash
make prodcheck-up
bash scripts/measure-resources.sh --compose deploy/compose/docker-compose.prodcheck.yml \
  --out /tmp/resource-measurements.md --idle-seconds 20
cat /tmp/resource-measurements.md
```

期望：產出含兩張表（Idle 與 During index），每個 service 都有 peak RSS 數字，且「Summed peak RSS」行有值。本機數字不代表 NAS，但腳本必須真的跑得出表格。

- [ ] **Step 3: 建樣板供 NAS 實測填入**

```bash
cat > docs/deploy/resource-measurements.md <<'MD'
# Resource measurements

> 尚未在目標 NAS 上執行。

在 NAS 上執行（離峰時段，因為第二段會跑一次完整索引）：

```
bash scripts/measure-resources.sh \
  --compose deploy/compose/docker-compose.prod.yml \
  --out docs/deploy/resource-measurements.md \
  --idle-seconds 120
```

跑完後：

1. 把產生的內容 commit 進來。
2. 依 "During index" 的 peak RSS 調整 `deploy/compose/.env.prod` 的
   `APP_MEM_LIMIT`（建議取 peak 的 1.5 倍，但總和必須明顯低於 2048 MiB）。
3. 用最大的一張代表性照片單獨驗證 decode 不會 OOM：
   `docker compose -f deploy/compose/docker-compose.prod.yml run --rm --no-deps photo-app index --rebuild-thumbnails`
   若被 OOM kill（exit 137），**調高 limit 而不是降低圖片品質**。
4. 在 `docs/deploy/03-acceptance.md` 勾掉對應項目。
MD
```

- [ ] **Step 4: 加 Makefile target 並 commit**

```makefile
measure:
	bash scripts/measure-resources.sh --compose $(PRODCHECK) --out docs/deploy/resource-measurements.md
```

```bash
git add scripts/measure-resources.sh docs/deploy/resource-measurements.md Makefile
git commit -m "feat: container resource measurement for nas capacity tuning"
```

---

## Task 13: 索引排程與 Pages production build

兩個都是「把既有能力接到 production 環境」的小工作，共用一個 commit 不划算，但各自也不足以成為獨立 task，因此合併為一個 task、兩個 deliverable，各自可獨立驗證。

**Files:**
- Create: `scripts/nas-index.sh`
- Create: `web/.env.production.example`
- Modify: `Makefile`（`build-web-prod`）

**Interfaces:**
- Consumes: `photo-app index`（exit 3 = lock busy）、Spec 3 的 `npm run build` 與 `npm run guard`。
- Produces: DSM Task Scheduler 可直接貼上的腳本；Pages build 用的環境變數樣板與 `make build-web-prod`。

- [ ] **Step 1: 寫索引排程腳本**

```bash
cat > scripts/nas-index.sh <<'SH'
#!/usr/bin/env bash
# Hourly incremental index, for DSM Task Scheduler.
#
# Paste this into Control Panel > Task Scheduler > Create > Scheduled Task >
# User-defined script, running as root, every hour:
#
#   bash /volume1/docker/photo-browser/repo/scripts/nas-index.sh
#
# Exit 3 from photo-app means another index (or an admin command) holds the
# lock. That is the specified "already running" answer, not a failure, so this
# script reports it and exits 0 — otherwise DSM would email an alert every hour
# during a long initial scan.
set -uo pipefail

REPO="${REPO:-/volume1/docker/photo-browser/repo}"
COMPOSE="${COMPOSE:-deploy/compose/docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-deploy/compose/.env.prod}"
LOG="${LOG:-/volume1/docker/photo-browser/data/index.log}"

while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --env-file) ENV_FILE="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

cd "$REPO" || { echo "repo not found: $REPO" >&2; exit 1; }
mkdir -p "$(dirname "$LOG")"

# Redirect the whole script rather than a { } block: a block's assignments are
# fine in bash, but routing output this way keeps rc in the current shell with
# no subshell subtleties to get wrong.
exec >> "$LOG" 2>&1

echo "=== $(date -Iseconds) index start ==="
compose_args=(-f "$COMPOSE")
[ -f "$ENV_FILE" ] && compose_args+=(--env-file "$ENV_FILE")
docker compose "${compose_args[@]}" run --rm --no-deps photo-app index
rc=$?
case "$rc" in
  0) echo "=== index ok ===" ;;
  3) echo "=== index skipped: another run holds the lock ===" ;;
  *) echo "=== index FAILED (exit $rc) ===" ;;
esac
echo

# Keep the log bounded; DSM does not rotate script output for us.
if [ -f "$LOG" ] && [ "$(wc -c < "$LOG")" -gt 5242880 ]; then
  tail -c 2097152 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi

# 3 is the specified "already running" answer, not a failure.
[ "$rc" = 3 ] && exit 0
exit "$rc"
SH
chmod +x scripts/nas-index.sh
```

**注意**：腳本用 `exec >> "$LOG"` 而不是 `{ ... } >> "$LOG"`，因為後者在部分 shell 下是子 shell，`rc` 傳不回來——而這支腳本的整個重點就是正確區分 exit 3 與真正的失敗。`--compose` 參數讓 Step 2 能直接對 prodcheck 測試，不必改腳本。

- [ ] **Step 2: 驗證 lock-busy 真的被當成成功**

用 prodcheck 造出「鎖被佔住」的情況：

```bash
make prodcheck-up
# 佔住 index.lock：開一個長時間持鎖的 admin 指令
docker compose -f deploy/compose/docker-compose.prodcheck.yml run -d --no-deps \
  --entrypoint sh photo-app -c 'photo-app admin list-users; sleep 20' >/dev/null
sleep 2
docker compose -f deploy/compose/docker-compose.prodcheck.yml run --rm --no-deps photo-app index; echo "rc=$? (expect 3)"
```

期望：看到 `photo-app is already running` 與 `rc=3`。接著跑腳本本身，確認它把 exit 3 視為正常：

```bash
REPO=$PWD LOG=/tmp/index.log bash scripts/nas-index.sh \
  --compose deploy/compose/docker-compose.prodcheck.yml --env-file /dev/null
echo "script exit=$? (expect 0)"
grep -c "index skipped" /tmp/index.log
```

期望：`script exit=0`，且 log 內 `index skipped` 出現 1 次。

- [ ] **Step 3: 驗證 log 會被截斷**

```bash
head -c 6000000 /dev/zero | tr '\0' 'x' > /tmp/index.log
REPO=$PWD LOG=/tmp/index.log bash scripts/nas-index.sh \
  --compose deploy/compose/docker-compose.prodcheck.yml --env-file /dev/null >/dev/null 2>&1
ls -la /tmp/index.log
```

期望：檔案小於 3 MB——證明截斷邏輯生效（spec §8 要求 log bounded）。

- [ ] **Step 4: 寫 Pages build 樣板**

```bash
cat > web/.env.production.example <<'ENV'
# Cloudflare Pages build environment. Set these in the Pages project settings
# (Settings > Environment variables > Production), not in a committed file.
#
# VITE_AUTH_MODE=firebase is what makes rollup drop the testauth adapter; the
# bundle guard fails the build if any testauth code survives.
VITE_AUTH_MODE=firebase
VITE_API_BASE_URL=https://photos-api.example.com/api/v1

# From Firebase console > Project settings > Your apps > Web app.
# These four are public by design — they identify the project, they do not
# authorize anything. Never put a service account key here.
VITE_FIREBASE_API_KEY=
VITE_FIREBASE_AUTH_DOMAIN=
VITE_FIREBASE_PROJECT_ID=
VITE_FIREBASE_APP_ID=

# Must be empty in production: testauth does not exist there.
VITE_TESTAUTH_URL=
VITE_DEV_USERS=
ENV
```

- [ ] **Step 5: 加 `build-web-prod` target**

```makefile
# Production frontend build. Refuses to produce a bundle that still carries
# testauth code, and refuses to build without a real API base URL.
build-web-prod:
	@test -n "$$VITE_API_BASE_URL" || { echo "VITE_API_BASE_URL must be set"; exit 1; }
	@test -n "$$VITE_FIREBASE_API_KEY" || { echo "VITE_FIREBASE_API_KEY must be set"; exit 1; }
	cd web && VITE_AUTH_MODE=firebase npm ci --prefer-offline --no-audit && npm run build && npm run guard
```

- [ ] **Step 6: 驗證 production build 與 guard**

```bash
VITE_API_BASE_URL=https://photos-api.example.com/api/v1 \
VITE_FIREBASE_API_KEY=stub VITE_FIREBASE_AUTH_DOMAIN=stub \
VITE_FIREBASE_PROJECT_ID=stub VITE_FIREBASE_APP_ID=stub \
make build-web-prod
echo "exit=$?"
grep -r "photos-api.example.com" web/dist/assets/*.js | head -1
```

期望：exit 0、`Bundle guard OK`，且 API base URL 真的被 inline 進 bundle（證明環境變數有生效，而不是 build 了一份預設值）。

- [ ] **Step 7: 驗證缺變數會擋**

```bash
make build-web-prod; echo "exit=$? (expect 1)"
```

期望：exit 1 並印出 `VITE_API_BASE_URL must be set`。

- [ ] **Step 8: Commit**

```bash
git add scripts/nas-index.sh web/.env.production.example Makefile
git commit -m "feat: nas index scheduling script and pages production build"
```

---

## Task 14: Runbook A — 網域、Cloudflare、Firebase

從這裡開始是 **Track B**：需要人在瀏覽器裡點的步驟。Task 14 的 deliverable 是**文件**，不是設定本身——文件寫完就算完成；真正去點的時機由使用者決定。每一步都要有「怎麼知道成功了」。

**Files:**
- Create: `docs/deploy/01-external-services.md`

**Interfaces:**
- Consumes: `deploy/cloudflared/config.yml` 的 placeholder 名稱、`web/.env.production.example` 的變數名。
- Produces: 一份填得完的表單，結束時使用者手上會有：`PUBLIC_APP_HOST`、`PUBLIC_API_HOST`、tunnel id、credential JSON、Firebase 四個 `VITE_FIREBASE_*` 值與 `FIREBASE_PROJECT_ID`。

- [ ] **Step 1: 寫文件骨架與「你會得到什麼」**

```markdown
# 01 — 外部服務設定（網域 / Cloudflare / Firebase）

**這份文件要人工執行。** 做完之後，你手上會有下面這張表的所有值；
Task 15 的 NAS 部署會逐一用到。

| 值 | 填在哪裡 | 你的值 |
|---|---|---|
| `PUBLIC_APP_HOST` | `.env.prod`、Firebase authorized domain | |
| `PUBLIC_API_HOST` | `.env.prod`、`cloudflared/config.yml`、Pages 的 `VITE_API_BASE_URL` | |
| Tunnel ID | `cloudflared/config.yml` 兩處 | |
| Tunnel credential 檔名 | `<tunnel-id>.json`，放 NAS | |
| `FIREBASE_PROJECT_ID` | `.env.prod` | |
| `VITE_FIREBASE_API_KEY` | Pages 環境變數 | |
| `VITE_FIREBASE_AUTH_DOMAIN` | Pages 環境變數 | |
| `VITE_FIREBASE_PROJECT_ID` | Pages 環境變數 | |
| `VITE_FIREBASE_APP_ID` | Pages 環境變數 | |

時間預估：40–60 分鐘，其中網域 DNS 生效可能要等。
```

- [ ] **Step 2: 寫「1. 網域與 Cloudflare zone」**

```markdown
## 1. 取得網域並接上 Cloudflare

1. 註冊一個網域。可以用 Cloudflare Registrar 直接買（少一次 nameserver 搬遷），
   或在任何註冊商買再搬過來。
2. Cloudflare dashboard → **Add a site** → 輸入網域 → 選 **Free** 方案。
3. Cloudflare 會給你兩個 nameserver。到註冊商把 nameserver 改成這兩個。
4. **怎麼知道成功了：** zone 的狀態從 `Pending Nameserver Update` 變成 **Active**
   （可能要幾分鐘到幾小時）。用指令確認：

   ```
   dig +short NS <your-domain>
   ```

   輸出應該是兩個 `*.ns.cloudflare.com`。

5. 決定兩個子網域並填進上面的表：
   - `PUBLIC_APP_HOST` — 前端，例如 `photos.<your-domain>`
   - `PUBLIC_API_HOST` — API 與圖片，例如 `photos-api.<your-domain>`

   **兩者必須不同**。前端是靜態檔案走 Pages，API 走 Tunnel 進 NAS；
   混在同一個 hostname 會讓 Cloudflare 的快取規則難以分離，而 spec 明令
   `/api/` 與 protected media 不得進共用快取。
```

- [ ] **Step 3: 寫「2. 建立 Tunnel」**

```markdown
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

   輸出會包含 tunnel id（UUID）與 credential 檔路徑
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

   前者列出 `photo-browser` 與其 id；後者回 Cloudflare 的 IP（100.x 或 104.x），
   **不是**你家的對外 IP。如果回的是你家 IP，表示 DNS record 建錯了——
   刪掉重來，不要繼續。

6. 把 credential 檔案安全地搬到 NAS（Task 15 會用），**不要**放進 git、
   不要用 email 或聊天軟體傳。建議用 `scp` 直接送到 NAS。

7. 填 `deploy/cloudflared/config.yml` 的三個 placeholder：
   `REPLACE_WITH_TUNNEL_ID`（兩處）與 `REPLACE_WITH_PUBLIC_API_HOST`（兩處）。
   填完在 repo 根目錄跑：

   ```
   bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed
   ```

   必須印出 `verify-tunnel-config OK`。這支腳本會擋下沒換掉的 placeholder，
   以及任何指向 DSM/SMB/SSH 的 ingress rule。
```

- [ ] **Step 4: 寫「3. Cloudflare Pages」**

```markdown
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
   | Node version | `20`（Environment variables 加 `NODE_VERSION=20`） |

   `npm run guard` 放進 build command 是刻意的：它會在 bundle 含有 testauth
   程式碼時讓 **build 失敗**，而不是默默部署出去。

3. Environment variables（Production）照 `web/.env.production.example` 填。
   `VITE_TESTAUTH_URL` 與 `VITE_DEV_USERS` **留空**。
4. 部署完成後，Pages → Custom domains → 加上 `PUBLIC_APP_HOST`。
5. **怎麼知道成功了：**

   ```
   curl -sI https://<PUBLIC_APP_HOST> | head -3
   curl -s https://<PUBLIC_APP_HOST> | grep -o '<title>[^<]*</title>'
   ```

   回 `200` 且 title 是 `Photo Browser`。此時開網頁會停在 login 畫面並且
   **Google 登入會失敗**——因為 Firebase 還沒設定 authorized domain，下一節處理。

## 4. 快取規則（重要）

Cloudflare 預設不會快取 `/api/` 這類無副檔名的回應，但 spec 要求明確保證。

1. dashboard → 你的 zone → **Caching** → **Cache Rules** → **Create rule**
2. 規則：`Hostname equals <PUBLIC_API_HOST>` → **Bypass cache**
3. **怎麼知道成功了：** Task 15 驗收時跑
   `bash scripts/verify-deployment.sh ... --edge`，其中的
   `cf-cache-status` 檢查會確認 API 回應沒有來自共用快取。
```

- [ ] **Step 5: 寫「5. Firebase」**

```markdown
## 5. Firebase 專案

1. [console.firebase.google.com](https://console.firebase.google.com) → **Add project**。
   不需要 Google Analytics。
2. **Build → Authentication → Get started → Sign-in method → Google → Enable。**
   只開這一個 provider（spec：POC 只啟用一個已決定的 provider）。
   前端用的是 `signInWithPopup(GoogleAuthProvider)`，所以必須是 Google。
3. **Authentication → Settings → Authorized domains**：
   - 加入 `<PUBLIC_APP_HOST>`
   - 移除 `localhost` 以外你不需要的項目；**不要**加 `PUBLIC_API_HOST`
     （API 不做瀏覽器登入）。
4. **Project settings → General → Your apps → Add app → Web**：
   取得 `apiKey` / `authDomain` / `projectId` / `appId` 四個值，填進上面的表，
   再貼進 Pages 的環境變數，重新 deploy 一次 Pages。
5. **Project settings → General → Project ID**：這個值就是 `FIREBASE_PROJECT_ID`，
   backend 會用它推導 issuer `https://securetoken.google.com/<project-id>`。
6. 準備三個測試身分（用你自己的 Google 帳號家族即可）：
   - 一個會成為 **admin**
   - 一個會成為 **member**
   - 一個**刻意不加進 allowlist**，用來驗 403

   三個都先在前端登入一次（會被擋在 `/forbidden`，這是正常的），
   然後到 **Authentication → Users** 抄下三個 **User UID**——Task 15 要用。
7. **怎麼知道成功了：** 開 `https://<PUBLIC_APP_HOST>`，按 Sign in with Google，
   popup 能完成登入且畫面落在 **Access denied**（`/forbidden`）。
   這代表 Firebase 認證成功、但 allowlist 還沒有你——正是此刻該有的狀態。
   如果 popup 報 `auth/unauthorized-domain`，回到步驟 3。
```

- [ ] **Step 6: 驗證文件可用性**

這是文件 task，驗證方式是**逐步走一遍**並確認沒有卡點：

```bash
# 文件裡引用到的腳本與樣板都必須存在
for f in scripts/verify-tunnel-config.sh web/.env.production.example \
         deploy/cloudflared/config.yml scripts/verify-deployment.sh; do
  test -f "$f" && echo "ok   $f" || echo "MISSING $f"
done
# 文件裡提到的 placeholder 名稱必須與設定檔一致
grep -o 'REPLACE_WITH_[A-Z_]*' deploy/cloudflared/config.yml | sort -u
grep -o 'REPLACE_WITH_[A-Z_]*' docs/deploy/01-external-services.md | sort -u
```

期望：四個檔案都 `ok`；兩份 placeholder 清單**完全一致**。不一致表示文件會叫人去改一個不存在的欄位。

- [ ] **Step 7: Commit**

```bash
git add docs/deploy/01-external-services.md
git commit -m "docs: runbook for domain, cloudflare tunnel, pages and firebase"
```

---

## Task 15: Runbook B — NAS 部署與驗收

**Files:**
- Create: `docs/deploy/02-nas-deployment.md`
- Create: `docs/deploy/03-acceptance.md`

**Interfaces:**
- Consumes: Task 14 產出的所有值；Task 1–13 的所有腳本與設定。
- Produces: 一條從空白 NAS 到通過 spec §11 驗收的路徑，以及一份可簽收的清單。

- [ ] **Step 1: 寫 `02-nas-deployment.md` 的前置與 share 設定**

```markdown
# 02 — NAS 部署

**前置：** `01-external-services.md` 全部完成，且 `docs/deploy/preflight-report.md`
已在這台 NAS 上跑過且沒有 BLOCKING 項目。

## 1. DSM 準備

1. **Control Panel → Terminal & SNMP → Enable SSH service**（部署完可關掉）。
2. **Control Panel → Regional Options → Time**：
   - Time zone 設好
   - **Synchronize with NTP server** 打開，選 `pool.ntp.org`

   這不是可選項。Firebase token 的 `exp`/`iat` 驗證依賴正確時間，
   NAS 時鐘偏移超過幾分鐘會讓所有登入失敗，而且錯誤訊息看起來像
   「token 無效」，很難聯想到時鐘。

   確認：`ssh` 進去跑 `date -u`，與你筆電的 `date -u` 相差不超過數秒。

3. **Control Panel → Shared Folder**：確認照片 share（例如 `photos`）存在。
4. 建立應用目錄：

   ```
   sudo mkdir -p /volume1/docker/photo-browser/{data,thumbnails,repo}
   sudo mkdir -p /volume1/docker/photo-browser/data/cloudflared
   ```

5. 決定服務身分。用一個**只有照片 share 唯讀權限**的 DSM 使用者：

   ```
   id <service-user>
   ```

   把 uid/gid 填進 `.env.prod` 的 `APP_UID` / `APP_GID`。

   ```
   sudo chown -R <uid>:<gid> /volume1/docker/photo-browser/{data,thumbnails}
   sudo chmod 750 /volume1/docker/photo-browser/data
   ```

   照片目錄**不要**改擁有者——它以 `:ro` 掛載，服務只需要讀取權限。
```

- [ ] **Step 2: 寫「2. 取得 repo 與設定」**

```markdown
## 2. 取得 repo 與填設定

```
cd /volume1/docker/photo-browser/repo
git clone <repo-url> .
cp deploy/compose/.env.prod.example deploy/compose/.env.prod
vi deploy/compose/.env.prod     # 填入 Task 14 表格裡的值 + 上一步的 uid/gid
```

安裝 tunnel credential（檔案從筆電 scp 過來）：

```
sudo mv <tunnel-id>.json /volume1/docker/photo-browser/data/cloudflared/
sudo chown <uid>:<gid> /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
sudo chmod 0400        /volume1/docker/photo-browser/data/cloudflared/<tunnel-id>.json
```

確認設定沒有漏填：

```
docker compose -f deploy/compose/docker-compose.prod.yml \
  --env-file deploy/compose/.env.prod config | grep -c '\${'
bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml deployed
```

期望：第一個指令印 `0`（沒有未展開的變數）；第二個印 `verify-tunnel-config OK`。

## 3. 建置映像

```
make test          # 先確認在 NAS 的 CPU 上測試全過
make build-prod
make verify-image
```

期望：三個都 exit 0，`verify-image OK`。
`make test` 在 Braswell 上會比你筆電慢很多（十分鐘等級是正常的）。

## 4. 首次索引（離峰執行）

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  run --rm --no-deps photo-app index
```

期望：輸出 `scan: seen=N new=N ...`，`warnings=0`。
把 `seen` 的數字抄進 `docs/deploy/preflight-report.md` 的 Photo count 欄位。

如果 `seen=0`，表示目錄層級不對——索引器要的是
`PHOTO_ROOT/<category>/<album>/<photo>` 兩層結構。先整理照片目錄再重跑。

如果中途被 OOM kill（exit 137），提高 `.env.prod` 的 `APP_MEM_LIMIT` 再重跑；
索引是可重入的，已完成的檔案不會重做。

## 5. 建立 allowlist

用 Task 14 抄下來的 Firebase User UID：

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  run --rm --no-deps photo-app admin add-user --uid=<admin-uid> --email=<admin-email> --role=admin
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  run --rm --no-deps photo-app admin add-user --uid=<member-uid> --email=<member-email> --role=member
```

**第三個身分不要加**——它就是用來驗 403 的。

確認：

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
  run --rm --no-deps photo-app admin list-users
```

## 6. 啟動

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod up -d
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod ps
```

期望：三個 service 都在跑，`photo-app` 狀態 `healthy`。
**確認沒有任何 host port 被開出來**：

```
docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod ps --format '{{.Name}} {{.Ports}}'
```

期望：Ports 欄全部是空的。只要看到 `0.0.0.0:...->...`，立刻停下——
spec 明令 application、DB 與 internal nginx port 不得 publish 到對外介面。

## 7. 排程每小時索引

**Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script**

- User: `root`
- Schedule: 每天、每小時重複一次，**起始時間設在離峰**
- Run command:

  ```
  bash /volume1/docker/photo-browser/repo/scripts/nas-index.sh
  ```

確認：手動按 **Run** 一次，然後看 `/volume1/docker/photo-browser/data/index.log`
最後幾行應該有 `=== index ok ===`。

## 8. 排程每日備份

同樣用 Task Scheduler，每天一次（RPO 24 小時）：

```
bash /volume1/docker/photo-browser/repo/scripts/backup-sqlite.sh \
  --compose /volume1/docker/photo-browser/repo/deploy/compose/docker-compose.prod.yml \
  --dest /volume1/backup/photo-browser --keep 7
```

把 `/volume1/backup/photo-browser` 納入既有的 Synology off-NAS 備份流程。
Thumbnails **不要**備份——它是 derived data，restore 時重建。

備份腳本的完整性檢查在 DSM 上會走 `photo-browser-api-acceptance` 映像（DSM 沒有
`sqlite3` 執行檔），所以先建一次：

```
docker build --target api-acceptance -t photo-browser-api-acceptance .
```

## 9. 機密與識別值的備份

備份壓縮檔**刻意不含**任何憑證。下列項目要放進你既有的密碼管理／機密備份流程
（spec §9：Firebase/Cloudflare identifiers 與 encrypted credentials 納入既定
secret backup process）：

| 項目 | 從哪裡來 | 遺失的後果 |
|---|---|---|
| Tunnel credential `<tunnel-id>.json` | Task 14 §2 | 要重建 tunnel 並改 DNS |
| Cloudflare 帳號與 zone | Task 14 §1 | 需要重新接管網域 |
| Tunnel ID | Task 14 §2 | 可從 dashboard 查回 |
| Firebase project ID 與四個 `VITE_FIREBASE_*` | Task 14 §5 | 可從 console 查回 |
| `deploy/compose/.env.prod` | 本文件 §2 | 可重建，但含 uid/gid 與路徑，重建費時 |
| 三個測試身分的 Firebase User UID | Task 14 §5 | 要重新登入取得 |

確認方式：把上表填好存進密碼管理器後，**假裝 NAS 已毀**，只用密碼管理器裡的
內容 + off-NAS 備份，能不能回答「tunnel 叫什麼名字、credential 在哪」。
答不出來就是還沒備份完整。

## 10. DSM 通知

Spec §8 要求 repeated container restart、volume near-full 與 backup failure
都要透過 Synology alert 通知。

1. **Control Panel → Notification → Email**：設好收件信箱並按 **Send a test
   message** 確認收得到。
2. **Control Panel → Notification → Rules → Storage**：開啟
   **Volume usage exceeds** 並設在 **85%**。thumbnail 的低空間保護（Task 3）
   在 2 GiB 剩餘時才啟動，85% 的警示讓你早很多知道。
3. **Task Scheduler** 的兩個任務（index 與 backup）都在 **Settings** 分頁勾選
   **Send run details by email** 與 **only when the script terminates
   abnormally**。`nas-index.sh` 把「鎖被佔住」回傳 0，所以正常運作時不會吵你；
   只有真正失敗才發信。
4. Container 反覆重啟的偵測：DSM 的 Container Manager 不會主動通知，用一個
   每日檢查的排程任務補上：

   ```
   bash /volume1/docker/photo-browser/repo/scripts/nas-index.sh --compose      /volume1/docker/photo-browser/repo/deploy/compose/docker-compose.prod.yml >/dev/null
   docker compose -f /volume1/docker/photo-browser/repo/deploy/compose/docker-compose.prod.yml      --env-file /volume1/docker/photo-browser/repo/deploy/compose/.env.prod      ps --format '{{.Name}} {{.Status}}' | grep -v 'Up ' && exit 1
   exit 0
   ```

   任何 service 不是 `Up`（含 `Restarting`）時以 exit 1 結束，DSM 就會寄信。

確認方式：手動把 `photo-app` 停掉（`docker compose ... stop photo-app`），
按 **Run** 跑上面那個任務，應該收到失敗通知；再 `start` 回來。
```

- [ ] **Step 3: 寫 `03-acceptance.md`**

```markdown
# 03 — 驗收清單（spec §11）

每一項都要有證據。「看起來沒問題」不算。

## 自動化檢查

在筆電上對真實部署執行（token 從瀏覽器 devtools 的
Network → 任一 `/api/v1` request → Request Headers → Authorization 複製）：

```
bash scripts/verify-deployment.sh \
  --base-url https://<PUBLIC_API_HOST> \
  --member-token "<member jwt>" \
  --admin-token  "<admin jwt>" \
  --denied-token "<unallowlisted jwt>" \
  --edge
```

- [ ] 全部 `ok`，最後印出 `verify-deployment OK`

## 逐項驗收

- [ ] **LAN 外的 allowlisted member 可完成完整 browse flow**
      用手機的行動網路（**關掉 Wi-Fi**）開 `https://<PUBLIC_APP_HOST>`，
      Google 登入 → 照片格狀清單 → 開一張圖 → 登出。
- [ ] **Unallowlisted 使用者取不到 metadata 或 image bytes**
      用第三個身分登入，落在 `/forbidden`；上面的自動化檢查已覆蓋 API 層。
- [ ] **NAS 未暴露 DSM／SMB／filesystem／DB／管理埠**
      `--edge` 檢查已覆蓋 DSM 內容；另外手動確認
      `https://<PUBLIC_API_HOST>:5001` 與 `https://<PUBLIC_API_HOST>/webman/`
      都不是 DSM 畫面。
- [ ] **人工複製新照片後能收斂**
      在 `photos` share 的某個 album 放一張新照片 → Task Scheduler 按 **Run**
      → 重整前端 → 新照片出現。
- [ ] **Original bytes 由 Nginx 傳送（backend 授權後）**
      devtools 看 `/api/v1/photos/<id>/original` 回 200 且
      `content-type: image/jpeg`；backend log 顯示該請求，但 body 大小為 0。
- [ ] **Mobile layout 在 360 px 可用**
      devtools 切 360×640，無橫向捲動、底部導覽可點。
      （Spec 3 的 Playwright 已自動驗過同一條件。）
- [ ] **Logs 足以診斷 tunnel / DB / auth / scan / disk 問題**
      `docker compose ... logs --tail=50` 三個 service 各看一次，
      確認有 request id、status、duration；**確認沒有 bearer token 出現**：
      ```
      docker compose -f deploy/compose/docker-compose.prod.yml --env-file deploy/compose/.env.prod \
        logs --no-color | grep -ciE 'bearer [a-z0-9._-]{20,}'
      ```
      期望印 `0`。
- [ ] **Backup/restore drill 成功且不依賴 thumbnail backup**
      在筆電上 `make restore-drill`（對 prodcheck），印出 `restore drill PASSED`。
      NAS 上則至少驗證一次真實備份可讀：
      ```
      docker run --rm -v /volume1/backup/photo-browser:/b photo-browser-api-acceptance \
        sqlite3 /b/<latest>.db "PRAGMA integrity_check; SELECT count(*) FROM users;"
      ```
- [ ] **在 target NAS 記錄 idle 與 scan-time CPU/memory**
      在 NAS 上跑 `scripts/measure-resources.sh`（見
      `docs/deploy/resource-measurements.md`），把結果 commit 回來，
      並據此調整 `.env.prod` 的 memory limit。

## 簽收

| 項目 | 日期 | 執行者 | 備註 |
|---|---|---|---|
| 自動化檢查全過 | | | |
| 逐項驗收全過 | | | |
| 資源數據已記錄 | | | |

全部通過後，POC 才可進入 MVP 決策（spec §12）。
若有失敗項目，針對**實際缺口**另寫小型 follow-up spec（例如 HEIC codec 或
viewer-size derivative），**不要**擴張整體架構。

## Troubleshooting

| 症狀 | 最可能原因 | 怎麼確認 |
|---|---|---|
| 登入後一直停在 Access denied | UID 沒進 allowlist，或抄成 email 而不是 Firebase User UID | `photo-app admin list-users` 比對 |
| 所有登入都失敗、token 看起來有效 | NAS 時鐘偏移 | NAS 上 `date -u` vs 筆電 `date -u` |
| 前端載入但所有 API 都 CORS 失敗 | `ALLOWED_ORIGINS` 沒有 `https://<PUBLIC_APP_HOST>` | `.env.prod` 與 devtools console |
| API hostname 回 `error 1033` | tunnel 沒連上 | `docker compose ... logs cloudflared` |
| API hostname 直接斷線 | nginx `server_name` 與 `PUBLIC_API_HOST` 不一致 | 兩處設定比對 |
| 縮圖全空、原圖正常 | thumbnail volume 權限或空間 | `df -h` 與 `ls -la /volume1/docker/photo-browser/thumbnails` |
| 索引 exit 137 | 記憶體上限太低 | 提高 `APP_MEM_LIMIT` 後重跑 |
| 每小時收到 DSM 失敗通知 | `nas-index.sh` 把 exit 3 當失敗 | 看 `index.log` 是否為 `index skipped` |
```

- [ ] **Step 4: 驗證兩份文件引用的東西都存在**

```bash
# 文件裡出現的每個 scripts/ 與 deploy/ 路徑都必須真的存在
grep -ohE '(scripts|deploy|docs)/[A-Za-z0-9_./-]+' docs/deploy/02-nas-deployment.md docs/deploy/03-acceptance.md \
  | sed 's/[.,)]$//' | sort -u | while read -r f; do
    case "$f" in *'<'*|*'>'*) continue ;; esac
    test -e "$f" && echo "ok   $f" || echo "MISSING $f"
  done
```

期望：沒有任何 `MISSING`。有的話表示 runbook 會叫人去跑不存在的腳本。

- [ ] **Step 5: 驗證文件裡的 make target 都存在**

```bash
grep -ohE 'make [a-z-]+' docs/deploy/*.md | sort -u | sed 's/make //' | while read -r t; do
  grep -q "^$t:" Makefile && echo "ok   make $t" || echo "MISSING make $t"
done
```

期望：沒有 `MISSING`。

- [ ] **Step 6: Commit**

```bash
git add docs/deploy/02-nas-deployment.md docs/deploy/03-acceptance.md
git commit -m "docs: nas deployment runbook and spec 4 acceptance checklist"
```

---

## 附錄 A：完成關卡

Track A（repo）在以下全綠時算完成，這些都可以在沒有 NAS、沒有 Cloudflare 帳號的情況下驗證：

- [ ] `make test` exit 0（含 Task 2 的新子命令測試、Task 3 的 free-space guard 測試、Task 4 的 no-store 測試）
- [ ] `make test-web` exit 0
- [ ] `make build-prod && make verify-image` exit 0
- [ ] `bash scripts/verify-tunnel-config.sh deploy/cloudflared/config.yml template` OK
- [ ] `make prodcheck-up && make verify-deployment` 全部 `ok`
- [ ] `make restore-drill` 印出 `restore drill PASSED`
- [ ] `bash scripts/measure-resources.sh` 產出兩張表
- [ ] `make build-web-prod`（帶 stub Firebase 值）exit 0 且 `Bundle guard OK`
- [ ] `make prodcheck-down` 收乾淨

Track B（runbook）在以下全綠時算完成——**這一段需要人，且需要真實的網域、
Cloudflare 帳號、Firebase 專案與 NAS**：

- [ ] `docs/deploy/preflight-report.md` 已在 DS716+II 上實跑且無 BLOCKING
- [ ] `docs/deploy/01-external-services.md` 的值表格填滿
- [ ] `docs/deploy/02-nas-deployment.md` 走完，三個 service `healthy` 且無 published port
- [ ] `docs/deploy/03-acceptance.md` 所有項目打勾並簽收
- [ ] `docs/deploy/resource-measurements.md` 含 NAS 實測數據

## 附錄 B：Spec 對照

| Spec 4 節 | 對應 task |
|---|---|
| §3 前置檢查關卡 | Task 1（preflight + HEIC probe） |
| §3 低空間時停止產生 thumbnail | Task 3（indexer free-space guard） |
| §4 部署拓撲 | Task 5（compose 三 service）、Task 7（tunnel ingress） |
| §5 Volume 與 permission 契約 | Task 5（mount 與 uid/gid）、Task 15 §1（DSM 權限） |
| §6 Cloudflare 契約 — Pages | Task 13（build 與環境變數）、Task 14 §3 |
| §6 Cloudflare 契約 — Tunnel | Task 7、Task 14 §2 |
| §6 Cloudflare 契約 — Caching | Task 4（no-store / private）、Task 14 §4、Task 9（`--edge` 檢查） |
| §7 Firebase production 契約 | Task 14 §5、Task 15 §5（bootstrap admin） |
| §8 Scheduling | Task 13（`nas-index.sh`）、Task 15 §7 |
| §8 Health | Task 2（healthcheck 子命令）、Task 5（compose healthcheck） |
| §8 Logs | Task 4（log_format）、Task 5（json-file rotation）、Task 15 驗收的 token grep |
| §8 Resource limits | Task 5（limit 欄位）、Task 12（實測後調整） |
| §8 Synology alerts（restart / volume / backup） | Task 15 §10 |
| §9 備份 | Task 2（`backup` 子命令）、Task 10（排程腳本） |
| §9 機密與 identifier 備份 | Task 15 §9 |
| §9 Restore drill | Task 11 |
| §10 Functional path | Task 9（前半）、Task 15 驗收（真機手動） |
| §10 Security checks | Task 9（全部八條） |
| §10 Recovery checks | Task 11 |
| §11 POC 驗收條件 | Task 15（`03-acceptance.md`）＋附錄 A |
| §12 MVP 決策 | Task 15 簽收表之後，不在本 plan 範圍 |

## 附錄 C：本 plan 不做的事

沿用 spec §2 的 non-goals，另加執行面的界線：

- High availability、Cloudflare Worker backend、protected media 的共用 edge cache
- Prometheus / Grafana、Kubernetes、外部資料庫、public sharing
- HEIC 支援（Task 1 只做 probe 並記錄為 unsupported）
- 自動化 Cloudflare / Firebase / DSM 設定——這些需要互動式登入與人工判斷，
  本 plan 一律寫成 runbook 由人執行
- 代替使用者註冊網域或建立任何外部帳號
