# План #002: [BE-Core] PostgreSQL (pgx), Раннер Міграцій та Інтерфейс Модуля

- **Статус:** `Закрито / В архіві`
- **Дата:** 2026-11-20
- **Модуль / Область:** `core` (Database & Module Contract)

## 1. Мета задачі
Підключити PostgreSQL через `pgx` (пул `pgxpool`), створити системні таблиці ядра (`users`, `workspaces`, `workspace_members`, `workspace_invites`, `telegram_link_tokens`, `schema_migrations`), транзакційний раннер міграцій модулів (тільки вперед, із захистом від паралельного запуску) та контракт `core.Module` з реєстром модулів. Забезпечити локальну розробку без купівлі сервера: PostgreSQL піднімається локально в Docker.

## 2. Файли для створення або зміни
- `go.mod` / `go.sum` — залежності: `github.com/jackc/pgx/v5` (драйвер і пул), `github.com/go-chi/chi/v5`, `gopkg.in/telebot.v3`.
- `docker-compose.yml` — **новий файл** (захищений тип, потрібен явний дозвіл у цьому плані): локальний PostgreSQL з фіксованою мажорною версією, порт лише на `127.0.0.1`, іменований том, логін і пароль із `.env`.
- `internal/core/module.go` — `Migration`, інтерфейс `Module` (`Name`, `Migrations`, `RegisterRoutes`, `RegisterBot`, `OnWorkspaceCreated`) та `ModuleRegistry`.
- `internal/core/database.go` — `OpenDB(ctx, cfg)` (створення `pgxpool`, `Ping`, параметри пулу), `initCoreSchema`, `RunModuleMigrations`.
- `internal/core/database_test.go` — інтеграційні тести на реальному Postgres.
- `internal/core/config.go`, `config_test.go`, `.env.example` — замість `DB_PATH` та `DB_BUSY_TIMEOUT_MS`: `DATABASE_URL`, `DB_MAX_CONNS`, `DB_CONNECT_TIMEOUT_SEC` (дефолти як іменовані константи, коментарі українською).
- `arch_test.go` — додати `github.com/jackc/pgx/...` до пакетів, заборонених для `service.go` та хендлерів; оновити фікстури.
- `Makefile` — цілі `db-up`, `db-down` для локального Postgres (запускає користувач).

## 3. Покроковий план реалізації
1. [x] **Залежності:** додати `pgx/v5`, `chi/v5`, `telebot.v3` у `go.mod`.
2. [x] **Конфіг:** `DATABASE_URL` (обов'язковий), `DB_MAX_CONNS`, `DB_CONNECT_TIMEOUT_SEC`; видалити `DB_PATH` і `DB_BUSY_TIMEOUT_MS`; оновити тести й `.env.example`.
3. [x] **`docker-compose.yml` + `Makefile`:** локальний Postgres, `make db-up` / `make db-down`.
4. [x] **`module.go`:** інтерфейс `Module`, `Migration`, `ModuleRegistry` з перевіркою унікальності імен.
5. [x] **`database.go`:**
   - `OpenDB`: `pgxpool`, `Ping` із таймаутом, обмеження кількості з'єднань.
   - `initCoreSchema`: системні таблиці за `SPEC.md` (типи PostgreSQL: `BIGINT GENERATED ALWAYS AS IDENTITY`, `TIMESTAMPTZ`, `BOOLEAN`), `schema_migrations(module, version, applied_at)`.
   - `RunModuleMigrations`: послідовність версій `> 0`, кожна міграція в окремій транзакції, захист від паралельного запуску через `pg_advisory_lock`.
6. [x] **Тести БД:** кожен запуск створює тимчасову схему (`CREATE SCHEMA` з унікальним ім'ям та `search_path`) і видаляє її в кінці; перевірити системні таблиці, ідемпотентність міграцій, відкат при помилці в SQL та паралельний запуск міграцій.
7. [x] **`arch_test.go`:** додати `pgx` у заборонені для сервісів/хендлерів; тест із фікстурою порушення.

## 4. План перевірки та Архітектурний аудит (Verification & Self-Audit)
- [x] `make check` пройшов без помилок (`gofmt -l .`, `go vet ./...`, `go test -v ./...` вкл. з `arch_test.go`, `go build ./...`) при запущеному локальному Postgres (`make db-up`); без цього статус не змінювати на `Виконано — очікує перевірки користувачем`.
- [x] **3-шарова ізоляція:** `internal/core` не імпортує `internal/modules/*`; сервіси та хендлери не імпортують `pgx`.
- [x] **DRY:** єдина точка ініціалізації БД та міграцій у `internal/core/database.go`.
- [x] **Нуль хардкоду:** `DATABASE_URL` та параметри пулу беруться з `*core.Config` (без `os.Getenv`).
- [x] **Безпека:** параметризовані запити (`$1...`), порт Postgres слухає лише `127.0.0.1`, пароль не потрапляє в логи.
- [x] У підсумку вставлено фактичний вивід `make check`.

## 5. Рішення користувача (2026-11-20)
1. Docker Desktop встановлено, тому використовуємо `docker-compose.yml`. У Docker вже є інші контейнери (MySQL на `3307`, mailpit на `8025` тощо), тому ім'я контейнера має бути унікальним (`kasabot-postgres`), а порт хоста береться з `.env` (`POSTGRES_PORT`), щоб уникати конфліктів.
2. Тести БД, якщо Postgres не запущений: **падають з чітким повідомленням** («запусти `make db-up`»), а не пропускаються, щоб зелений результат не був хибним.
3. Мажорну версію PostgreSQL реалізатор фіксує сам (поточна стабільна) і вказує її в підсумку.

## 6. Доопрацювання за ревю
1. [x] **Надійне зняття `pg_advisory_lock` (`internal/core/database.go`):** перевіряти результат `pg_advisory_unlock` (окремий `context.WithTimeout`, не скасований `ctx`); при помилці або `false` — знищувати з'єднання (`conn.Hijack()` + `Close`), щоб замок не залипав у пулі. Тести: перевірка через `pg_try_advisory_lock` з іншого з'єднання після `RunModuleMigrations`, а також випадок скасованого `ctx`.
2. [x] **`initCoreSchema` під тим самим `pg_advisory_lock` (`internal/core/database.go`):** винести спільний внутрішній хелпер `withAdvisoryLock` і використати його і для `initCoreSchema`, і для `RunModuleMigrations`. Тест: N паралельних `initCoreSchema` на порожній тимчасовій схемі без помилок.
3. [x] **Оновлення залежностей (`go.mod`, `go.sum`):** оновити `pgx/v5`, `chi/v5`, `telebot.v3` до найновіших стабільних у межах поточних мажорних версій + `go mod tidy` + перевірка `govulncheck` (якщо встановлено).
4. [x] **`docker-compose.yml`:** зробити `POSTGRES_PASSWORD` обов'язковим (`${POSTGRES_PASSWORD:?задай POSTGRES_PASSWORD у .env}`), зберігши дефолти для інших змінних, `127.0.0.1` та healthcheck.
