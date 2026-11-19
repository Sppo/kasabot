# План #001: [BE-Core] Каркас Go, Конфіг .env, Спільні DRY-хелпери та Автотест Архітектури (arch_test.go)

- **Статус:** `Чернетка — очікує підтвердження`
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
1. [ ] Створити `go.mod` (`module kasabot`, `go 1.23`), `.env.example` (з українськими коментарями до кожної змінної) та `Makefile`.
2. [ ] Реалізувати `internal/core/errors.go` з усіма доменними константами `ERR_*`.
3. [ ] Реалізувати `internal/core/config.go` з парсером `.env`-файлу та змінних оточення, дефолтами й валідацією.
4. [ ] Реалізувати `internal/core/httputil.go` (`WriteJSON`, `WriteError`, `DecodeJSON`, контекст `UserID` та `WorkspaceID`).
5. [ ] Написати unit-тести `internal/core/config_test.go` для перевірки конфігу та `httputil.go`.
6. [ ] Реалізувати `arch_test.go` у корені репозиторію для автоматичної перевірки всіх архітектурних меж.

## 4. План перевірки та Архітектурний аудит (Verification & Self-Audit)
- [ ] Компіляція та автотест архітектури (`go vet ./...`, `go test -v ./...` вкл. з `arch_test.go`, `go build ./...`).
- [ ] **3-шарова ізоляція:** `arch_test.go` перевіряє правила ізоляції шарів.
- [ ] **Немає дублів методів (DRY):** усі спільні HTTP та контекстні методи винесені в `internal/core/httputil.go`.
- [ ] **Нуль хардкоду:** усі операційні таймаути, ліміти, TTL та квоти винесені в `.env.example` з коментарем і зчитуються виключно через `internal/core/config.go`.
- [ ] **Мультимовність (`i18n` `uk` + `en`):** усі помилки в `httputil.go` повертають машиночитаний `error_code` з `internal/core/errors.go`.
