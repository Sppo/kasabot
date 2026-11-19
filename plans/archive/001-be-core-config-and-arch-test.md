# План #001: [BE-Core] Каркас Go, Конфіг .env, Спільні DRY-хелпери та Автотест Архітектури (arch_test.go)

- **Статус:** `Закрито / В архіві` *(виправлення за [001a](./001a-fix-makefile-and-arch-test-review.md) виконані, `make check` пройдено)*
- **Модуль / Область:** `core` (Backend Foundation & Architecture Guardrails)

## 1. Мета задачі
Створити базовий фундамент Go-бекенду (`go.mod`, `.env.example`, `Makefile`), єдину точку зчитування конфігурації (`internal/core/config.go`), єдиний реєстр кодів помилок (`internal/core/errors.go`), спільні DRY-утиліти для HTTP та контексту (`internal/core/httputil.go`), а також автоматичний тест архітектури `arch_test.go`, який на кожному `go test ./...` перевіряє дотримання правил із `ARCHITECTURE_RULES.md` для Go та React.

## 2. Файли для створення або зміни
- `go.mod` — ініціалізація модуля `kasabot` (Go 1.23, на цьому кроці лише стандартна бібліотека без зовнішніх залежностей).
- `.env.example` — повний перелік операційних параметрів із детальними коментарями українською мовою (одиниці виміру та значення за замовчуванням).
- `Makefile` — команди `make check` (`go vet ./...`, `go test ./...`, `go build ./...`), `make dev`, `make tunnel`, `make build`.
- `internal/core/config.go` — єдина точка зчитування `.env` та змінних середовища (`os.Getenv`), парсинг і валідація операційних таймаутів, лімітів аудіо, квот AI, TTL токенів, дефолтної валюти й таймзони.
- `internal/core/errors.go` — єдиний реєстр констант машиночитаних кодів помилок (`ERR_UNAUTHORIZED`, `ERR_FORBIDDEN`, `ERR_INVALID_CREDENTIALS`, `ERR_EMAIL_TAKEN`, `ERR_NOT_FOUND`, `ERR_VALIDATION`, `ERR_SUBSCRIPTION_EXPIRED`, `ERR_AI_QUOTA_EXCEEDED`, `ERR_AUDIO_TOO_LARGE`, `ERR_UNSUPPORTED_MEDIA`, `ERR_AI_PARSE_FAILED`, `ERR_INTERNAL`).
- `internal/core/httputil.go` — спільні DRY-хелпери для всіх майбутніх хендлерів і модулів (`WriteJSON`, `WriteError`, `DecodeJSON`, типізовані ключі контексту `WithUserID` / `UserIDFromCtx`, `WithWorkspaceID` / `WorkspaceIDFromCtx`).
- `internal/core/config_test.go` — unit-тести зчитування `.env`, дефолтних значень, валідації та HTTP/контекст хелперів.
- `arch_test.go` — Full-Stack автотест архітектури на стандартних пакетах `go/parser` та `go/ast`:
  1. Заборона імпорту `internal/modules/*` всередині `internal/core`.
  2. Заборона крос-імпортів між модулями `internal/modules/<A>` -> `internal/modules/<B>`.
  3. Заборона імпорту `"database/sql"`, `"net/http"` або `"gopkg.in/telebot.v3"` у `service.go` та заборона `"database/sql"` у `*_handler.go`.
  4. Заборона виклику `os.Getenv` у будь-якому `.go` файлі поза `internal/core/config.go` (і тестовими `*_test.go`).
  5. Сканування `web/src/` (коли папка з'явиться): заборона крос-імпортів `modules/<A>` -> `modules/<B>`, прямих `fetch(` / `indexedDB` у `components/*.tsx` та `import.meta.env` поза `web/src/core/config.ts`.

## 3. Покроковий план реалізації
1. [x] Створити `go.mod` (`module kasabot`, `go 1.23`), `.env.example` (з українськими коментарями до кожної змінної) та `Makefile`.
2. [x] Реалізувати `internal/core/errors.go` з усіма доменними константами `ERR_*`.
3. [x] Реалізувати `internal/core/config.go` з парсером `.env`-файлу та змінних оточення, дефолтами й валідацією.
4. [x] Реалізувати `internal/core/httputil.go` (`WriteJSON`, `WriteError`, `DecodeJSON`, контекст `UserID` та `WorkspaceID`).
5. [x] Написати unit-тести `internal/core/config_test.go` для перевірки конфігу та `httputil.go`.
6. [x] Реалізувати `arch_test.go` у корені репозиторію для автоматичної перевірки всіх архітектурних меж.

### 3.1. Доопрацювання за результатами Code Review (Пункти 1–7)
7. [x] **Безпека `JWT_SECRET` (`internal/core/config.go`):** прибрати небезпечний дефолт, зробити `JWT_SECRET` обов'язковим (`>= 16` символів) і явно відхиляти плейсхолдер `change-me-in-production-min-16-chars`.
8. [x] **Суворий `DecodeJSON` (`internal/core/httputil.go`, `.env.example`, `config.go`):** додати `MAX_JSON_BODY_BYTES` у `.env.example` / `Config`, обмеження розміру тіла (`http.MaxBytesReader`), виклик `dec.DisallowUnknownFields()` та перевірку відсутності хвоста після першого JSON-об'єкта (`io.EOF`).
9. [x] **Надійний парсер `.env` без мутації глобального `os.Setenv` (`internal/core/config.go`):** парсити `.env` у локальну `map[string]string` (пріоритет: непорожній `os.Getenv` -> `dotEnvMap` -> `fallback`), підтримати префікс `export `, inline-коментарі `# ...` та зняття лише збалансованих парних лапок `"..."` / `'...'`.
10. [x] **Валідація валюти `DEFAULT_CURRENCY` (`internal/core/config.go`):** перевіряти, що код складається рівно з 3 латинських літер `A–Z` (відхиляти цифри на кшталт `123`).
11. [x] **Виправлення `Makefile`:** використати змінну `PORT ?= 8080` у `make tunnel` та додати перевірку наявності `./cmd/app` у `build` і `dev`.
12. [x] **Посилення `arch_test.go`:**
    - Відстежувати аліаси пакету `"os"` та `"syscall"` (включно з `import o "os"`, `import . "os"`, викликами `Getenv`, `LookupEnv`, `Environ`).
    - Додати правило для `repository.go` у `internal/modules/*`: заборона імпорту `"net/http"` та `"gopkg.in/telebot.v3"`.
    - На фронтенді перевіряти також `import '...modules/...'` (side-effect), `import('...modules/...')` (dynamic import), а заборону прямих `fetch(` / `indexedDB` поширити і на `*Page.tsx`, і на `components/*.tsx`.
13. [x] **Розширення unit-тестів (`internal/core/config_test.go`):** додати тести на обов'язковий/плейсхолдерний `JWT_SECRET`, inline-коментарі/лапки/`export` у `.env`, невалідну валюту `123`, а також тести `DecodeJSON` (невідомі поля, зайвий хвіст, перевищення ліміту розміру).

### 3.2. Повне закриття прогалин `arch_test.go` та негативні фікстури (Code Review #2, Пункти 1–9)
14. [x] **Точний `SkipDir` та маски файлів шарів у `arch_test.go`:**
    - Пропускати `web`, `bin`, `dist` лише в корені репозиторію (`relPath == "web"` тощо), а не на будь-якій глибині.
    - Прив'язати правила шарів до масок файлів: `service*.go` (Шар 2), `*handler*.go` (Шар 1), `repo*.go` (Шар 3), а також вимагати наявності `service_test.go`, якщо в модулі є `service.go`.
15. [x] **Повний список заборонених пакетів та функцій оточення (`os.ExpandEnv`, `os.Expand`):**
    - У `service*.go` заборонити `database/sql*`, `modernc.org/sqlite*`, `net/http*`, `github.com/go-chi/*`, `telebot*`.
    - У `*handler*.go` заборонити `database/sql*`, `modernc.org/sqlite*`.
    - У `repo*.go` заборонити `net/http*`, `github.com/go-chi/*`, `telebot*`.
    - Додати `ExpandEnv` та `Expand` до списку заборонених викликів `os` поза `internal/core/config.go`.
16. [x] **Контроль звернень між шарами всередині одного Go-пакета (Пункт #6):**
    - В AST файлів `*handler*.go` заборонити будь-які посилання на типи чи конструктори репозиторію (`Repository`, `SQLiteRepository`, `NewRepository`, `NewSQLiteRepository`), щоб контролер не міг обійти `Service` всередині одного пакета модуля.
17. [x] **Перевірка правил SQL, Грошей, Логування та Кодів помилок в AST (`arch_test.go`, Пункт #7):**
    - Заборонити `fmt.Print*` (`Println`, `Printf`, `Print`) у продакшн `.go` файлах (дозволено тільки `log/slog`).
    - Заборонити рядкові літерали `"ERR_..."` у будь-якому `.go` файлі поза `internal/core/errors.go`.
    - Заборонити `float32` / `float64` для грошових полів (`Amount*`, `Balance*`, `Total*`, `Price*`) у структурах `model*.go` та `FLOAT`/`REAL` у SQL-міграціях.
    - Перевіряти SQL у модулях (`internal/modules/<mod>/`): префікс таблиць модуля у `CREATE TABLE` (`fin_` для `finance`), заборона `SELECT *`, заборона `fmt.Sprintf` із SQL-ключовими словами (`SELECT`/`INSERT`/`UPDATE`/`DELETE`), обов'язкова наявність `workspace_id` у SQL-запитах `repo*.go`.
18. [x] **Посилення перевірок Фронтенду (`arch_test.go`, Пункти #4, #5, #7):**
    - Розв'язувати відносні шляхи імпортів (`../notes/x`, `./...`) через `filepath.Clean`: заборонити імпорт `web/src/modules/*` із `web/src/core/*`, а також відносні імпорти між різними модулями `web/src/modules/<A>` -> `web/src/modules/<B>`.
    - Очищати JS/TS коментарі (`//` та `/* */`) перед скануванням, додати `axios` до заборонених викликів (`\b(fetch|axios|indexedDB)\b`) у View-файлах (`components/*.tsx` та `*Page.tsx`).
    - Перевіряти `.tsx` файли на відсутність захардкодженого кириличного тексту в JSX-тегах та атрибутах `placeholder`/`title`/`aria-label` (мусить використовуватись `t('...')`).
19. [x] **Негативні фікстури для самоперевірки `arch_test.go` (Пункт #8 та #9):**
    - Винести перевірку в чисті функції `checkGoArchitecture(rootDir string) []string` та `checkFrontendArchitecture(rootDir string) []string`.
    - Додати у `arch_test.go` табличний тест `TestArchitectureValidatorCatchesViolations`, який на тимчасовому дереві `t.TempDir()` створює негативні фікстури на кожне з правил (крос-імпорт модулів, `core -> module`, хендлер викликає `Repository` в одному пакеті, `service_x.go` імпортує `net/http/httputil`, `os.ExpandEnv`, `fmt.Println`, `"ERR_..."` поза `errors.go`, `float64` для суми, `SELECT *`, SQL без `workspace_id`, відносний імпорт `../otherModule`, `axios` у `Page.tsx`, хардкод тексту в JSX) і перевіряє, що валідатор гарантовано ловить кожне порушення.
20. [x] **Дрібні покращення `Makefile` та парсера `.env`:**
    - У `Makefile` зчитувати `PORT` із `.env` (якщо файл існує) для `make tunnel`.
    - У `config.go` (`parseDotEnvValue`) дозволити лапки всередині неекранованих значень без зовнішніх лапок, якщо значення не починається з лапки, або повертати помилку тільки коли значення починається з незакритої лапки.

## 4. План перевірки та Архітектурний аудит (Verification & Self-Audit)
- [x] Компіляція та автотест архітектури (`go vet ./...`, `go test -v ./...` вкл. з `arch_test.go`, `go build ./...`).
- [x] **3-шарова ізоляція:** `arch_test.go` перевіряє правила ізоляції шарів.
- [x] **Немає дублів методів (DRY):** усі спільні HTTP та контекстні методи винесені в `internal/core/httputil.go`.
- [x] **Нуль хардкоду:** усі операційні таймаути, ліміти, TTL та квоти винесені в `.env.example` з коментарем і зчитуються виключно через `internal/core/config.go`.
- [x] **Мультимовність (`i18n` `uk` + `en`):** усі помилки в `httputil.go` повертають машиночитаний `error_code` з `internal/core/errors.go`.
