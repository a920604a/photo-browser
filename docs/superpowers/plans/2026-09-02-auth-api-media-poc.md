# 驗證授權 API 與影像傳送 POC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL：以 `superpowers:subagent-driven-development`（建議）或 `superpowers:executing-plans` 逐 Task 執行。每個 step 使用 `- [ ]` checkbox 追蹤。

**Goal：** 在 Spec 1 索引之上加一個 `photo-app serve` HTTP process，提供 Firebase ID Token 驗證、allowlist 授權、唯讀 catalogue/timeline API、以及透過 Nginx `X-Accel-Redirect` 送出 thumbnail 與 original 的安全 media flow。

**Architecture：** 沿用 Spec 1 SQLite schema，多加一個 application-owned `users` table 與 `photo-app admin` CLI 子命令做 allowlist bootstrap。HTTP 層用 Go 1.22+ 的 `net/http` + `ServeMux`（method + path pattern），Firebase token 用 `github.com/golang-jwt/jwt/v5` + 自己抓 Google JWKS 驗簽。Media handler 只回 header/`X-Accel-Redirect`，實際 bytes 由 Nginx 從 internal location 送出。Acceptance 用 docker-compose 起 Nginx + backend + fake JWKS server 做真實 end-to-end。

**Tech Stack：** Go 1.26、`net/http`、`github.com/golang-jwt/jwt/v5`、既有 `modernc.org/sqlite`、Nginx 1.26 stable、docker compose v2。

**Spec：** `docs/superpowers/specs/2026-08-30-02-auth-api-media-poc.md`

## Global Constraints

- 沿用 Spec 1 schema；`users` 是 application-owned，禁止與 filesystem-derived 資料混雜。
- `photo-app rebuild` 已明確只 truncate derived tables（Spec 1 Task 7），Spec 2 不得改動該 scope；`users` 必須存活。
- 只允許以 `net/http` + `ServeMux` 實作路由，不新增 web framework、middleware chain lib、ORM 或 DI container。
- Firebase token 驗證固定以 `github.com/golang-jwt/jwt/v5` + 自寫 JWKS cache；不引入 `firebase.google.com/go`。
- JWKS 端點固定值：`https://www.googleapis.com/service_accounts/v1/jwks/securetoken@system.gserviceaccount.com`；issuer 固定 `https://securetoken.google.com/<project_id>`；audience 固定 `<project_id>`。允許 `FIREBASE_JWKS_URL`、`FIREBASE_ISSUER` env override 供測試使用，但預設不得為空。
- 所有 `/api/v1/**` 除 `/health/*` 外必須同時通過 authentication 與 allowlist authorization；middleware 順序固定，不得繞過。
- Public catalogue/media endpoint 僅接受 GET/HEAD；`POST/PATCH` 只出現在 admin routes。
- Backend 不回大型 image bytes；thumbnail 與 original 都以 `X-Accel-Redirect` 交給 Nginx。Handler 內禁止 `io.Copy(w, file)`。
- Cursor 必須是 opaque base64url、含 version byte 與由 server 決定的 tuple；client 不得直接送 SQL 欄位名。
- Error response 統一 `{"error":{"code","message"}}`；禁止洩漏 SQL、path、stack、Firebase internals。
- Log 必須遮蔽 `Authorization` header 與任何 token；每個 request 有唯一 request ID。
- CORS allow-origin 精確等於環境變數指定的 frontend origin，禁止 wildcard；CORS 不視為 authorization。
- Nginx 兩個 internal location（`/internal-media/originals/`、`/internal-media/thumbnails/`）都必須 `internal;` 且關閉 autoindex；container 內 mount 為 read-only。
- Target 仍為 `linux/amd64` Braswell、`GOAMD64=v1`、user `65532:65532`；不能在 runtime image 加入測試 binary 或 exiftool。

---

## 檔案配置

```text
.
├── cmd/
│   ├── photo-app/main.go                # 既有；不改
│   └── testauth/main.go                 # 新；acceptance-only fake Firebase：JWKS + mint
├── internal/
│   ├── config/config.go                 # 擴充 HTTP/Firebase/CORS/Nginx paths
│   ├── database/schema.sql              # 加 users table + partial unique index
│   ├── users/
│   │   ├── models.go
│   │   ├── store.go                     # allowlist CRUD、normalize、lookup
│   │   └── store_test.go
│   ├── auth/
│   │   ├── firebase.go                  # JWKS cache + Verify(token) -> Claims
│   │   ├── firebase_test.go
│   │   ├── middleware.go                # WithAuth(next http.Handler)
│   │   ├── middleware_test.go
│   │   └── testkeys.go                  # test-only RSA/JWKS/Sign helpers（_test build tag 亦可）
│   ├── httpapi/
│   │   ├── errors.go                    # writeError、Err codes、safe messages
│   │   ├── errors_test.go
│   │   ├── requestid.go                 # WithRequestID middleware + context helper
│   │   ├── requestid_test.go
│   │   ├── cors.go                      # WithCORS(originAllowlist)
│   │   ├── cors_test.go
│   │   ├── cursor.go                    # encode/decode opaque tuple cursors
│   │   ├── cursor_test.go
│   │   ├── pagination.go                # limit parsing (default 50, max 200)
│   │   ├── pagination_test.go
│   │   ├── router.go                    # NewRouter(deps) *http.ServeMux
│   │   ├── router_test.go               # end-to-end round-trip against router
│   │   ├── health.go                    # /live /ready
│   │   ├── me.go                        # /me
│   │   ├── catalog.go                   # /categories /albums /photos /timeline
│   │   ├── catalog_test.go
│   │   ├── media.go                     # /photos/{id}/thumbnail /original
│   │   ├── media_test.go
│   │   ├── admin.go                     # /admin/users /admin/index-runs
│   │   └── admin_test.go
│   ├── catalog/
│   │   ├── query.go                     # 唯讀 List/GetByID/Timeline 方法
│   │   └── query_test.go
│   ├── indexer/
│   │   └── indexer.go                   # 抽出 RunWithScanID(scanID, opts) 供 admin API 用
│   └── app/
│       ├── admin_cli.go                 # `admin add-user|list-users|set-role|disable-user|enable-user`
│       ├── admin_cli_test.go
│       ├── serve.go                     # `serve` 子命令：build router、start server、trigger scan goroutine
│       ├── serve_test.go
│       ├── run.go                       # 修改：路由 admin/serve 兩個新子命令
│       └── run_test.go                  # 修改：新命令的 dispatcher 測試
├── deploy/
│   ├── nginx/nginx.conf                 # backend upstream + /api proxy + /internal-media internal
│   └── compose/docker-compose.acceptance.yml
├── scripts/
│   ├── acceptance.sh                    # 既有；不改
│   └── acceptance_api.sh                # 新；docker-compose up 後跑 http 驗證
├── Dockerfile                           # 加 testauth build + api-acceptance stage
└── Makefile                             # 加 `make acceptance-api`
```

不建立額外 service/repository interface、DI container、handler factory 或 middleware chain library。每個 handler 直接接 `deps` struct（Store、Users、Verifier、Indexer、Config），透過 closure 掛到 `ServeMux`。

---

### Task 1：擴充 config 與新增 users table

**Files：**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/database/schema.sql`
- Modify: `internal/database/database_test.go`
- Create: `internal/users/models.go`
- Create: `internal/users/store.go`
- Create: `internal/users/store_test.go`

**Interfaces：**
- Consumes: 既有 `catalog.Store` transaction pattern；`database.Open`
- Produces:
  ```go
  type Config struct {
      PhotoRoot, DataDir, ThumbnailDir string
      HTTPListen        string  // env HTTP_LISTEN, default ":8080"
      FirebaseProjectID string  // env FIREBASE_PROJECT_ID, required for serve
      FirebaseIssuer    string  // env FIREBASE_ISSUER, default derived from ProjectID
      FirebaseJWKSURL   string  // env FIREBASE_JWKS_URL, default Google JWKS
      AllowedOrigins    []string // env ALLOWED_ORIGINS, comma-separated
      InternalOriginals string   // env INTERNAL_MEDIA_ORIGINALS, default "/internal-media/originals"
      InternalThumbnails string  // env INTERNAL_MEDIA_THUMBNAILS, default "/internal-media/thumbnails"
  }
  ```
  ```go
  type User struct {
      ID              int64
      FirebaseUID     sql.NullString
      Email           sql.NullString
      NormalizedEmail sql.NullString
      Role            string // "admin" | "member"
      Enabled         bool
      CreatedAt, UpdatedAt time.Time
  }

  type Store struct { conn dbtx }
  func NewStore(db *sql.DB) *Store
  func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error
  func (s *Store) Add(ctx context.Context, u User, now time.Time) (int64, error)
  func (s *Store) UpdateRole(ctx context.Context, id int64, role string, now time.Time) error
  func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool, now time.Time) error
  func (s *Store) LookupByUID(ctx context.Context, uid string) (User, bool, error)
  func (s *Store) LookupByEmail(ctx context.Context, email string) (User, bool, error) // uses normalized_email
  func (s *Store) List(ctx context.Context) ([]User, error)
  func NormalizeEmail(email string) string // trim + strings.ToLower
  ```

- [ ] **Step 1：擴 config test，驗證新環境變數與必填規則**

  在 `internal/config/config_test.go` 加：

  ```go
  func TestLoadHTTPServeDefaults(t *testing.T) {
      t.Setenv("PHOTO_ROOT", "/photos")
      t.Setenv("DATA_DIR", "/data")
      t.Setenv("THUMBNAIL_DIR", "/thumbnails")
      t.Setenv("FIREBASE_PROJECT_ID", "demo-proj")
      t.Setenv("ALLOWED_ORIGINS", "https://photos.example.com")
      cfg, err := Load()
      if err != nil { t.Fatal(err) }
      if cfg.HTTPListen != ":8080" { t.Fatalf("HTTPListen=%q", cfg.HTTPListen) }
      if cfg.FirebaseIssuer != "https://securetoken.google.com/demo-proj" {
          t.Fatalf("issuer=%q", cfg.FirebaseIssuer)
      }
      if cfg.FirebaseJWKSURL == "" { t.Fatal("JWKS URL empty") }
      if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "https://photos.example.com" {
          t.Fatalf("origins=%v", cfg.AllowedOrigins)
      }
      if cfg.InternalOriginals != "/internal-media/originals" ||
          cfg.InternalThumbnails != "/internal-media/thumbnails" {
          t.Fatalf("internal paths=%+v", cfg)
      }
  }

  func TestLoadRejectsWildcardOrigin(t *testing.T) {
      t.Setenv("PHOTO_ROOT", "/p"); t.Setenv("DATA_DIR", "/d"); t.Setenv("THUMBNAIL_DIR", "/t")
      t.Setenv("FIREBASE_PROJECT_ID", "demo")
      t.Setenv("ALLOWED_ORIGINS", "*")
      if _, err := Load(); err == nil { t.Fatal("wildcard origin must be rejected") }
  }
  ```

  Run：`make test`  Expected：FAIL（新欄位不存在）。

- [ ] **Step 2：實作 config 擴充**

  修改 `internal/config/config.go`：

  ```go
  type Config struct {
      PhotoRoot, DataDir, ThumbnailDir                 string
      HTTPListen                                       string
      FirebaseProjectID, FirebaseIssuer, FirebaseJWKSURL string
      AllowedOrigins                                   []string
      InternalOriginals, InternalThumbnails            string
  }

  const defaultJWKS = "https://www.googleapis.com/service_accounts/v1/jwks/securetoken@system.gserviceaccount.com"

  func Load() (Config, error) {
      c := Config{
          PhotoRoot:          value("PHOTO_ROOT", "/photos"),
          DataDir:            value("DATA_DIR", "/data"),
          ThumbnailDir:       value("THUMBNAIL_DIR", "/thumbnails"),
          HTTPListen:         value("HTTP_LISTEN", ":8080"),
          FirebaseProjectID:  value("FIREBASE_PROJECT_ID", ""),
          FirebaseIssuer:     value("FIREBASE_ISSUER", ""),
          FirebaseJWKSURL:    value("FIREBASE_JWKS_URL", defaultJWKS),
          InternalOriginals:  value("INTERNAL_MEDIA_ORIGINALS", "/internal-media/originals"),
          InternalThumbnails: value("INTERNAL_MEDIA_THUMBNAILS", "/internal-media/thumbnails"),
      }
      for name, path := range map[string]string{
          "PHOTO_ROOT": c.PhotoRoot, "DATA_DIR": c.DataDir, "THUMBNAIL_DIR": c.ThumbnailDir,
      } {
          if !filepath.IsAbs(path) { return Config{}, fmt.Errorf("%s must be absolute", name) }
      }
      if c.FirebaseProjectID != "" && c.FirebaseIssuer == "" {
          c.FirebaseIssuer = "https://securetoken.google.com/" + c.FirebaseProjectID
      }
      raw := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS"))
      if raw != "" {
          for _, o := range strings.Split(raw, ",") {
              o = strings.TrimSpace(o)
              if o == "" { continue }
              if o == "*" { return Config{}, errors.New("ALLOWED_ORIGINS must not be *") }
              c.AllowedOrigins = append(c.AllowedOrigins, o)
          }
      }
      return c, nil
  }
  ```

  Run：`make test`  Expected：config tests PASS。既有 Spec 1 test 不受影響（新欄位在缺省下不強制）。

- [ ] **Step 3：schema 加 users table + partial unique index，並寫遷移 test**

  在 `internal/database/schema.sql` 尾端追加：

  ```sql
  CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    firebase_uid TEXT,
    email TEXT,
    normalized_email TEXT,
    role TEXT NOT NULL CHECK(role IN ('admin','member')),
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK(firebase_uid IS NOT NULL OR normalized_email IS NOT NULL)
  );
  CREATE UNIQUE INDEX IF NOT EXISTS idx_users_firebase_uid
    ON users(firebase_uid) WHERE firebase_uid IS NOT NULL;
  CREATE UNIQUE INDEX IF NOT EXISTS idx_users_normalized_email
    ON users(normalized_email) WHERE normalized_email IS NOT NULL;
  ```

  在 `internal/database/database_test.go` 加：

  ```go
  func TestOpenCreatesUsersTable(t *testing.T) {
      db, err := Open(filepath.Join(t.TempDir(), "photo.db"))
      if err != nil { t.Fatal(err) }
      defer db.Close()
      var name string
      if err := db.QueryRow(
          `SELECT name FROM sqlite_master WHERE type='table' AND name='users'`,
      ).Scan(&name); err != nil { t.Fatalf("users table missing: %v", err) }
      // partial unique index rejects duplicate uid but tolerates duplicate NULL uid.
      _, err = db.Exec(`INSERT INTO users(firebase_uid,role,enabled,created_at,updated_at) VALUES(?,?,1,?,?)`,
          "uid-1", "member", "t", "t")
      if err != nil { t.Fatal(err) }
      _, err = db.Exec(`INSERT INTO users(firebase_uid,role,enabled,created_at,updated_at) VALUES(?,?,1,?,?)`,
          "uid-1", "member", "t", "t")
      if err == nil { t.Fatal("duplicate firebase_uid must fail") }
  }
  ```

  Run：`make test`  Expected：新 test PASS；schema 已有 idempotent guard，重跑不炸。

- [ ] **Step 4：寫 users store failing test**

  `internal/users/store_test.go` 建議測試：
  - `NormalizeEmail("  Foo@Example.COM ")` → `"foo@example.com"`。
  - Add(UID only) / Add(email only) / Add(both) 各成功；Add(neither) 回 error。
  - Add 重複 UID 或重複 email 回 error（sqlite unique constraint 映射為 `users.ErrDuplicate`）。
  - `LookupByUID`、`LookupByEmail` 分別回對應 row；`enabled=0` 仍會被 lookup 到（呼叫端負責 gate）。
  - `SetEnabled(false)` 後 `List()` 仍看得到但 `Enabled=false`。
  - `UpdateRole("admin")` 生效；不合法 role 回 error。

  ```go
  func TestUsersStoreCRUD(t *testing.T) {
      db, _ := database.Open(filepath.Join(t.TempDir(), "photo.db"))
      defer db.Close()
      s := users.NewStore(db)
      ctx := context.Background()
      now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

      id, err := s.Add(ctx, users.User{
          FirebaseUID:     sql.NullString{String: "uid-a", Valid: true},
          Email:           sql.NullString{String: "Alice@Example.com", Valid: true},
          NormalizedEmail: sql.NullString{String: users.NormalizeEmail("Alice@Example.com"), Valid: true},
          Role: "admin", Enabled: true,
      }, now)
      if err != nil || id == 0 { t.Fatalf("add: id=%d err=%v", id, err) }

      got, ok, err := s.LookupByEmail(ctx, "ALICE@example.com")
      if err != nil || !ok || got.Role != "admin" { t.Fatalf("lookup: ok=%v err=%v role=%q", ok, err, got.Role) }
  }

  func TestUsersStoreRejectsEmpty(t *testing.T) {
      db, _ := database.Open(filepath.Join(t.TempDir(), "photo.db"))
      defer db.Close()
      s := users.NewStore(db)
      _, err := s.Add(context.Background(), users.User{Role: "member", Enabled: true}, time.Now())
      if err == nil { t.Fatal("empty uid+email must fail") }
  }
  ```

  Run：`make test`  Expected：FAIL（package 尚未存在）。

- [ ] **Step 5：實作 users store**

  `internal/users/models.go` 定義上面 `User` type。`store.go` 用 `catalog.Store` 相同的 `dbtx` 介面 pattern：

  ```go
  var ErrDuplicate = errors.New("users: duplicate identity")
  var ErrInvalidRole = errors.New("users: invalid role")

  func NormalizeEmail(e string) string {
      return strings.ToLower(strings.TrimSpace(e))
  }

  func (s *Store) Add(ctx context.Context, u User, now time.Time) (int64, error) {
      if !u.FirebaseUID.Valid && !u.NormalizedEmail.Valid {
          return 0, errors.New("users.Add: firebase_uid or normalized_email required")
      }
      if u.Role != "admin" && u.Role != "member" { return 0, ErrInvalidRole }
      nowStr := now.UTC().Format("2006-01-02T15:04:05Z07:00")
      var id int64
      err := s.conn.QueryRowContext(ctx, `
          INSERT INTO users(firebase_uid,email,normalized_email,role,enabled,created_at,updated_at)
          VALUES(?,?,?,?,?,?,?) RETURNING id`,
          u.FirebaseUID, u.Email, u.NormalizedEmail, u.Role, boolInt(u.Enabled), nowStr, nowStr,
      ).Scan(&id)
      if err != nil {
          if strings.Contains(err.Error(), "UNIQUE") { return 0, ErrDuplicate }
          return 0, err
      }
      return id, nil
  }
  ```

  `LookupByEmail` 內部先 `NormalizeEmail` 再查 `normalized_email = ?`。`enabled` 儲存為 INTEGER，讀出時轉 bool。時間字串沿用既有 `catalog` 格式。

- [ ] **Step 6：Run tests**

  Run：`make test`  Expected：users tests PASS，config tests PASS，database tests PASS。

- [ ] **Step 7：Commit**

  ```bash
  git add internal/config internal/database internal/users
  git commit -m "feat: add user allowlist store and http-serve config"
  ```

---

### Task 2：Admin CLI 子命令做 allowlist bootstrap

**Files：**
- Create: `internal/app/admin_cli.go`
- Create: `internal/app/admin_cli_test.go`
- Modify: `internal/app/run.go`
- Modify: `internal/app/run_test.go`

**Interfaces：**
- Consumes: `users.Store`、`config.Config`、`filelock.Acquire`
- Produces:
  ```go
  // CLI 契約
  photo-app admin add-user     --uid=<uid> | --email=<addr>  [--role=member|admin]
  photo-app admin list-users
  photo-app admin set-role     (--uid=... | --email=...) --role=admin|member
  photo-app admin enable       (--uid=... | --email=...)
  photo-app admin disable      (--uid=... | --email=...)
  ```
  Exit code：`0` 成功、`2` 使用錯誤、`1` runtime failure、`3` lock busy（沿用既有）。

- [ ] **Step 1：寫 admin CLI 派發 failing test**

  在 `internal/app/admin_cli_test.go` 用 `injected users.Store`（sqlite in-memory 或 tempdir）：

  ```go
  func TestAdminAddUserByEmailInsertsRow(t *testing.T) {
      env := newTestEnv(t) // helper 建立 tempdir + open sqlite + users.Store
      code := app.RunAdmin(context.Background(), env,
          []string{"add-user", "--email=Alice@Example.com", "--role=admin"},
          io.Discard, io.Discard)
      if code != 0 { t.Fatalf("exit=%d", code) }
      got, ok, _ := env.Users.LookupByEmail(context.Background(), "alice@example.com")
      if !ok || got.Role != "admin" { t.Fatalf("row=%+v ok=%v", got, ok) }
  }

  func TestAdminAddUserRequiresIdentity(t *testing.T) {
      env := newTestEnv(t)
      code := app.RunAdmin(context.Background(), env,
          []string{"add-user", "--role=member"}, io.Discard, io.Discard)
      if code != 2 { t.Fatalf("expected usage error, got %d", code) }
  }
  ```

  另加 test：`set-role` 未知 uid 回 exit 1、`disable` 兩次 idempotent、`list-users` 輸出 tab-separated `id\tuid\temail\trole\tenabled`。

  Run：`make test`  Expected：FAIL（`RunAdmin` 不存在）。

- [ ] **Step 2：實作 admin CLI 派發**

  `internal/app/admin_cli.go`：

  ```go
  type AdminEnv struct {
      Users *users.Store
      Now   func() time.Time
  }

  func RunAdmin(ctx context.Context, env AdminEnv, args []string, stdout, stderr io.Writer) int {
      if len(args) == 0 { fmt.Fprintln(stderr, adminUsage); return 2 }
      switch args[0] {
      case "add-user":     return adminAddUser(ctx, env, args[1:], stdout, stderr)
      case "list-users":   return adminListUsers(ctx, env, stdout, stderr)
      case "set-role":     return adminSetRole(ctx, env, args[1:], stdout, stderr)
      case "enable":       return adminSetEnabled(ctx, env, args[1:], true, stdout, stderr)
      case "disable":      return adminSetEnabled(ctx, env, args[1:], false, stdout, stderr)
      default:
          fmt.Fprintf(stderr, "unknown admin command: %s\n%s\n", args[0], adminUsage); return 2
      }
  }
  ```

  Parser：不引入 `flag` package chaos，就 loop `args`，只接受 `--uid=`、`--email=`、`--role=`；未知 flag → 2。實作參考：

  ```go
  func parseUserSelector(args []string) (uid, email, role string, extras []string, err error) {
      for _, a := range args {
          switch {
          case strings.HasPrefix(a, "--uid="):   uid = strings.TrimPrefix(a, "--uid=")
          case strings.HasPrefix(a, "--email="): email = strings.TrimPrefix(a, "--email=")
          case strings.HasPrefix(a, "--role="):  role = strings.TrimPrefix(a, "--role=")
          default:                                extras = append(extras, a)
          }
      }
      return
  }
  ```

  `add-user`：至少一個 uid/email；role default `member`；直接呼叫 `users.Store.Add`。ErrDuplicate → exit 1 帶 message `"user already exists"`。

  `set-role` 與 `enable/disable`：以 uid 優先、否則 email 找 row，找不到回 exit 1。

- [ ] **Step 3：把 `admin` dispatch 掛進 `app.Run`**

  修改 `internal/app/run.go` 的 switch：

  ```go
  case "admin":
      // admin 也需要 filelock，避免與 index 同時寫 users。
      return runLocked(ctx, stderr, cmds, func(ctx context.Context) error {
          return cmds.Admin(ctx, args[1:])
      })
  ```

  在 `Commands` struct 加 `Admin func(ctx context.Context, args []string) error`；`Main` 在 `prodEnv.users()` 建 `users.NewStore(db)` 後 wire。

- [ ] **Step 4：跑 tests + build**

  Run：
  ```bash
  make test
  make build
  docker run --rm photo-browser:local admin
  ```
  Expected：tests PASS；build PASS；`admin` 無子命令時印 usage + exit 2。

- [ ] **Step 5：Commit**

  ```bash
  git add internal/app internal/users
  git commit -m "feat: admin cli for allowlist bootstrap"
  ```

---

### Task 3：Firebase JWKS cache 與 JWT 驗證

**Files：**
- Create: `internal/auth/firebase.go`
- Create: `internal/auth/firebase_test.go`
- Create: `internal/auth/testkeys.go`
- Modify: `go.mod` (add `github.com/golang-jwt/jwt/v5`)

**Interfaces：**
- Produces:
  ```go
  type Claims struct {
      UID           string    // sub
      Email         string    // email
      EmailVerified bool      // email_verified
      IssuedAt      time.Time
      Expires       time.Time
  }

  type Verifier struct {
      Issuer, Audience, JWKSURL string
      HTTPClient *http.Client   // nil -> http.DefaultClient
      Now        func() time.Time // nil -> time.Now
      Refresh    time.Duration    // default 1h
  }

  func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error)

  // Test helper（在 testkeys.go）
  type TestSigner struct { KID string; PrivateKey *rsa.PrivateKey }
  func NewTestSigner(kid string) *TestSigner
  func (s *TestSigner) JWKS() []byte
  func (s *TestSigner) Sign(claims map[string]any) string
  ```
- Sentinel errors：`ErrInvalidToken`, `ErrTokenExpired`, `ErrIssuerMismatch`, `ErrAudienceMismatch`, `ErrKeyNotFound`, `ErrEmailNotVerified` — 只用在 log/tests；HTTP 對外一律 401。

- [ ] **Step 1：加入 `golang-jwt/jwt/v5` 依賴**

  在容器內執行：`docker run --rm -v "$PWD:/src" -w /src golang:1.26.7-bookworm go get github.com/golang-jwt/jwt/v5@v5.2.1 && go mod tidy`。確認 `go.sum` 更新。

- [ ] **Step 2：寫 verifier failing tests**

  `internal/auth/firebase_test.go` 使用 `TestSigner` 起 `httptest.Server` 供 JWKS：

  ```go
  func newFixture(t *testing.T) (*auth.Verifier, *auth.TestSigner, func()) {
      s := auth.NewTestSigner("test-kid-1")
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.Header().Set("Content-Type", "application/json")
          _, _ = w.Write(s.JWKS())
      }))
      v := &auth.Verifier{
          Issuer:  "https://securetoken.google.com/demo-proj",
          Audience: "demo-proj",
          JWKSURL: srv.URL,
          Now:     func() time.Time { return time.Unix(1_700_000_000, 0) },
      }
      return v, s, srv.Close
  }

  func TestVerifyValidToken(t *testing.T) {
      v, s, done := newFixture(t); defer done()
      tok := s.Sign(map[string]any{
          "iss":            v.Issuer,
          "aud":            v.Audience,
          "sub":            "user-1",
          "email":          "alice@example.com",
          "email_verified": true,
          "iat":            v.Now().Unix() - 10,
          "exp":            v.Now().Unix() + 3600,
      })
      c, err := v.Verify(context.Background(), tok)
      if err != nil { t.Fatalf("verify: %v", err) }
      if c.UID != "user-1" || c.Email != "alice@example.com" || !c.EmailVerified {
          t.Fatalf("claims=%+v", c)
      }
  }
  ```

  另加 test：
  - 過期（`exp` < now）→ ErrTokenExpired
  - `iss` 錯 → ErrIssuerMismatch
  - `aud` 錯 → ErrAudienceMismatch
  - 未知 kid → ErrKeyNotFound
  - 演算法非 RS256 → ErrInvalidToken（拒絕 `none` 與 HS256）
  - 兩次 Verify 之間只呼叫一次 JWKS URL（用 handler 計數）

  Run：`make test`  Expected：FAIL。

- [ ] **Step 3：實作 `testkeys.go`**

  用 `rsa.GenerateKey(rand.Reader, 2048)` 或固定 seed；`JWKS()` 回傳 Google Firebase 格式（`kid, kty=RSA, use=sig, alg=RS256, n, e`；`n/e` 用 base64url no padding）。`Sign` 用 `jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))` + `SignedString(privateKey)`，並把 header `kid` 設為 `TestSigner.KID`。

  這個檔在 production build 也會被編入（`internal/auth`），但只被 test package 呼叫。若要更嚴格，改成 `testkeys_test_helper.go` + `//go:build testkeys` 也可；本 plan 保持普通檔以便 `cmd/testauth` 重用同一套 helper。

- [ ] **Step 4：實作 `Verifier`**

  ```go
  type keySet struct {
      keys    map[string]*rsa.PublicKey
      fetched time.Time
  }

  type Verifier struct { /* fields as above */; mu sync.Mutex; cache *keySet }

  func (v *Verifier) refresh(ctx context.Context) (*keySet, error) {
      client := v.HTTPClient; if client == nil { client = http.DefaultClient }
      req, _ := http.NewRequestWithContext(ctx, "GET", v.JWKSURL, nil)
      resp, err := client.Do(req); if err != nil { return nil, err }
      defer resp.Body.Close()
      if resp.StatusCode != 200 { return nil, fmt.Errorf("jwks status %d", resp.StatusCode) }
      var body struct { Keys []struct{ Kid,Kty,Alg,N,E string } `json:"keys"` }
      if err := json.NewDecoder(resp.Body).Decode(&body); err != nil { return nil, err }
      ks := &keySet{keys: map[string]*rsa.PublicKey{}, fetched: v.now()}
      for _, k := range body.Keys {
          if k.Kty != "RSA" || k.Alg != "RS256" { continue }
          nBytes, _ := base64.RawURLEncoding.DecodeString(k.N)
          eBytes, _ := base64.RawURLEncoding.DecodeString(k.E)
          n := new(big.Int).SetBytes(nBytes); e := new(big.Int).SetBytes(eBytes)
          ks.keys[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
      }
      return ks, nil
  }

  func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
      v.mu.Lock(); defer v.mu.Unlock()
      ttl := v.Refresh; if ttl == 0 { ttl = time.Hour }
      if v.cache == nil || v.now().Sub(v.cache.fetched) > ttl {
          ks, err := v.refresh(ctx); if err != nil { return nil, err }
          v.cache = ks
      }
      if k, ok := v.cache.keys[kid]; ok { return k, nil }
      // 若 miss，強制 refresh 一次（rotation）
      ks, err := v.refresh(ctx); if err != nil { return nil, err }
      v.cache = ks
      if k, ok := ks.keys[kid]; ok { return k, nil }
      return nil, ErrKeyNotFound
  }

  func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
      parser := jwt.NewParser(
          jwt.WithValidMethods([]string{"RS256"}),
          jwt.WithIssuer(v.Issuer),
          jwt.WithAudience(v.Audience),
          jwt.WithLeeway(30*time.Second),
      )
      t, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
          kid, _ := t.Header["kid"].(string)
          return v.key(ctx, kid)
      })
      if err != nil {
          switch {
          case errors.Is(err, jwt.ErrTokenExpired):    return Claims{}, ErrTokenExpired
          case errors.Is(err, jwt.ErrTokenInvalidIssuer):   return Claims{}, ErrIssuerMismatch
          case errors.Is(err, jwt.ErrTokenInvalidAudience): return Claims{}, ErrAudienceMismatch
          }
          return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
      }
      mc := t.Claims.(jwt.MapClaims)
      return Claims{
          UID:           strOf(mc["sub"]),
          Email:         strOf(mc["email"]),
          EmailVerified: boolOf(mc["email_verified"]),
          IssuedAt:      timeOf(mc["iat"]),
          Expires:       timeOf(mc["exp"]),
      }, nil
  }
  ```

  `v.now()` 為內部 helper（讀 `Now` 或 `time.Now`）。

- [ ] **Step 5：Run tests**

  Run：`make test`  Expected：`internal/auth` PASS，其他 package 不受影響。

- [ ] **Step 6：Commit**

  ```bash
  git add internal/auth go.mod go.sum
  git commit -m "feat: verify firebase id tokens via cached jwks"
  ```

---

### Task 4：Auth middleware、error contract、request ID、CORS

**Files：**
- Create: `internal/auth/middleware.go`
- Create: `internal/auth/middleware_test.go`
- Create: `internal/httpapi/errors.go`
- Create: `internal/httpapi/errors_test.go`
- Create: `internal/httpapi/requestid.go`
- Create: `internal/httpapi/requestid_test.go`
- Create: `internal/httpapi/cors.go`
- Create: `internal/httpapi/cors_test.go`

**Interfaces：**
- Produces:
  ```go
  // httpapi/errors.go
  type ErrorCode string
  const (
      CodeBadRequest    ErrorCode = "bad_request"
      CodeUnauthorized  ErrorCode = "unauthorized"
      CodeForbidden     ErrorCode = "forbidden"
      CodeNotFound      ErrorCode = "not_found"
      CodeConflict      ErrorCode = "conflict"
      CodeInternal      ErrorCode = "internal"
  )
  func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string)
  func StatusFor(code ErrorCode) int

  // requestid.go
  func WithRequestID(next http.Handler) http.Handler
  func RequestIDFromContext(ctx context.Context) string

  // cors.go
  func WithCORS(origins []string, next http.Handler) http.Handler

  // auth/middleware.go
  type ContextKey int
  const CtxUser ContextKey = 1
  type AuthUser struct { ID int64; UID, Email, Role string }

  func WithAuth(v *Verifier, us *users.Store, next http.Handler) http.Handler
  func UserFromContext(ctx context.Context) (AuthUser, bool)
  func RequireRole(role string, next http.Handler) http.Handler
  ```

- [ ] **Step 1：寫 errors + request ID tests**

  `errors_test.go`：驗證 body shape `{"error":{"code":"forbidden","message":"..."}}`；`WriteError(w,http.StatusForbidden,CodeForbidden,"nope")` → status 403、`Content-Type: application/json`、body 精確 match。

  `requestid_test.go`：GET / 沒帶 header 時 middleware 產生 UUID-like ID 並塞 response header `X-Request-ID`；帶入的 header 只接受 `[A-Za-z0-9-]{1,64}`，否則產新 ID。`RequestIDFromContext` 在 handler 內回同樣值。

  Run：`make test`  Expected：FAIL。

- [ ] **Step 2：實作 errors 與 request ID**

  `errors.go` 統一 body：
  ```go
  type errorEnvelope struct { Error errorBody `json:"error"` }
  type errorBody struct { Code ErrorCode `json:"code"`; Message string `json:"message"` }
  func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
      w.Header().Set("Content-Type", "application/json; charset=utf-8")
      w.WriteHeader(status)
      _ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{code, message}})
  }
  ```
  `message` 由 handler 決定，禁止塞入 SQL 或 path；未預期錯誤一律 `CodeInternal` + `"internal error"`。

  `requestid.go` 用 `crypto/rand` 產 16 bytes 轉 base64url。middleware：讀 `X-Request-ID` header，用 regex 驗證；ok 就沿用，否則產新。把值存 `context.WithValue(ctx, requestIDKey{}, id)` 並 `w.Header().Set("X-Request-ID", id)`，然後呼叫 next。

- [ ] **Step 3：寫 CORS test**

  `cors_test.go`：
  - Preflight `OPTIONS /api/v1/photos` with `Origin: https://ok.example`、`Access-Control-Request-Method: GET`、且 origins allowlist 含它 → 204、header `Access-Control-Allow-Origin: https://ok.example`、`Access-Control-Allow-Methods: GET, HEAD, OPTIONS, POST, PATCH`、`Access-Control-Allow-Headers: Authorization, Content-Type, X-Request-ID`、`Access-Control-Allow-Credentials: true`、`Vary: Origin`。
  - Preflight 未在 allowlist origin → 403 + `Vary: Origin`；response body 空。
  - GET 帶未知 Origin → next handler 執行、但不加 `Access-Control-Allow-Origin`。
  - `origins` 為空時 middleware 不加 CORS header，直接 pass through（本地開發用）。

  Run：`make test`  Expected：FAIL。

- [ ] **Step 4：實作 CORS**

  ```go
  func WithCORS(origins []string, next http.Handler) http.Handler {
      allow := map[string]bool{}
      for _, o := range origins { allow[o] = true }
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          origin := r.Header.Get("Origin")
          if origin != "" { w.Header().Add("Vary", "Origin") }
          isPreflight := r.Method == http.MethodOptions &&
              r.Header.Get("Access-Control-Request-Method") != ""
          if origin != "" && allow[origin] {
              w.Header().Set("Access-Control-Allow-Origin", origin)
              w.Header().Set("Access-Control-Allow-Credentials", "true")
              if isPreflight {
                  w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS, POST, PATCH")
                  w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
                  w.Header().Set("Access-Control-Max-Age", "600")
                  w.WriteHeader(http.StatusNoContent); return
              }
          } else if isPreflight {
              w.WriteHeader(http.StatusForbidden); return
          }
          next.ServeHTTP(w, r)
      })
  }
  ```

- [ ] **Step 5：寫 auth middleware failing test**

  用 Task 3 的 `TestSigner` 建 verifier；預先 seed users store 一位 `uid-a`：

  ```go
  func TestWithAuthAllowsAllowlistedUID(t *testing.T) {
      v, s, done := newFixture(t); defer done()
      us := newUsersStore(t)
      _, _ = us.Add(ctx, users.User{FirebaseUID: sqlStr("uid-a"), Role: "member", Enabled: true}, now)
      handler := auth.WithAuth(v, us, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          u, ok := auth.UserFromContext(r.Context())
          if !ok || u.Role != "member" { t.Fatalf("user=%+v ok=%v", u, ok) }
          w.WriteHeader(200)
      }))
      req := httptest.NewRequest("GET", "/", nil)
      req.Header.Set("Authorization", "Bearer "+s.Sign(claimsFor("uid-a")))
      rec := httptest.NewRecorder()
      handler.ServeHTTP(rec, req)
      if rec.Code != 200 { t.Fatalf("code=%d body=%s", rec.Code, rec.Body) }
  }
  ```
  另加：
  - 缺 header → 401 + JSON envelope
  - `Bearer ` 前綴缺失 → 401
  - Token 過期 → 401
  - 有效 token 但 uid 不在 allowlist → 403
  - Token email 有效且 `email_verified=true` 且 email 在 allowlist → 200
  - Token email 有效但 `email_verified=false` → 403（fail closed）
  - 用戶 `enabled=false` → 403
  - `RequireRole("admin")` 對 member → 403、對 admin → 200

  Run：`make test`  Expected：FAIL。

- [ ] **Step 6：實作 auth middleware**

  ```go
  func WithAuth(v *Verifier, us *users.Store, next http.Handler) http.Handler {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          h := r.Header.Get("Authorization")
          if !strings.HasPrefix(h, "Bearer ") {
              httpapi.WriteError(w, 401, httpapi.CodeUnauthorized, "missing bearer token"); return
          }
          claims, err := v.Verify(r.Context(), strings.TrimPrefix(h, "Bearer "))
          if err != nil {
              // token 相關失敗一律 401，且 log 只記 request id + err type，不記 token 本身
              httpapi.WriteError(w, 401, httpapi.CodeUnauthorized, "invalid token"); return
          }
          var u users.User; var ok bool
          if claims.UID != "" {
              u, ok, err = us.LookupByUID(r.Context(), claims.UID)
          }
          if !ok && claims.EmailVerified && claims.Email != "" {
              u, ok, err = us.LookupByEmail(r.Context(), claims.Email)
          }
          if err != nil { httpapi.WriteError(w, 500, httpapi.CodeInternal, "internal error"); return }
          if !ok || !u.Enabled {
              httpapi.WriteError(w, 403, httpapi.CodeForbidden, "access denied"); return
          }
          au := AuthUser{ID: u.ID, UID: claims.UID, Email: claims.Email, Role: u.Role}
          ctx := context.WithValue(r.Context(), CtxUser, au)
          next.ServeHTTP(w, r.WithContext(ctx))
      })
  }

  func RequireRole(role string, next http.Handler) http.Handler {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          u, ok := UserFromContext(r.Context())
          if !ok || u.Role != role {
              httpapi.WriteError(w, 403, httpapi.CodeForbidden, "access denied"); return
          }
          next.ServeHTTP(w, r)
      })
  }
  ```

- [ ] **Step 7：Run tests**

  Run：`make test`  Expected：全綠。

- [ ] **Step 8：Commit**

  ```bash
  git add internal/auth internal/httpapi
  git commit -m "feat: http auth middleware, error envelope, request id, cors"
  ```

---

### Task 5：HTTP server bootstrap（serve 子命令、健康檢查、/me）

**Files：**
- Create: `internal/httpapi/router.go`
- Create: `internal/httpapi/router_test.go`
- Create: `internal/httpapi/health.go`
- Create: `internal/httpapi/me.go`
- Create: `internal/app/serve.go`
- Create: `internal/app/serve_test.go`
- Modify: `internal/app/run.go`

**Interfaces：**
- Produces:
  ```go
  type RouterDeps struct {
      Config    config.Config
      Users     *users.Store
      Catalog   *catalog.Store
      Verifier  *auth.Verifier
      Indexer   *indexer.Indexer     // for admin index-runs, filled in Task 9
      Now       func() time.Time
  }
  func NewRouter(deps RouterDeps) http.Handler
  ```
- CLI：`photo-app serve`（無其他 flag；一切靠 env）；exit code 0（正常 shutdown 收到 SIGTERM）、1（bind 失敗或 runtime error）、2（多餘 arg）。

- [ ] **Step 1：health/me test**

  `router_test.go`：組出 `NewRouter(deps)`，直接 `httptest.NewServer(router)`，做：
  - `GET /api/v1/health/live` → 200 body `{"status":"ok"}`，無需 token。
  - `GET /api/v1/health/ready` → 200 且開得起 DB ping；DB 關閉時應 503 + `service_unavailable`（或用一個 injected `healthCheck func() error`）。
  - `GET /api/v1/me` 沒 token → 401。
  - 帶正確 token 且 uid 在 allowlist → 200 body `{"uid","email","role"}`。
  - Preflight `OPTIONS /api/v1/me` from allowed origin → 204。

  Run：`make test`  Expected：FAIL。

- [ ] **Step 2：實作 router**

  用 Go 1.22 pattern：

  ```go
  func NewRouter(d RouterDeps) http.Handler {
      mux := http.NewServeMux()
      mux.HandleFunc("GET /api/v1/health/live", healthLive)
      mux.HandleFunc("GET /api/v1/health/ready", healthReady(d))

      auth := func(h http.Handler) http.Handler { return authpkg.WithAuth(d.Verifier, d.Users, h) }
      admin := func(h http.Handler) http.Handler { return auth(authpkg.RequireRole("admin", h)) }

      mux.Handle("GET /api/v1/me", auth(http.HandlerFunc(meHandler)))
      // Task 7 catalog、Task 8 media、Task 9 admin 於後續補上：
      // mux.Handle("GET /api/v1/categories", auth(http.HandlerFunc(listCategories(d))))
      // mux.Handle("POST /api/v1/admin/users", admin(http.HandlerFunc(adminAddUser(d))))
      _ = admin // 保留 alias，避免 unused

      var h http.Handler = mux
      h = httpapi.WithCORS(d.Config.AllowedOrigins, h)
      h = httpapi.WithRequestID(h)
      return h
  }
  ```

  `healthReady` 對 catalog DB ping：`d.Catalog.RawDB().PingContext(r.Context())`；失敗 503 + `CodeInternal`。

  `me.go`：讀 `UserFromContext`，序列化為 `{"uid","email","role"}`。

- [ ] **Step 3：寫 serve command test**

  以 fake `net.Listener`（`httptest.NewServer` 或 `net.Listen("tcp","127.0.0.1:0")`）驗證 `app.RunServe(ctx, env)` 能：
  - Wire router、開 listener、阻塞。
  - `ctx.Done()` 觸發後 `Shutdown` 5 秒內回傳。
  - `serve` 命令與 `index/rebuild` 共用 lock 檔案 policy：serve 不搶 `index.lock`（否則 cron 無法跑），改用 `serve.lock` 檔案，避免同時起兩個 API process。

- [ ] **Step 4：實作 serve**

  ```go
  func RunServe(ctx context.Context, env *prodEnv, stdout, stderr io.Writer) error {
      if env.cfg.FirebaseProjectID == "" {
          return errors.New("FIREBASE_PROJECT_ID is required for serve")
      }
      db, store, err := env.openDB(); if err != nil { return err }
      us := users.NewStore(db)
      v := &authpkg.Verifier{
          Issuer: env.cfg.FirebaseIssuer, Audience: env.cfg.FirebaseProjectID,
          JWKSURL: env.cfg.FirebaseJWKSURL, Refresh: time.Hour,
      }
      idx := env.buildIndexer(store)
      router := httpapi.NewRouter(httpapi.RouterDeps{
          Config: env.cfg, Users: us, Catalog: store, Verifier: v, Indexer: idx, Now: time.Now,
      })
      srv := &http.Server{Addr: env.cfg.HTTPListen, Handler: router, ReadHeaderTimeout: 10 * time.Second}
      errCh := make(chan error, 1)
      go func() { errCh <- srv.ListenAndServe() }()
      select {
      case <-ctx.Done():
          shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
          defer cancel()
          return srv.Shutdown(shutdownCtx)
      case err := <-errCh:
          if errors.Is(err, http.ErrServerClosed) { return nil }
          return err
      }
  }
  ```

  修改 `internal/app/run.go` 加 `case "serve"`；lock 改成 `serve.lock`（不要 blocking 舊 `index.lock`）。CLI 訊息：

  ```
  usage:
    photo-app index [--rebuild-thumbnails]
    photo-app rebuild
    photo-app admin <sub-command>
    photo-app serve
  ```

- [ ] **Step 5：Run + build**

  ```bash
  make test && make build
  docker run --rm -e FIREBASE_PROJECT_ID=demo -e HTTP_LISTEN=127.0.0.1:0 photo-browser:local serve
  ```
  Expected：tests PASS；build PASS；serve 在有 SIGTERM 時乾淨結束（此步驟只確認 wire 完成；完整 endpoint 在後續 task 補齊）。

- [ ] **Step 6：Commit**

  ```bash
  git add internal/httpapi internal/app
  git commit -m "feat: http serve command with health, me, cors, request id"
  ```

---

### Task 6：Cursor encoding、pagination、catalog read queries

**Files：**
- Create: `internal/httpapi/cursor.go`
- Create: `internal/httpapi/cursor_test.go`
- Create: `internal/httpapi/pagination.go`
- Create: `internal/httpapi/pagination_test.go`
- Create: `internal/catalog/query.go`
- Create: `internal/catalog/query_test.go`

**Interfaces：**
- Produces:
  ```go
  // cursor.go
  type Cursor struct { Version byte; TakenAt sql.NullString; ID int64 }
  func EncodeCursor(c Cursor) string
  func DecodeCursor(s string) (Cursor, error) // 錯回 ErrBadCursor

  // pagination.go
  type PageParams struct { Limit int; Cursor string }
  func ParsePage(q url.Values) (PageParams, error) // default 50, max 200

  // catalog/query.go
  func (s *Store) ListCategories(ctx context.Context) ([]Category, error)
  func (s *Store) GetCategory(ctx context.Context, id int64) (Category, bool, error)
  func (s *Store) ListAlbumsByCategory(ctx context.Context, categoryID int64) ([]Album, error)
  func (s *Store) ListAlbums(ctx context.Context, cur Cursor, limit int) ([]Album, Cursor, error)
  func (s *Store) GetAlbum(ctx context.Context, id int64) (Album, bool, error)
  func (s *Store) ListAlbumPhotos(ctx context.Context, albumID int64, cur Cursor, limit int) ([]Photo, Cursor, error)
  func (s *Store) ListPhotos(ctx context.Context, cur Cursor, limit int) ([]Photo, Cursor, error)
  func (s *Store) ListTimeline(ctx context.Context, cur Cursor, limit int) ([]Photo, Cursor, error) // taken_at IS NOT NULL
  func (s *Store) GetPhoto(ctx context.Context, id int64) (Photo, bool, error)
  func (s *Store) AlbumCoverThumbnail(ctx context.Context, albumID int64) (sql.NullString, error)
  ```

- [ ] **Step 1：cursor test**

  ```go
  func TestCursorRoundTrip(t *testing.T) {
      c := httpapi.Cursor{Version: 1, TakenAt: sql.NullString{String: "2024-01-01T00:00:00", Valid: true}, ID: 42}
      s := httpapi.EncodeCursor(c)
      got, err := httpapi.DecodeCursor(s)
      if err != nil || got != c { t.Fatalf("got=%+v err=%v", got, err) }
  }

  func TestCursorRejectsUnknownVersion(t *testing.T) {
      bad := base64.RawURLEncoding.EncodeToString([]byte{2, 0, 0, 0, 0})
      if _, err := httpapi.DecodeCursor(bad); err == nil { t.Fatal("expected version error") }
  }

  func TestCursorRejectsGarbage(t *testing.T) {
      if _, err := httpapi.DecodeCursor("!!!"); err == nil { t.Fatal("expected base64 error") }
      if _, err := httpapi.DecodeCursor(""); err != nil { t.Fatal("empty cursor must decode to zero") }
  }
  ```

- [ ] **Step 2：cursor 實作**

  格式：`version(1) | takenAt_present(1) | takenAt(19 bytes fixed ASCII, 0-padded) | id(int64 big-endian)`；`base64.RawURLEncoding` 編碼。

  ```go
  func EncodeCursor(c Cursor) string {
      buf := make([]byte, 1+1+19+8)
      buf[0] = c.Version
      if c.TakenAt.Valid {
          buf[1] = 1
          copy(buf[2:21], padTakenAt(c.TakenAt.String))
      }
      binary.BigEndian.PutUint64(buf[21:], uint64(c.ID))
      return base64.RawURLEncoding.EncodeToString(buf)
  }
  ```

  空 string → 回 `Cursor{}`。Version 只接受 1。

- [ ] **Step 3：pagination test + 實作**

  Test：`limit=` 缺省 50、`limit=100` 100、`limit=999` 200、`limit=-1` error、`limit=abc` error。實作 `strconv.Atoi` + clamp。

- [ ] **Step 4：catalog query test**

  用 tempdir DB seed 資料：3 categories、每 category 2 albums、每 album 5 photos（部分 `taken_at=NULL`）。驗證：
  - `ListCategories` 依 `name ASC`。
  - `ListAlbums` cursor 分頁：兩頁湊回全 6 筆、無重複、順序 `taken_at DESC NULLS LAST, id DESC`（album 排序以最大 photo `taken_at` 為 proxy → 為避免複雜，MVP 先用 `albums.updated_at DESC, id DESC`；in-code 註記 spec 允許 album cover 由查詢決定）。
  - `ListAlbumPhotos(albumID, cur, 2)` 三次呼叫、湊齊 5 筆，順序 `taken_at DESC NULLS LAST, id DESC`。
  - `ListTimeline` 只含 `taken_at IS NOT NULL`；分頁 stable。
  - `AlbumCoverThumbnail` 回 album 排序下第一筆非 null `thumbnail_key`。

- [ ] **Step 5：實作 catalog queries**

  以 `taken_at IS NULL` 排在最後：

  ```sql
  SELECT id, album_id, ... FROM photos
   WHERE album_id = ?
     AND (
       ? = 0
       OR (taken_at IS NOT NULL AND (taken_at < ? OR (taken_at = ? AND id < ?)))
       OR (taken_at IS NULL AND ? = 1 AND id < ?)
     )
   ORDER BY taken_at IS NULL, taken_at DESC, id DESC
   LIMIT ?
  ```

  Cursor bytes `Version=1`；`ID=0` 代表第一頁。Store 內部拼 args；handler 只塞 decode 結果。

- [ ] **Step 6：Commit**

  ```bash
  git add internal/httpapi internal/catalog
  git commit -m "feat: cursor pagination and catalog read queries"
  ```

---

### Task 7：Catalogue HTTP handlers

**Files：**
- Create: `internal/httpapi/catalog.go`
- Create: `internal/httpapi/catalog_test.go`
- Modify: `internal/httpapi/router.go`

**Interfaces：**
- Endpoints wired：
  ```text
  GET /api/v1/categories
  GET /api/v1/categories/{category_id}/albums
  GET /api/v1/albums?cursor=&limit=
  GET /api/v1/albums/{album_id}
  GET /api/v1/albums/{album_id}/photos?cursor=&limit=
  GET /api/v1/photos?cursor=&limit=
  GET /api/v1/photos/timeline?cursor=&limit=
  GET /api/v1/photos/{photo_id}
  ```
- Response shape：
  ```json
  {"items":[...], "next_cursor":"..."}   // 分頁 endpoint
  {"id":1,"name":"...", ...}             // 單資源
  ```

- [ ] **Step 1：table-driven handler tests**

  在 `catalog_test.go` seed 一個 sqlite fixture 三 categories，然後對 router 送 request：

  ```go
  cases := []struct{ path, wantJSONField string; wantStatus int }{
      {"/api/v1/categories", "items", 200},
      {"/api/v1/categories/1/albums", "items", 200},
      {"/api/v1/categories/9999/albums", "error", 404},
      {"/api/v1/albums", "items", 200},
      {"/api/v1/albums/1", "id", 200},
      {"/api/v1/albums/1/photos?limit=2", "next_cursor", 200},
      {"/api/v1/photos/timeline?limit=100", "items", 200},
      {"/api/v1/photos?cursor=BAD!!!", "error", 400},
  }
  ```

  另加：無 auth → 401、認證通過但 role member 存取所有 catalogue → 200（Spec 2 允許 member 看全部）。

- [ ] **Step 2：實作 handlers**

  Handler 模板：

  ```go
  func listCategories(d RouterDeps) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          cats, err := d.Catalog.ListCategories(r.Context())
          if err != nil { WriteError(w, 500, CodeInternal, "internal error"); return }
          type item struct{ ID int64; Name string; RelativePath string }
          items := make([]item, len(cats))
          for i, c := range cats { items[i] = item{c.ID, c.Name, c.RelativePath} }
          writeJSON(w, 200, map[string]any{"items": items})
      }
  }
  ```

  Album detail 額外附 `cover_thumbnail_key`（呼叫 `AlbumCoverThumbnail`）。`{photo_id}` path 用 `r.PathValue("photo_id")` + `strconv.ParseInt`。無效 int → 400。找不到 → 404。

  Router 加 `Handle("GET /api/v1/categories", auth(http.HandlerFunc(listCategories(d))))` 等 8 個 route。

- [ ] **Step 3：Run tests**

  Run：`make test`  Expected：catalog handler tests 全綠。

- [ ] **Step 4：Commit**

  ```bash
  git add internal/httpapi
  git commit -m "feat: catalog http endpoints"
  ```

---

### Task 8：Media handlers（X-Accel-Redirect）

**Files：**
- Create: `internal/httpapi/media.go`
- Create: `internal/httpapi/media_test.go`
- Modify: `internal/httpapi/router.go`

**Interfaces：**
- Endpoints：
  ```text
  GET /api/v1/photos/{photo_id}/thumbnail/{thumbnail_key}
  GET /api/v1/photos/{photo_id}/original
  ```
- Response（成功）：204 是不夠的—— Nginx `X-Accel-Redirect` 期望 upstream 回 200/2xx + `X-Accel-Redirect: /internal-media/.../<path>`；backend 不寫 body。

- [ ] **Step 1：寫 media handler tests**

  ```go
  func TestThumbnailRedirectHappyPath(t *testing.T) {
      d := seed(t) // photo id=1, relative_path="旅遊/日本/a.jpg", thumbnail_key="1-100-200"
      req := signedGET(t, d, "/api/v1/photos/1/thumbnail/1-100-200")
      rec := httptest.NewRecorder()
      httpapi.NewRouter(d).ServeHTTP(rec, req)
      if rec.Code != 200 { t.Fatalf("code=%d", rec.Code) }
      if got := rec.Header().Get("X-Accel-Redirect"); got != "/internal-media/thumbnails/1-100-200.webp" {
          t.Fatalf("redirect=%q", got)
      }
      if rec.Header().Get("Cache-Control") != "private, max-age=86400, immutable" {
          t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
      }
      if rec.Header().Get("ETag") != `"1-100-200"` { t.Fatalf("etag=%q", rec.Header().Get("ETag")) }
      if rec.Body.Len() != 0 { t.Fatalf("body not empty: %q", rec.Body) }
  }
  ```

  另加：
  - key mismatch（DB current key ≠ URL key）→ 404 `not_found`（不洩漏正確 key）。
  - Photo not found → 404。
  - key 含 `/` 或 `..` → 400。
  - Original endpoint 回 `X-Accel-Redirect: /internal-media/originals/<url-escaped relative_path>`；`ETag: "size-mtime"`；`Cache-Control: private, max-age=3600`；`Content-Type` 由 `photo.mime_type`。
  - `photo.relative_path` 含中文與空格 → URL escape 正確（單元測試比對）。
  - Photo `thumbnail_key IS NULL` → 404（thumbnail 尚未產生）。

- [ ] **Step 2：實作 media handler**

  ```go
  func photoThumbnail(d RouterDeps) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          photoID, ok := parseID(r.PathValue("photo_id"))
          key := r.PathValue("thumbnail_key")
          if !ok || filepath.Base(key) != key || key == "" { WriteError(w,400,CodeBadRequest,"bad request"); return }
          p, found, err := d.Catalog.GetPhoto(r.Context(), photoID)
          if err != nil { WriteError(w,500,CodeInternal,"internal error"); return }
          if !found || !p.ThumbnailKey.Valid || p.ThumbnailKey.String != key {
              WriteError(w,404,CodeNotFound,"not found"); return
          }
          w.Header().Set("X-Accel-Redirect", path.Join(d.Config.InternalThumbnails, key+".webp"))
          w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
          w.Header().Set("ETag", fmt.Sprintf("%q", key))
          w.Header().Set("X-Content-Type-Options", "nosniff")
          w.WriteHeader(200)
      }
  }
  ```

  `photoOriginal`：檢查 relative_path 是相對、不含 `..`、不含 `\x00`；`url.PathEscape` 每段後 `path.Join(d.Config.InternalOriginals, escaped)`。`ETag: fmt.Sprintf("\"%d-%d\"", p.FileSize, p.FileMTimeNS)`。

- [ ] **Step 3：Router wiring**

  ```go
  mux.Handle("GET /api/v1/photos/{photo_id}/thumbnail/{thumbnail_key}",
      auth(http.HandlerFunc(photoThumbnail(d))))
  mux.Handle("GET /api/v1/photos/{photo_id}/original",
      auth(http.HandlerFunc(photoOriginal(d))))
  ```

  再手動確認：`HEAD /api/v1/photos/1/original` 也走 GET handler（Go 1.22 `HEAD` 自動 fallback），輸出 headers、無 body。

- [ ] **Step 4：Commit**

  ```bash
  git add internal/httpapi
  git commit -m "feat: media redirects via nginx internal locations"
  ```

---

### Task 9：Admin HTTP endpoints + 非同步 index run

**Files：**
- Create: `internal/httpapi/admin.go`
- Create: `internal/httpapi/admin_test.go`
- Modify: `internal/indexer/indexer.go` — 抽出 `RunWithScanID(ctx, scanID, opts)`；既有 `Run` 內部改成 `StartScan` → `RunWithScanID` 的組合。
- Modify: `internal/catalog/store.go` — 加 `GetScanRun(ctx, id) (ScanRun, bool, error)` 與 `ScanRun` model。
- Modify: `internal/httpapi/router.go` — wire admin routes。

**Interfaces：**
- Endpoints：
  ```text
  GET   /api/v1/admin/users
  POST  /api/v1/admin/users     body {"firebase_uid?","email?","role":"admin|member"}
  PATCH /api/v1/admin/users/{user_id}   body {"role?":"...","enabled?":true|false}
  POST  /api/v1/admin/index-runs        body {"rebuild_thumbnails?":true}   -> 202 + {"scan_id":n}
  GET   /api/v1/admin/index-runs/{scan_id}   -> {"id","status","counts":{...},"started_at","finished_at"}
  ```

- [ ] **Step 1：抽 indexer 非同步入口**

  修改 `internal/indexer/indexer.go`：把既有 `Run` 拆成：

  ```go
  func (i *Indexer) Run(ctx context.Context, opts Options) (catalog.ScanCounts, error) {
      scanID, err := i.Store.StartScan(ctx, time.Now())
      if err != nil { return catalog.ScanCounts{}, err }
      return i.RunWithScanID(ctx, scanID, opts)
  }
  ```

  `RunWithScanID(ctx, scanID, opts)` 執行既有 loop + reconcile + FinishScan defer 邏輯。**原有 test 必須全部繼續 pass**——先跑 `make test`。

- [ ] **Step 2：admin handler tests**

  Seed users store (admin + member)、seed catalog、build router：

  - `GET /admin/users` 由 member → 403。由 admin → 200 list。
  - `POST /admin/users` 缺 `firebase_uid` 且缺 `email` → 400。
  - `POST /admin/users` 重複 → 409。
  - `PATCH /admin/users/{id}` 改 role → 200；未知 id → 404。
  - `POST /admin/index-runs`：
    - 第一次呼叫 → 202、body `scan_id`、進 goroutine 執行；用 fake indexer inject 一個 channel 確認被呼叫。
    - 同時第二次呼叫 → 409（lock 尚未釋放）。
  - `GET /admin/index-runs/{scan_id}` 已完成 scan → 200 + status/counts。

- [ ] **Step 3：實作 admin handlers**

  ```go
  func startIndexRun(d RouterDeps, sem chan struct{}) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          select {
          case sem <- struct{}{}:
          default:
              WriteError(w,409,CodeConflict,"index already running"); return
          }
          var body struct{ RebuildThumbnails bool `json:"rebuild_thumbnails"` }
          _ = json.NewDecoder(r.Body).Decode(&body)
          scanID, err := d.Catalog.StartScan(r.Context(), d.Now())
          if err != nil { <-sem; WriteError(w,500,CodeInternal,"internal error"); return }
          go func() {
              defer func() { <-sem }()
              ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
              defer cancel()
              _, _ = d.Indexer.RunWithScanID(ctx, scanID, indexer.Options{RebuildThumbnails: body.RebuildThumbnails})
          }()
          writeJSON(w, 202, map[string]any{"scan_id": scanID})
      }
  }
  ```

  `sem` 是 `chan struct{}` cap=1，在 `NewRouter` 建；同時作為 admin single-flight guard。**注意**：這與 `photo-app index` CLI 共存時，兩者的協調依然靠 `serve.lock`+`index.lock` 之外的一層——實務上 serve process 執行 index 期間，若外部 cron 呼 `photo-app index` 會拿不到 lock 而 exit 3，這是正確行為。

  `POST /admin/users` handler：
  ```go
  var body struct{ FirebaseUID, Email, Role string }
  if err := json.NewDecoder(r.Body).Decode(&body); err != nil { WriteError(w,400,CodeBadRequest,"bad json"); return }
  if body.Role == "" { body.Role = "member" }
  u := users.User{ Role: body.Role, Enabled: true }
  if body.FirebaseUID != "" { u.FirebaseUID = sql.NullString{String: body.FirebaseUID, Valid: true} }
  if body.Email != "" {
      u.Email = sql.NullString{String: body.Email, Valid: true}
      u.NormalizedEmail = sql.NullString{String: users.NormalizeEmail(body.Email), Valid: true}
  }
  id, err := d.Users.Add(r.Context(), u, d.Now())
  if errors.Is(err, users.ErrDuplicate) { WriteError(w,409,CodeConflict,"user already exists"); return }
  if errors.Is(err, users.ErrInvalidRole) { WriteError(w,400,CodeBadRequest,"invalid role"); return }
  if err != nil { WriteError(w,500,CodeInternal,"internal error"); return }
  writeJSON(w, 201, map[string]any{"id": id})
  ```

- [ ] **Step 4：Router wire**

  ```go
  sem := make(chan struct{}, 1)
  mux.Handle("GET /api/v1/admin/users", admin(http.HandlerFunc(listAdminUsers(d))))
  mux.Handle("POST /api/v1/admin/users", admin(http.HandlerFunc(addAdminUser(d))))
  mux.Handle("PATCH /api/v1/admin/users/{user_id}", admin(http.HandlerFunc(patchAdminUser(d))))
  mux.Handle("POST /api/v1/admin/index-runs", admin(http.HandlerFunc(startIndexRun(d, sem))))
  mux.Handle("GET /api/v1/admin/index-runs/{scan_id}", admin(http.HandlerFunc(getIndexRun(d))))
  ```

- [ ] **Step 5：Run tests**

  Run：`make test`  Expected：全綠，包含 indexer 既有 test。

- [ ] **Step 6：Commit**

  ```bash
  git add internal/httpapi internal/indexer internal/catalog
  git commit -m "feat: admin http endpoints and async index runs"
  ```

---

### Task 10：Nginx integration、testauth binary、docker-compose acceptance

**Files：**
- Create: `cmd/testauth/main.go`
- Create: `deploy/nginx/nginx.conf`
- Create: `deploy/compose/docker-compose.acceptance.yml`
- Create: `scripts/acceptance_api.sh`
- Modify: `Dockerfile` — 加 `testauth` build 與 `api-acceptance` stage
- Modify: `Makefile` — 加 `make acceptance-api`

**Interfaces：**
- `testauth`：獨立 binary。`GET /jwks` 回 JWKS；`GET /mint?sub=<uid>&email=<addr>&verified=1&exp=3600` 回一段 JWT。私鑰在 process 內生成；`/jwks` 每次回同一組。用固定 seed 讓 acceptance 可重跑。
- Nginx：
  ```nginx
  upstream photo_backend { server backend:8080; }
  server {
    listen 80;
    location / {
      proxy_pass http://photo_backend;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Request-ID $http_x_request_id;
    }
    location /internal-media/originals/ {
      internal;
      autoindex off;
      alias /srv/photos/;
    }
    location /internal-media/thumbnails/ {
      internal;
      autoindex off;
      alias /srv/thumbnails/;
    }
  }
  ```

- [ ] **Step 1：實作 testauth binary**

  `cmd/testauth/main.go`：

  ```go
  func main() {
      signer := auth.NewTestSigner("test-kid-1")
      mux := http.NewServeMux()
      mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
          w.Header().Set("Content-Type", "application/json")
          _, _ = w.Write(signer.JWKS())
      })
      mux.HandleFunc("GET /mint", func(w http.ResponseWriter, r *http.Request) {
          q := r.URL.Query()
          claims := map[string]any{
              "iss":            os.Getenv("MINT_ISSUER"),
              "aud":            os.Getenv("MINT_AUDIENCE"),
              "sub":            q.Get("sub"),
              "email":          q.Get("email"),
              "email_verified": q.Get("verified") == "1",
              "iat":            time.Now().Unix(),
              "exp":            time.Now().Add(time.Hour).Unix(),
          }
          if q.Get("exp") == "-1" { claims["exp"] = time.Now().Add(-time.Minute).Unix() }
          fmt.Fprint(w, signer.Sign(claims))
      })
      http.ListenAndServe(":8090", mux)
  }
  ```

- [ ] **Step 2：Dockerfile 加 stage**

  ```dockerfile
  FROM build AS api-build
  ARG TARGETARCH=amd64
  ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOAMD64=v1
  RUN go build -trimpath -ldflags='-s -w' -o /out/testauth ./cmd/testauth

  FROM debian:bookworm-slim AS api-acceptance
  RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq sqlite3 && rm -rf /var/lib/apt/lists/*
  COPY --from=build /out/photo-app /usr/local/bin/photo-app
  COPY --from=api-build /out/testauth /usr/local/bin/testauth
  COPY deploy/nginx/nginx.conf /etc/nginx/nginx.conf
  ```

  runtime image **不** 含 testauth。

- [ ] **Step 3：docker-compose**

  `deploy/compose/docker-compose.acceptance.yml`：

  ```yaml
  services:
    testauth:
      image: photo-browser-api-acceptance
      command: /usr/local/bin/testauth
      environment:
        MINT_ISSUER: https://securetoken.google.com/demo-proj
        MINT_AUDIENCE: demo-proj
      ports: ["8090:8090"]

    backend:
      image: photo-browser-api-acceptance
      command: /usr/local/bin/photo-app serve
      environment:
        PHOTO_ROOT: /srv/photos
        DATA_DIR: /srv/data
        THUMBNAIL_DIR: /srv/thumbnails
        HTTP_LISTEN: :8080
        FIREBASE_PROJECT_ID: demo-proj
        FIREBASE_JWKS_URL: http://testauth:8090/jwks
        ALLOWED_ORIGINS: http://frontend.local
        INTERNAL_MEDIA_ORIGINALS: /internal-media/originals
        INTERNAL_MEDIA_THUMBNAILS: /internal-media/thumbnails
      volumes:
        - ../../test-photos:/srv/photos:ro
        - photo-data:/srv/data
        - photo-thumbs:/srv/thumbnails
      depends_on: [testauth]

    nginx:
      image: nginx:1.26
      volumes:
        - ../../deploy/nginx/nginx.conf:/etc/nginx/nginx.conf:ro
        - ../../test-photos:/srv/photos:ro
        - photo-thumbs:/srv/thumbnails:ro
      ports: ["8081:80"]
      depends_on: [backend]

  volumes:
    photo-data: {}
    photo-thumbs: {}
  ```

  Nginx `upstream backend` 直接靠 compose network 名稱解析。

- [ ] **Step 4：acceptance_api.sh**

  流程：
  1. `docker compose build` 用剛 build 的 image。
  2. `docker compose up -d`；`until curl -sf http://localhost:8081/api/v1/health/live; do sleep 1; done`（最多 30 秒）。
  3. 使用 `docker compose exec backend photo-app admin add-user --uid=alice --role=member` 加 allowlist。
  4. 也建一個 admin：`--uid=root --role=admin`。
  5. 觸發 index：`docker compose exec backend photo-app index`（一次同步 scan，確保後續 GET 有資料）。
  6. Mint token：`TOK=$(curl -s "http://localhost:8090/mint?sub=alice&verified=1")`。
  7. 驗證：
     - `curl -sf -H "Authorization: Bearer $TOK" http://localhost:8081/api/v1/me | jq -e '.uid == "alice"'`。
     - `curl -s http://localhost:8081/api/v1/me | grep -q 'unauthorized'`（無 token → 401）。
     - `NOTOK=$(curl -s "http://localhost:8090/mint?sub=nobody&verified=1")`；`curl -s -H "Authorization: Bearer $NOTOK" http://localhost:8081/api/v1/photos | jq -e '.error.code=="forbidden"'`。
     - `EXPIRED=$(curl -s "http://localhost:8090/mint?sub=alice&verified=1&exp=-1")`；帶它 → 401。
     - Member GET `/api/v1/admin/users` → `.error.code=="forbidden"`。
     - Admin token → 200，`items` 至少含 alice 與 root。
     - 直接 `curl -s http://localhost:8081/internal-media/thumbnails/somekey.webp` → Nginx 回 404（`internal` 拒絕）。
     - `PHOTO_ID=$(curl -s -H "Authorization: Bearer $TOK" http://localhost:8081/api/v1/photos?limit=1 | jq '.items[0].id')`；
       `THUMB_KEY=$(curl -s -H "Authorization: Bearer $TOK" http://localhost:8081/api/v1/photos/$PHOTO_ID | jq -r '.thumbnail_key')`；
       `curl -sf -H "Authorization: Bearer $TOK" -o /tmp/thumb.webp "http://localhost:8081/api/v1/photos/$PHOTO_ID/thumbnail/$THUMB_KEY"`；
       `file /tmp/thumb.webp | grep -q "Web/P"` 確認 Nginx 有真的把 bytes 送出。
     - Original endpoint 也拉一次，`file` 出 JPEG。
     - `curl -sI -H "Authorization: Bearer $TOK" -H "Range: bytes=0-99" ".../original"` → 206（Nginx range）。
     - Traversal 攻擊：`curl -s -H "Authorization: Bearer $TOK" "http://localhost:8081/api/v1/photos/$PHOTO_ID/thumbnail/..%2f..%2fetc%2fpasswd"` → 400 或 404，且 body 不含 `/etc`。
     - `docker compose logs backend | grep -qv "Bearer "` 確認未把 token 印出。
  8. 觸發 admin async scan：`curl -sf -X POST -H "Authorization: Bearer $ADMIN_TOK" http://localhost:8081/api/v1/admin/index-runs | jq -e '.scan_id'`。
  9. `docker compose down -v`。

  Script 使用 `set -euo pipefail` 與 `fail()` helper。

- [ ] **Step 5：Makefile**

  ```make
  api-acceptance: build
  	docker build --target api-acceptance -t photo-browser-api-acceptance .
  	bash scripts/acceptance_api.sh
  ```

- [ ] **Step 6：完整驗收**

  ```bash
  make test
  make acceptance          # Spec 1 既有
  make api-acceptance      # 新
  make build
  ```

  Expected：全部 exit 0。

- [ ] **Step 7：Commit**

  ```bash
  git add cmd/testauth deploy scripts/acceptance_api.sh Dockerfile Makefile
  git commit -m "test: end-to-end auth+api+nginx acceptance"
  ```

---

## 最終驗證矩陣

| Spec 2 驗收要求 | 實作 Task | 證據 |
|---|---:|---|
| Firebase ID Token 驗證 | 3 | `internal/auth` unit tests |
| UID + email_verified allowlist | 1、4 | users store + auth middleware tests |
| Admin bootstrap 無 unauth endpoint | 2 | admin CLI 測試 |
| /me、/health/* | 5 | router 測試 |
| Categories/Albums/Photos/Timeline | 6、7 | catalog handler 測試 |
| Cursor pagination | 6 | cursor round-trip + handler 分頁測試 |
| Media X-Accel-Redirect | 8 | media handler 單元測試 + api-acceptance |
| Thumbnail key mismatch → 404 | 8 | media_test |
| Nginx internal 拒絕直連 | 10 | api-acceptance direct-curl 驗證 |
| Range delivery | 10 | api-acceptance HEAD/Range |
| CORS 精確 origin | 4、10 | cors_test + acceptance preflight |
| Error envelope 不洩漏 | 4、8 | errors_test + traversal 測試 |
| Log 不含 token | 4、10 | acceptance `docker compose logs` grep |
| Admin manual scan → 202 + 409 | 9 | admin_test + api-acceptance |
| Rebuild 保留 users | 1、既有 | users 表未在 ClearCatalogue 刪除清單 |

## 執行順序

Task 1 → 10 循序。Task 3 可與 Task 2 平行（無 shared file），但 subagent 執行時建議循序，方便 review。

## 明確延後

- Cloudflare Tunnel、compose production 版本、resource 量測 → Spec 4。
- Frontend、Timeline UI、Firebase client wiring → Spec 3。
- Per-Album/Per-Photo ACL、public sharing、多 role → 未列入。

