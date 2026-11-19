# HARD ARCHITECTURE BOUNDARIES (DO NOT VIOLATE)

This file defines non-negotiable architectural, security, and data invariants for **KasaBot**. Every rule below is enforced by automated architecture tests (`arch_test.go`), unit tests, and build checks (`go test ./...`, `npm run build`).

---

## 1. Backend Architecture (Go)

1. **Strict Module Isolation (`NEVER` cross-import modules):**
   - Code inside `internal/modules/<A>` **MUST NEVER** import `internal/modules/<B>`.
   - Cross-module communication is **ONLY** allowed via interfaces or events defined in `internal/core`.

2. **Core Independence (`NEVER` import modules into core):**
   - `internal/core` **MUST NEVER** import anything from `internal/modules/*`.
   - Dependencies flow strictly one way: `cmd/app` -> `internal/modules/*` -> `internal/core`.

3. **Strict 3-Layer Module Separation (`Handler -> Service -> Repository`):**
   - **Handlers (`http_handler.go`, `bot_handler.go`):** **MUST NEVER** import `"database/sql"` or `github.com/jackc/pgx/*`, or access `Repository` directly. Handlers **ONLY** parse input, call `service.go`, and return JSON/Telegram responses. Zero SQL or business math inside handlers.
   - **Services (`service.go`):** **MUST NEVER** import `"database/sql"`, `github.com/jackc/pgx/*`, `"net/http"`, or `"gopkg.in/telebot.v3"`. Services contain pure business logic and talk to the database **ONLY** via the module's `Repository` interface (this interface is an explicit architectural requirement for layer isolation and unit testing).
   - **Repositories (`repository.go`):** Contain **ONLY** parameterized SQL queries and struct mapping. Every query on module data **MUST** filter by `WHERE workspace_id = $n` and specify columns explicitly (avoid `SELECT *`).

4. **Database Prefixing, PostgreSQL & Migrations:**
   - **Prefixing:** Every table created by module `finance` **MUST** start with `fin_` (`fin_categories`, `fin_transactions`). Modules **MUST NOT** query or mutate tables owned by `core` or other modules.
   - **PostgreSQL Connection:** `database.go` **MUST** use `pgxpool` (`github.com/jackc/pgx/v5`) configured only from `Config` (`DATABASE_URL`, pool size, connect timeout), verify the connection with `Ping` on startup, and use types `BIGINT GENERATED ALWAYS AS IDENTITY` for ids, `TIMESTAMPTZ` for timestamps and `BOOLEAN` for flags. The database server is a separate service (locally via Docker Compose); it **MUST** listen only on `127.0.0.1` in development.
   - **Migrations:** Tracked in `schema_migrations (module TEXT, version INTEGER, applied_at TIMESTAMPTZ, PRIMARY KEY(module, version))`. Migrations are strictly forward-only, sequential, and wrapped in transactions (PostgreSQL supports transactional DDL); the runner **MUST** take a `pg_advisory_lock` so concurrent starts do not apply the same migration twice. **NEVER** edit an already-applied migration; create a new version instead.

5. **Operational Config (`.env`) vs. Domain Constants (Code):**
   - **Single Config Entrypoint:** **NEVER** call `os.Getenv` outside `internal/core/config.go`.
   - **What goes into `.env` / `.env.example` (Operational Config):** ports, database connection (`DATABASE_URL`) and pool size (`DB_MAX_CONNS`), timeouts (`AI_REQUEST_TIMEOUT_SEC`, `HTTP_TIMEOUT_SEC`, `DB_CONNECT_TIMEOUT_SEC`), byte limits (`MAX_AUDIO_SIZE_BYTES`), quotas/TTLs (`TRIAL_DAYS`, `TRIAL_DAILY_AI_LIMIT`, `ACTIVE_DAILY_AI_LIMIT`, `JWT_TTL_HOURS`), AI model name (`GEMINI_MODEL`), default timezone and currency. Every `.env.example` variable **MUST** have a Ukrainian comment with units and defaults.
   - **What stays in Code (Domain Constants):** error codes (`ERR_*` in `internal/core/errors.go`), table names, role names (`owner`, `member`), transaction types (`expense`, `income`), and system category keys (`groceries`, `fuel`, etc.) are typed Go/TS constants — **NEVER** put domain constants into `.env`.

6. **Money, Errors, Logging & DRY:**
   - **Money:** **NEVER** use `FLOAT` / `REAL` / `float64` for storing money in the database. Always store integer minor units (`amount_minor BIGINT`, e.g., kopiykas).
   - **Errors (`internal/core/errors.go`):** Wrap internal errors with `fmt.Errorf("context: %w", err)`. Map all client-facing errors to constants in `internal/core/errors.go` (`ERR_*`), mirrored 1:1 in `uk.ts` and `en.ts`.
   - **Logging (`log/slog`):** Use standard `log/slog` (`workspace_id`, `user_id`, duration, `error_code`). **NEVER** use `fmt.Println` or log passwords, JWT tokens, API keys, or raw audio bytes.
   - **DRY Helpers:** Reuse `WriteJSON`, `WriteError`, `DecodeJSON`, `UserIDFromCtx`, `WorkspaceIDFromCtx` from `internal/core/httputil.go`. `http_handler.go` and `bot_handler.go` **MUST** call the exact same `Service` methods.

---

## 2. Security, Idempotency & Quotas

1. **SQL & Role Authorization:**
   - **ALWAYS** use parameterized SQL queries (`$1, $2, ...` placeholders) — **NEVER** format SQL with `fmt.Sprintf` or string concatenation.
   - Verify membership in `workspace_members` on every workspace request. Owner-only operations (creating invites, deleting workspace) **MUST** verify `role = 'owner'`.

2. **Audio Validation & Prompt Injection Defense:**
   - Enforce `http.MaxBytesReader` using `MAX_AUDIO_SIZE_BYTES` and validate audio MIME type against an explicit allowlist (`audio/ogg`, `audio/webm`, `audio/mp4`, `audio/mpeg`, `audio/wav`, `audio/aac`, `audio/x-m4a`).
   - Pass user text/audio strictly in the `user` content block (never concatenated into `systemInstruction`). Enforce Gemini `responseSchema` with a strict category `enum` and validate parsed output in `service.go` (`amount_minor > 0`, category belongs to workspace).

3. **Offline Queue Idempotency & AI Quota Fairness:**
   - **Idempotency:** Every offline/mobile parse request may include an `idempotency_key` (UUIDv4). `fin_transactions` stores `idempotency_key` (or deduplicates per workspace) so retries from `OfflineQueue` **NEVER** create duplicate transactions or charge AI quota twice.
   - **Quota Deduction:** Increment `workspaces.ai_requests_today` **ONLY** after a successful response from Gemini API (do not deduct user quota on network timeouts or `5xx` upstream failures).

---

## 3. Frontend Architecture (React + TypeScript + Capacitor)

1. **No Cross-Module Imports & Strict 3-Layer Split:**
   - Files in `web/src/modules/<A>` **MUST NEVER** import from `web/src/modules/<B>`. Shared utilities (`formatMoneyMinor`, `formatDate`, `getCategoryLabel`) live in `web/src/core/utils`.
   - **Components (`components/*.tsx`, `*Page.tsx`):** Pure UI. **MUST NEVER** call `fetch`, `axios`, or `IndexedDB` directly.
   - **Hooks (`hooks/`):** State, `OfflineQueue` (with UUID `idempotency_key`), `haptics`, and `402` handling.
   - **API (`api.ts`):** Typed requests via `web/src/core/api`.

2. **Mandatory Bilingual i18n (`uk` + `en`) & Single Config:**
   - **NEVER** hardcode user-facing Ukrainian or English text in `.tsx`. Use `t('...')` and define every key in **both** `uk.ts` and `en.ts` (enforced by TypeScript `TranslationSchema`).
   - **NEVER** access `import.meta.env` outside `web/src/core/config.ts`.

3. **Native App Store Safety (`Capacitor`):**
   - **NEVER** render direct external payment links (WayForPay / Monobank checkout URLs) when `Capacitor.isNativePlatform()` is `true`.

---

## 4. Automated Enforcement (`arch_test.go`, Unit Tests & `make check`)

1. **`arch_test.go` (Go + React AST/Source Scanner):**
   - Fails if `internal/core` imports `internal/modules/*` or `modules/<A>` imports `modules/<B>`.
   - Fails if `service.go` or `*_handler.go` imports `"database/sql"` or `github.com/jackc/pgx/*` (or `service.go` imports `"net/http"` / `"gopkg.in/telebot.v3"`).
   - Fails if `os.Getenv` is used outside `internal/core/config.go` or `import.meta.env` outside `web/src/core/config.ts`.
   - Fails if `web/src/modules/<A>` imports `web/src/modules/<B>` or `components/*.tsx` calls `fetch(` / `indexedDB`.
2. **Unit Tests (`service_test.go`):** New business logic in `service.go` **MUST** be covered by table-driven unit tests using a mock `Repository` and mock `core.AI`.
