# PLAN.md — Дорожня карта та Управління планами KasaBot

> **Правило для AI-агента (згідно з [AGENTS.md](./AGENTS.md) та [ARCHITECTURE_RULES.md](./ARCHITECTURE_RULES.md)):**
> 1. Кожен план виконується окремо для Бекенду (`[BE-*]`) або Фронтенду (`[FE-*]`), з мінімальним diff (2–5 файлів за раз) і повним покриттям тестами.
> 2. Перед стартом чергового плану створи файл `plans/<NNN>-<short-name>.md` за шаблоном [plans/TEMPLATE.md](./plans/TEMPLATE.md), запиши його в блок **«Поточна активна задача»** нижче і **запитай дозволу у користувача**.
> 3. Після виконання коду та `make check` відміть чекбокси в `plans/<NNN>-<short-name>.md` і чекай перевірки користувачем.
> 4. Тільки після команди користувача **«перенеси план в архівні»** — перемісти файл у `plans/archive/<NNN>-<short-name>.md`, додай його в таблицю архіву і відміть `[x]` у дорожній карті нижче.

---

## Поточна активна задача (Active Task)

- **Статус:** `Немає активної задачі (наступний: План #004d — тюнінг Gemini, далі #004b Telegram, #004c app.go)`
- **Файл плану:** —
- **Короткий опис:** —

---

## Архів виконаних планів (`plans/archive/`)

| № | Назва задачі | Модуль / Область | Файл в архіві | Статус |
|---|--------------|------------------|---------------|--------|
| 000 | Проєктування архітектури, специфікації та правил агентів | `docs` / `rules` | [000-architecture-and-specs.md](./plans/archive/000-architecture-and-specs.md) | Закрито |
| 001 | Каркас Go, Конфіг `.env`, `errors.go`, `httputil.go` та `arch_test.go` | `BE-Core` | [001-be-core-config-and-arch-test.md](./plans/archive/001-be-core-config-and-arch-test.md) | Закрито |
| 001a | Виправлення за код-ревю (`Makefile`, `arch_test.go`, `config.go`) | `BE-Core` | [001a-fix-makefile-and-arch-test-review.md](./plans/archive/001a-fix-makefile-and-arch-test-review.md) | Закрито |
| 002a | Правила та документація: перехід на PostgreSQL і нове правило залежностей | `docs` / `rules` | [002a-rules-postgres-and-dependencies.md](./plans/archive/002a-rules-postgres-and-dependencies.md) | Закрито |
| 003a | Логування у файли по днях, окремий файл помилок та `error_id` | `BE-Core` | [003a-be-core-logging.md](./plans/archive/003a-be-core-logging.md) | Закрито |
| 002 | PostgreSQL (`pgx`), Раннер Міграцій та Інтерфейс Модуля | `BE-Core` | [002-be-core-db-and-migrations.md](./plans/archive/002-be-core-db-and-migrations.md) | Закрито |
| 003 | Авторизація (JWT), Workspaces (інвайти), Telegram-токени та квоти підписки/AI | `BE-Core` | [003-be-core-auth-and-workspaces.md](./plans/archive/003-be-core-auth-and-workspaces.md) | Закрито |
| 003b | Реструктуризація `internal/core` на підпакети (без зміни поведінки) | `BE-Core` | [003b-be-core-restructure.md](./plans/archive/003b-be-core-restructure.md) | Закрито |
| 004a | Універсальний клієнт Gemini AI (`internal/core/ai`) | `BE-Core` | [004a-be-core-ai-client.md](./plans/archive/004a-be-core-ai-client.md) | Закрито |

---

## Детальна дорожня карта проєкту (12 ізольованих планів: Backend окремо, Frontend окремо)

### Етап 0. Проєктування та правила (Завершено)
- [x] Продуктовий опис та стратегія монетизації ([GUIDE.md](./GUIDE.md))
- [x] Технічна специфікація Mobile-First Modular Monolith ([SPEC.md](./SPEC.md))
- [x] Жорсткі архітектурні межі та правила агентів ([ARCHITECTURE_RULES.md](./ARCHITECTURE_RULES.md), [AGENTS.md](./AGENTS.md))

---

### Частина А: Бекенд — Ядро (`internal/core`) та Модуль Фінансів (`internal/modules/finance`)

#### [x] План `#001` (`[BE-Core]`): Каркас Go, Конфіг `.env` та Автотест Архітектури (`arch_test.go`)
- **Файли:** `go.mod`, `.env.example`, `Makefile`, `arch_test.go`, `internal/core/config.go`, `internal/core/errors.go`, `internal/core/httputil.go`, `internal/core/config_test.go`.
- **Зміст:**
  - Єдина точка зчитування `.env` (`config.go`) та коментований `.env.example`.
  - Єдиний реєстр кодів помилок (`errors.go`) та спільні DRY-хелпери (`httputil.go`: `WriteJSON`, `WriteError`, `DecodeJSON`, контекст).
  - Full-Stack автотест `arch_test.go` (перевірка імпортів модулів, відсутності SQL у сервісах/хендлерах, заборони `os.Getenv` / `import.meta.env` поза конфігами, заборони прямого `fetch` у React-компонентах).
- **Перевірка:** `go test ./...` проходить успішно.

#### [x] План `#002a` (`[docs]`): Правила та документація: перехід на PostgreSQL і нове правило залежностей
- **Файли:** `AGENTS.md`, `ARCHITECTURE_RULES.md`, `SPEC.md`, `PLAN.md`. Коду не змінює. План: [002a-rules-postgres-and-dependencies.md](./plans/archive/002a-rules-postgres-and-dependencies.md).

#### [x] План `#002` (`[BE-Core]`): PostgreSQL (`pgx`), Раннер Міграцій та Інтерфейс Модуля
- **Файли:** `go.mod`, `docker-compose.yml`, `Makefile`, `internal/core/module.go`, `internal/core/database.go`, `internal/core/database_test.go`, `internal/core/config.go`, `.env.example`, `arch_test.go`. План: [002-be-core-db-and-migrations.md](./plans/archive/002-be-core-db-and-migrations.md).
- **Зміст:**
  - Підключення PostgreSQL через `pgx/v5` (`pgxpool`), локально в Docker (`make db-up`).
  - Таблиця `schema_migrations` (міграції модулів тільки вперед у транзакції, `pg_advisory_lock`).
  - Системні таблиці ядра: `users`, `workspaces`, `workspace_members`, `workspace_invites`, `telegram_link_tokens`.
  - Контракт `core.Module` та реєстр модулів.
- **Перевірка:** інтеграційні тести на реальному Postgres (системні таблиці, ідемпотентність міграцій, відкат, паралельний запуск; `make check`).

#### [x] План `#003` (`[BE-Core]`): Авторизація (JWT), Workspaces (Інвайти) та Квоти Підписки/AI ([архів](./plans/archive/003-be-core-auth-and-workspaces.md))
- **Файли:** `internal/core/accounts/auth.go`, `internal/core/accounts/workspace.go`, `internal/core/accounts/auth_test.go`.
- **Зміст:**
  - Реєстрація та логін (`bcrypt` + `JWT`), авто-створення дефолтного воркспейсу з 14-денним `trial` та викликом `OnWorkspaceCreated`.
  - Middleware авторизації та перевірки членства/ролі (`owner` | `member`) у `workspace_members`.
  - Інвайт-коди для приєднання партнера/дружини до воркспейсу та одноразові токени прив'язки Telegram.
  - Перевірка активності підписки (`402 Payment Required` для Read-Only режиму) та денних квот AI (`429 Too Many Requests` зі списанням квоти лише після успішної відповіді AI).
- **Перевірка:** unit-тести реєстрації, логіну, інвайтів, ролей та блокування `402` / `429`.

#### [x] План `#003b` (`[BE-Core]`): Реструктуризація `internal/core` на підпакети (без зміни поведінки) ([архів](./plans/archive/003b-be-core-restructure.md))
- **Коли:** після закриття `#003` і **до** `#004`/`#005` (поки модулі не імпортують `core`).
- **Мета:** замість 20+ файлів в одній папці — підпакети за відповідальністю (орієнтовно `config`, `logging`, `db`, `auth`, `workspace`, `httpx`, `apperr`), щоб зорієнтуватися в коді було легко, а `arch_test.go` міг забороняти небажані імпорти між ними.
- **Умови:** жодних змін логіки; лише переміщення, імпорти та оновлення `arch_test.go`, `ARCHITECTURE_RULES.md`, `SPEC.md`; тести проходять до і після (`make check`); без циклічних імпортів.

#### [x] План `#003a` (`[BE-Core]`): Логування у файли по днях, окремий файл помилок та `error_id` ([архів](./plans/archive/003a-be-core-logging.md))
- **Файли:** `internal/core/logger.go`, `internal/core/logger_test.go`, `internal/core/config.go`, `internal/core/config_test.go`, `.env.example`.
- **Зміст:**
  - `slog` (JSON) у `logs/app-<день>.log`, помилки додатково в `logs/errors-<день>.log`, ротація за `LOG_RETENTION_DAYS`.
  - Короткий `error_id` та `request_id` для пошуку повних логів за скороченим повідомленням; HTTP-middleware.
  - Хук для Telegram-сповіщень (саме сповіщення робляться в Плані `#004`).
- **Перевірка:** unit-тести запису по днях, видалення старих файлів та відсутності секретів у логах (`make check`).

#### [x] План `#004a` (`[BE-Core]`): Універсальний Клієнт Gemini AI (`internal/core/ai`) ([архів](./plans/archive/004a-be-core-ai-client.md))
- **Файли:** `internal/core/ai/client.go`, `internal/core/ai/gemini.go`, `internal/core/ai/client_test.go`, `internal/core/apperr/errors.go`. План: [004a-be-core-ai-client.md](./plans/archive/004a-be-core-ai-client.md).
- **Зміст:**
  - Інтерфейс `ai.Client` (текст або аудіо + системний промпт + `responseSchema` → суворий JSON), реалізація на `net/http` без SDK.
  - Перевірка `MAX_AUDIO_SIZE_BYTES` та білого списку MIME-типів, захист від prompt injection (вміст користувача лише в ролі `user`), дата/час у `DEFAULT_TIMEZONE`, код `ERR_AI_UNAVAILABLE`; помилки не списують квоту.
- **Перевірка:** unit-тести з `httptest`-сервером; ручна перевірка `webm`/`mp4` із реальним ключем.

#### [ ] План `#004d` (`[BE-Core]`): Тюнінг параметрів генерації Gemini (після ручної перевірки #004a)
- **Коли:** після кроку 7 Плану #004a (ручна перевірка `webm`/`mp4` з реальним ключем) і до фінансового модуля (#006), коли видно реальні відповіді моделі.
- **Файли (орієнтовно):** `internal/core/ai/gemini.go`, `internal/core/ai/client_test.go`, за потреби `internal/core/config/config.go` та `.env.example`.
- **Результати ручної перевірки, які входять сюди (2026-11-20):**
  - Змінити типове `GEMINI_MODEL` з `gemini-2.5-flash` (знята для нових ключів, `404`) на `gemini-3.8-flash` у `config.go`, `.env.example`, `SPEC.md` і тестах конфігу; додати необов'язкову запасну модель `GEMINI_FALLBACK_MODEL` на випадок, коли основна перевантажена або знята.
  - `404` від Gemini означає «модель не існує», тобто помилку налаштування (як `401`/`403`): мапити на `500 ERR_INTERNAL` з `error_id` і логом `gemini_model_not_found`, а не на `422 ERR_AI_PARSE_FAILED`.
  - `503 high demand` трапляється реально: вирішити, чи робити в клієнті 1–2 швидкі повтори з короткою паузою (в межах `AI_REQUEST_TIMEOUT_SEC`) до того, як віддати `ERR_AI_UNAVAILABLE`; повтор не повинен списувати квоту.
  - У відповіді було 83 токени «міркування» на 15 токенів відповіді: вимкнути або обмежити (`thinkingConfig`), замірити вплив на якість і затримку.
- **Зміст:** підібрати за результатами ручних запусків: `temperature` (для розбору витрат бажано низьку, 0–0.2), `maxOutputTokens` і вимкнення «thinking» (`thinkingBudget: 0`) для `gemini-2.5-flash`, щоб зменшити вартість і затримку без втрати якості розбору. Параметри, які користувач захоче міняти без перекомпіляції, виносимо в `.env` з коментарями.
- **Перевірка:** тест, що параметри потрапляють у запит; ручне порівняння кількох фраз до/після.

#### [ ] План `#004b` (`[BE-Core]`): Опціональний Telegram-конектор (`internal/core/bot`)
- **Файли:** `internal/core/bot/bot.go`, `internal/core/bot/notifier.go`, `internal/core/bot/bot_test.go`.
- **Зміст:**
  - Безпечний старт без `TG_BOT_TOKEN`, обробка `/start link_<token>` через `accounts.AuthService.ConsumeTelegramLinkToken`, відповідь неприв'язаному `telegram_id` з інструкцією.
  - Асинхронний відправник скорочених сповіщень про помилки (`ErrorHook` логера): канал з обмеженою ємністю, воркер із `context`/graceful shutdown, повільний Telegram не блокує запити; у сповіщенні `error_id`, час, код помилки, без особистих даних.
- **Перевірка:** unit-тести з мок-API Telegram: прив'язка, безпечний старт, переповнення черги сповіщень без блокування.

#### [ ] План `#004c` (`[BE-Core]`): Контейнер `app.go`, HTTP-сервер та `cmd/app/main.go`
- **Файли:** `internal/core/app/app.go`, `internal/core/app/app_test.go`, `cmd/app/main.go`.
- **Зміст:**
  - Складання Chi-роутера: CORS, request-логування, `accounts.Routes()`, монтування модулів під `/api/v1/<module>` з `Auth` + `Workspace` middleware, запуск міграцій і сіду, graceful shutdown (HTTP, воркер сповіщень, логер).
  - Рішення про `GEMINI_API_KEY` при старті: зараз його відсутність дає `503 ERR_AI_UNAVAILABLE` лише під час `Parse`; визначити, чи це фатально для запуску (за замовчуванням: ні, сервер стартує, а в лог іде попередження; ручне додавання витрат працює без AI).
  - Нотатки з ревю #003: довірені проксі й реальний IP клієнта (`.env`) для блокування входу за `cloudflared`; rate-limit на публічні маршрути (`register`, `login`); 5xx відповіді з `error_id`.
- **Перевірка:** тести ініціалізації `App` і middleware-ланцюжка; ручний цикл `curl`: реєстрація → вхід → `/auth/me` → інвайт → join.

#### [ ] План `#005` (`[BE-Finance]`): Модуль Фінансів — Шар 3: Моделі, Міграції та Репозиторій БД (`repository.go`)
- **Файли:** `internal/modules/finance/model.go`, `internal/modules/finance/repository.go`, `internal/modules/finance/repository_test.go`.
- **Зміст:**
  - Міграції таблиць `fin_categories` та `fin_transactions` (суми в копійках `amount_minor`, поле `idempotency_key`).
  - Сід базових системних категорій витрат і доходів для нового воркспейсу.
  - Інтерфейс `Repository` та PostgreSQL-реалізація на `pgx` (параметризовані SQL-запити з обов'язковим `WHERE workspace_id = $n`, дедуплікація за `idempotency_key`, агрегація `summary` та розбивка по категоріях за `day` / `week` / `month`).
- **Перевірка:** інтеграційні тести репозиторію на реальному локальному PostgreSQL у тимчасовій схемі (`make db-up`, `go test ./internal/modules/finance/...`).

#### [ ] План `#006` (`[BE-Finance]`): Модуль Фінансів — Шар 2 (`service.go`) + Шар 1 (`http_handler.go`, `bot_handler.go`) + `cmd/app/main.go`
- **Файли:** `internal/modules/finance/service.go`, `internal/modules/finance/service_test.go`, `internal/modules/finance/http_handler.go`, `internal/modules/finance/bot_handler.go`, `internal/modules/finance/module.go`, `cmd/app/main.go`.
- **Зміст:**
  - Чиста бізнес-логіка в `service.go` (без імпорту `database/sql`): динамічна JSON Schema з `enum` категорій воркспейсу, розбивка чека на кілька транзакцій, конвертація в копійки, ідемпотентність, розрахунок відсотків аналітики.
  - Табличні unit-тести `service_test.go` з мок-репозиторієм та мок-AI.
  - Тонкі контролери `http_handler.go` (`/parse`, `/transactions`, `/summary`, `/categories`) та `bot_handler.go` (голос/текст + inline-кнопка `❌ Скасувати`).
  - Точка входу `cmd/app/main.go`.
- **Перевірка:** повністю робочий Бекенд + Telegram-бот, зелений `go test ./...` (включно з `arch_test.go` та `service_test.go`).

---

### Частина Б: Фронтенд — Mobile-First Додаток (`web/`)

#### [ ] План `#007` (`[FE-Core]`): Ініціалізація React/Vite/Tailwind/Capacitor/PWA, Конфіг та Двомовність (`i18n`: `uk` + `en`)
- **Файли:** `web/package.json`, `web/vite.config.ts`, `web/capacitor.config.ts`, `web/.env.example`, `web/src/core/config.ts`, `web/src/core/i18n/locales/uk.ts`, `web/src/core/i18n/locales/en.ts`, `web/src/core/i18n/I18nContext.tsx`, `web/src/core/utils/format.ts`.
- **Зміст:**
  - Налаштування Vite + React + TypeScript + Tailwind CSS + Lucide + `vite-plugin-pwa` + Capacitor.
  - Єдиний конфіг `web/src/core/config.ts` (єдине місце для `import.meta.env`).
  - Двомовна система `i18n` (`uk.ts` та `en.ts` зі суворою перевіркою типів `TranslationSchema`, мапінгом усіх `ERR_*` та системних категорій) + правило ESLint проти захардкоджених літералів у JSX (`no-literal-string`).
  - Спільні DRY-утиліти (`formatMoneyMinor`, `formatDate`, `getCategoryLabel`).
- **Перевірка:** `npm run build` (`tsc`) та `go test ./...` (`arch_test.go` для `web/src/`).

#### [ ] План `#008` (`[FE-Core]`): Мережевий API-клієнт, Офлайн-черга (`IndexedDB`) та Тактильна вібрація (`Haptics`)
- **Файли:** `web/src/core/api/client.ts`, `web/src/core/offline/queue.ts`, `web/src/core/offline/useOfflineQueue.ts`, `web/src/core/haptics/haptics.ts`.
- **Зміст:**
  - Єдиний HTTP-клієнт з автопідстановкою JWT, `X-Workspace-ID`, перекладом `error_code` та обробкою `402 Payment Required` (з перевіркою `Capacitor.isNativePlatform()` для дотримання правил App Store).
  - Офлайн-черга в `IndexedDB` (`queue.ts`): локальне збереження голосових аудіо-блобів і тексту з генерацією `idempotency_key` (UUIDv4) при відсутності мережі або таймауті + автоматична фонова синхронізація при появі інтернету.
  - Обгортка вібро-відгуку (`@capacitor/haptics` з фолбеком на Web Vibration API).
- **Перевірка:** `npm run build` та `go test ./...`.

#### [ ] План `#009` (`[FE-Core]`): Авторизація, Mobile-First Layout (Нижній Таб-Бар) та Екран «Простір»
- **Файли:** `web/src/core/auth/AuthContext.tsx`, `web/src/core/auth/LoginPage.tsx`, `web/src/core/auth/RegisterPage.tsx`, `web/src/core/layout/AppLayout.tsx`, `web/src/core/workspace/WorkspacePage.tsx`, `web/src/core/workspace/PaywallModal.tsx`.
- **Зміст:**
  - Екрани входу та реєстрації з миттєвим перемикачем мови `UA | EN`.
  - Mobile-First `AppLayout` із нижнім таб-баром під великий палець (`Пульт` | `Аналітика` | `Простір`), підтримкою `Safe Area` iPhone та адаптивним виглядом для ПК.
  - Екран «Простір»: перемикання між воркспейсами («Мій ФОП» / «Сім'я»), генерація інвайт-коду для приєднання дружини/партнера, підключення Telegram-бота за одноразовим лінком, індикатор статусу підписки/тріалу та модалка `PaywallModal` (з розрізненням Web vs Native App Store).
- **Перевірка:** повноцінний цикл реєстрації, входу, перемикання мов та приєднання другого учасника у браузері.

#### [ ] План `#010` (`[FE-Finance]`): Модуль Фінансів — Екран «Пульт» (Керування однією рукою та запис голосу)
- **Файли:** `web/src/modules/finance/api.ts`, `web/src/modules/finance/hooks/useFinance.ts`, `web/src/modules/finance/components/BigVoiceButton.tsx`, `web/src/modules/finance/components/QuickTextInput.tsx`, `web/src/modules/finance/components/OfflineQueueBanner.tsx`, `web/src/modules/finance/components/BalanceCard.tsx`, `web/src/modules/finance/components/RecentTransactions.tsx`.
- **Зміст:**
  - 3-шаровий поділ на фронті: типізований `finance/api.ts` → хук бізнес-логіки `useFinance.ts` (координація запису голосу, офлайн-черги, вібрації та списку операцій) → чисті UI-компоненти.
  - Велика кнопка запису голосу під великий палець (`BigVoiceButton` з автовизначенням `audio/webm` або `audio/mp4` для iOS Safari), рядок швидкого тексту, індикатор черги офлайн-записів, баланс за сьогодні/місяць та список останніх 5 операцій із кнопкою швидкого скасування (`Скасувати`).
- **Перевірка:** запис голосу й тексту з телефону/браузера, перевірка збереження в офлайн-чергу при вимкненій мережі та миттєвого скасування запису.

#### [ ] План `#011` (`[FE-Finance]`): Модуль Фінансів — Екран «Аналітика та Історія» + Власні Категорії
- **Файли:** `web/src/modules/finance/components/CategoryStats.tsx`, `web/src/modules/finance/components/TransactionList.tsx`, `web/src/modules/finance/components/CategoryManagerModal.tsx`, `web/src/modules/finance/FinancePage.tsx`, `web/src/App.tsx`.
- **Зміст:**
  - Вкладка «Аналітика та Історія»: перемикач періодів (`День` | `Тиждень` | `Місяць`), візуальна розбивка витрат і доходів по категоріях (сума, відсоток, прогрес-бар).
  - Повний список операцій за обраний період із відображенням автора запису (хто з учасників простору додав витрату) та кнопкою видалення.
  - Додавання власних кастомних категорій у воркспейс (які автоматично підхоплюються AI-парсером).
- **Перевірка:** повна перевірка аналітики, фільтрів періодів та кастомних категорій двома мовами (`uk` / `en`).

---

### Частина В: Фінальна Збірка та Поле-Тест на iPhone

#### [ ] План `#012` (`[Full-Stack]`): Вбудовування `//go:embed`, Скрипт HTTPS-тунелю та Фінальний E2E-аудит
- **Файли:** `internal/core/app/static.go`, оновлення `cmd/app/main.go` та `Makefile`.
- **Зміст:**
  - Вбудовування зібраного `web/dist` у єдиний Go-бінарник із підтримкою SPA-роутингу та PWA service worker (PostgreSQL лишається окремим сервісом, бінарник підключається через `DATABASE_URL`).
  - Команда `make tunnel` для запуску захищеного HTTPS-тунелю для тестування мікрофона та спільного простору з двох iPhone.
  - Фінальний прогін `make check` (`arch_test.go`, unit-тести, `npm run build`).
- **Перевірка:** запуск єдиного бінарника (з локальним PostgreSQL) та живий тест спільного бюджету з двох смартфонів.
