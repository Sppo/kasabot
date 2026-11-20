# План #003b: Реструктуризація `internal/core` на підпакети (без зміни поведінки)

- **Статус:** `Закрито / В архіві`
- **Модуль / Область:** `core`

## 1. Мета задачі / Першопричина
У `internal/core` накопичилося 17 файлів в одному пакеті `core`, і попереду додавання `ai.go`, `bot.go`, `app.go` (План `#004`). Поки модулі (`internal/modules/finance`) ще не імпортують `core`, необхідно розділити `internal/core` на логічні підпакети з однонаправленим графом залежностей (без циклічних імпортів) та без жодних змін бізнес-логіки чи API-контрактів.

Цільова структура підпакетів (DAG залежностей):
1. `internal/core/apperr` (`errors.go`) — коди помилок `ERR_*` та `AppError` (без внутрішніх залежностей).
2. `internal/core/config` (`config.go`, `config_test.go`) — єдина точка зчитування `.env` та `os.Getenv` (без внутрішніх залежностей).
3. `internal/core/reqctx` (`reqctx.go`, виділений з `httputil.go`) — ключі та хелпери `context.Context` (`UserIDFromCtx`, `WorkspaceIDFromCtx`, `WorkspaceRoleFromCtx`, `RequestIDFromCtx` та `With*`) для розриву циклу між `logging` та `httpx`.
4. `internal/core/module` (`module.go`) — `Migration`, `Module`, `ModuleRegistry` (без внутрішніх залежностей).
5. `internal/core/logging` (`logger.go`, `logger_test.go`) — `Logger`, `RequestLoggingMiddleware`, щоденні файли та `error_id` (залежить від `apperr`, `config`, `reqctx`).
6. `internal/core/httpx` (`httputil.go`) — `ErrorResponse`, `WriteJSON`, `WriteError`, `WriteErrorWithID`, `WriteAppError`, `DecodeJSON` (залежить від `apperr`, `logging`, `reqctx`).
7. `internal/core/db` (`database.go`, `store.go`, `database_test.go`) — `pgxpool`, міграції, системні моделі (`User`, `Workspace`), `CoreStore`, `PGStore` (залежить від `apperr`, `config`, `logging`, `module`).
8. `internal/core/accounts` (`auth.go`, `workspace.go`, `middleware.go`, `auth_handler.go`, `auth_test.go`, `workspace_test.go`, `middleware_test.go`) — бізнес-логіка авторизації/воркспейсів, middleware та HTTP-хендлери (залежить від `apperr`, `config`, `db`, `httpx`, `logging`, `module`, `reqctx`; **не імпортує** `pgx` чи `database/sql`).

## 2. Файли для створення або зміни
- `internal/core/apperr/errors.go` — перенесений `errors.go` (`package apperr`).
- `internal/core/config/config.go`, `internal/core/config/config_test.go` — перенесені конфіг і тести (`package config`).
- `internal/core/reqctx/reqctx.go` — контекстні хелпери, виділені з `httputil.go` (`package reqctx`).
- `internal/core/module/module.go` — перенесений `module.go` (`package module`).
- `internal/core/logging/logger.go`, `internal/core/logging/logger_test.go` — перенесені логер і тести (`package logging`).
- `internal/core/httpx/httputil.go` — перенесені JSON/Error HTTP-хелпери (`package httpx`).
- `internal/core/db/database.go`, `internal/core/db/store.go`, `internal/core/db/database_test.go` — перенесені БД-шар, `CoreStore`/`PGStore` і тести (`package db`).
- `internal/core/accounts/auth.go`, `workspace.go`, `middleware.go`, `auth_handler.go`, `auth_test.go`, `workspace_test.go`, `middleware_test.go` — перенесені сервіси, middleware, хендлери та тести (`package accounts`).
- Видалення старих файлів у корені `internal/core/*.go` після перенесення.
- `arch_test.go` — оновлення шляхів (`internal/core/config/config.go`, `internal/core/apperr/errors.go`), перевірка заборони SQL у `internal/core/accounts/*` та правил залежностей підпакетів `core`, оновлення тестів сканера.
- `ARCHITECTURE_RULES.md`, `.env.example` — оновлення згадок шляхів `internal/core/...`.

## 3. Покроковий план реалізації
1. [x] Перенести базові пакети без внутрішніх залежностей: `apperr`, `config`, `reqctx`, `module`.
2. [x] Перенести `logging` та `httpx`, підключивши імпорти `apperr`, `config`, `reqctx`.
3. [x] Перенести `db` (`database.go`, `store.go`, `database_test.go`), експортувавши необхідні поля/конструктори та оновивши імпорти.
4. [x] Перенести `accounts` (`auth.go`, `workspace.go`, `middleware.go`, `auth_handler.go` + `*_test.go`), оновивши імпорти та посилання на типи з `db`, `config`, `apperr`, `httpx`, `logging`, `module`, `reqctx`.
5. [x] Видалити старі файли з кореня `internal/core/*.go`.
6. [x] Оновити `arch_test.go` (нові шляхи винятків для `config.go` та `errors.go`, заборона SQL у `internal/core/accounts/`, оновлення тестових фікстур сканера).
7. [x] Оновити згадки шляхів у `ARCHITECTURE_RULES.md` та `.env.example`.
8. [x] Запустити `make check` (`gofmt`, `go vet`, `go test -v ./...`, `go build ./...`) і переконатися, що всі тести проходять без змін поведінки.

## 4. План перевірки та Архітектурний аудит (Verification & Self-Audit)
- [x] `make check` пройшов без помилок (`go vet ./...`, `go test ./...` вкл. з `arch_test.go`, `go build ./...`, `npm run build`); без цього статус не змінювати на `Виконано — очікує перевірки користувачем`
- [x] **Ізоляція шарів `core`:** `internal/core/accounts/*` не імпортує `pgx` або `database/sql`; `apperr`, `config`, `reqctx`, `module` не імпортують інші підпакети `internal/core`.
- [x] **Немає дублів методів (DRY):** контекстні хелпери не дублюються між `reqctx`, `httpx` і `logging`; HTTP-відповіді використовують `httpx`.
- [x] **Нуль хардкоду:** `os.Getenv` залишається виключно в `internal/core/config/config.go`; літерали `ERR_*` — виключно в `internal/core/apperr/errors.go`.
- [x] **Повна сумісність поведінки:** усі існуючі unit- та інтеграційні тести (`config`, `logging`, `db`, `accounts`, `arch_test.go`) проходять без послаблення перевірок.

## 6. Доопрацювання за код-ревю
Основна міграція прийнята (набір із 27 тестів збігається, логіка не змінена, граф без циклів). Єдине відхилення від плану: `store.go` (разом із типами `User`, `Workspace`, інтерфейсом `CoreStore`, `PGStore` і константами ролей та статусів підписки) опинився в `internal/core/db/`, а за розділом 1 він має бути в `internal/core/accounts/`. Причина вибору: `db` лишається суто інфраструктурним (пул, схема ядра, міграції), а доменні типи і SQL користувачів/воркспейсів належать `accounts`, і назви в API читаються як `accounts.Workspace`, а не `db.Workspace`. Користуються цим файлом лише файли `accounts/`, тож перенесення чисте.

1. [x] Перенести `internal/core/db/store.go` → `internal/core/accounts/store.go` (`package accounts`); прибрати префікс `db.` у `accounts/*.go` та їхніх тестах для `User`, `Workspace`, `CoreStore`, `PGStore`, `NewPGStore`, `Role*`, `PlanStatus*`. У `db/` лишаються `database.go` і `database_test.go`. Тіла функцій і SQL не змінювати.
2. [x] `arch_test.go`: у `internal/core/accounts/` дозволено імпорт `pgx`/`pgxpool` **лише в `store.go`** (як у плані); у решти файлів `accounts/` (`auth.go`, `workspace.go`, `middleware.go`, `auth_handler.go`) заборонено. Додати фікстуру: `accounts/store.go` з `pgx` — без порушень, `accounts/auth.go` з `pgx` — `[CORE-LAYER]`. У графі залежностей `db` більше не повинен імпортувати нічого з `accounts`, а `accounts` лишається залежним від `db`, якщо потрібні `OpenDB`/міграції; якщо не потрібні — прибрати зайве ребро з `isAllowedCoreEdge`.
3. [x] Документація: `ARCHITECTURE_RULES.md` (рядок про ізоляцію SQL у core: SQL/pgx лише в `internal/core/db/database.go` та `internal/core/accounts/store.go`), `SPEC.md` (дерево: `store.go` під `accounts/`, прибрати з `db/`; прибрати «типи User, Workspace» біля `db`).
4. [x] `make check` без помилок; набір тестів лишається тим самим (27, ті самі імена); `go test -race -count=1 ./internal/core/...`.
