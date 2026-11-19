# SPEC.md — Модульна Mobile-First платформа для бізнесу та щоденного обліку (Go + React/Capacitor + Telegram)

## 1. Архітектурна концепція (Mobile-First Modular Monolith)
Система будується як **модульний моноліт** на Go з клієнтським додатком на **React (TypeScript + Capacitor + PWA)**, спроєктованим передусім під **мобільний телефон та керування однією рукою («на ходу»)**.

- **Mobile-First (Головна точка входу):**
  - Клієнтський інтерфейс будується навколо сценарію «вийшов з магазину → дістав телефон → одним пальцем натиснув велику кнопку мікрофона → продиктував → сховав телефон».
  - Завдяки **Capacitor** єдина кодова база `web/` компілюється у повноцінні нативні додатки для **iOS (App Store)** та **Android (Google Play)** з тактильним вібро-відгуком (`Haptics`), нативним доступом до мікрофона та `Safe Area` під екрани смартфонів.
  - **Офлайн-черга (Offline-First Voice/Text Queue):** Якщо на паркінгу чи в магазині слабкий інтернет, голосовий або текстовий запис миттєво зберігається локально на телефоні (`IndexedDB`) зі статусом *«⏳ В черзі»* і автоматично відправляється на AI-розпізнавання у фоні, щойно з'явиться мережа.
- **Веб-кабінет для ПК (Другорядний режим):** На широкому екрані комп'ютера той самий інтерфейс автоматично розгортається у зручний дашборд для вечірнього перегляду аналітики. В продакшн-режимі папка `web/dist` вбудовується в Go-бінарник через `//go:embed`.
- **Telegram як опціональний канал:** Telegram-бот є додатковим конектором. Користувач за бажанням прив'язує свій Telegram у налаштуваннях додатку. Якщо `TG_BOT_TOKEN` не задано, система повноцінно працює без нього.
- **Ізоляція модулів:** Кожен функціональний напрямок (перший модуль — `finance`) є ізольованим модулем зі своїми міграціями БД, HTTP API, опціональними Telegram-хендлерами та UI-екранами. Додавання нових модулів не змінює код існуючих.
- **Мультитенантність (`Workspaces`):** Усі дані модулів прив'язані до `workspace_id`. Користувач може мати кілька просторів (наприклад, «Мій ФОП» та «Сімейний бюджет») і запрошувати в них партнерів.

---

## 2. Технологічний стек

### Backend (Go)
- **Мова:** Go (останній стабільний)
- **HTTP-роутер:** `github.com/go-chi/chi/v5` + `go-chi/cors`
- **Авторизація:** Email + Password (`golang.org/x/crypto/bcrypt`) + JWT (`github.com/golang-jwt/jwt/v5`)
- **База даних:** PostgreSQL (окремий сервіс; локально через Docker Compose, `make db-up`), драйвер та пул з'єднань `github.com/jackc/pgx/v5` (`pgxpool`), параметризовані запити (`$1, $2, ...`), підключення лише через `DATABASE_URL` з `.env`. ORM не використовується, щоб фільтр `workspace_id` лишався видимим у SQL.
- **Додаткова інфраструктура (за потреби):** Redis та подібні сервіси дозволені за правилом залежностей з `AGENTS.md` (розділ 2, п. 5): лише якщо вирішують реальну задачу затвердженого плану і не ламають інваріанти. Підключений сервіс записується в цей розділ.
- **AI Core:** Google Gemini API (`gemini-2.5-flash`, конфігурується через `.env`) — розпізнавання тексту та аудіо (`.ogg`, `.webm`, `.mp4`, `.m4a`) у суворий JSON за заданою JSON Schema.
- **Telegram Bot (опціонально):** `gopkg.in/telebot.v3`

### Mobile & Web Frontend (React + Capacitor + PWA)
- **Збірка та мова:** Vite + React 18+ + TypeScript
- **Мобільна нативна оболонка:** `@capacitor/core`, `@capacitor/cli`, `@capacitor/ios`, `@capacitor/android`, `@capacitor/haptics` (тактильна вібрація при старті/зупинці запису та збереженні)
- **Стилі та Mobile UI:** Tailwind CSS + Lucide Icons (Mobile-first layout з нижнім таб-баром під великий палець та підтримкою `env(safe-area-inset-*)`)
- **Офлайн-черга та PWA:** `IndexedDB` (локальне збереження аудіо-блобів та тексту при відсутності мережі з фоновою синхронізацією) + `vite-plugin-pwa`
- **Запити, кеш і маршрутизація:** TanStack Query (запити до API та кеш) і React Router. Redux не використовується.
- **Інтеграція з Go:** В продакшн-режимі папка `web/dist` вбудовується в Go-бінарник через `//go:embed` (база даних PostgreSQL лишається окремим сервісом).

---

## 3. Структура репозиторію
```text
kasabot/
├── cmd/
│   └── app/
│       └── main.go                 # Ініціалізація Core, міграції, реєстрація модулів, запуск HTTP і (опціонально) Bot
├── internal/
│   ├── core/
│   │   ├── app.go                  # Контейнер залежностей (DB, AI, Config, Auth, Registry)
│   │   ├── config.go               # Єдина точка зчитування .env (PORT, DATABASE_URL, JWT_SECRET, GEMINI_API_KEY тощо)
│   │   ├── errors.go               # Єдиний реєстр констант машиночитаних кодів помилок (ERR_*) для i18n
│   │   ├── httputil.go             # Спільні DRY-хелпери: WriteJSON, WriteError, DecodeJSON, контекст (UserID, WorkspaceID)
│   │   ├── database.go             # Підключення до PostgreSQL (pgxpool), базові таблиці ядра та раннер міграцій
│   │   ├── auth.go                 # Реєстрація/логін (Email+Password), JWT middleware, коди прив'язки Telegram
│   │   ├── workspace.go            # CRUD воркспейсів, інвайти партнерів, перемикання активного workspace
│   │   ├── ai.go                   # Універсальний клієнт Gemini API (текст + аудіо -> суворий JSON Schema)
│   │   ├── bot.go                  # Опціональна ініціалізація Telegram-бота, прив'язка /start link_<code>, роутинг
│   │   └── module.go               # Інтерфейс Module та реєстр модулів
│   └── modules/
│       └── finance/                # Модуль №1: Облік доходів та витрат (Суворий 3-шаровий поділ)
│           ├── module.go           # Реалізація core.Module (ініціалізація Repo -> Service -> Handlers)
│           ├── model.go            # Структури даних, категорії та DTO
│           ├── repository.go       # Шар 3 (БД): ТІЛЬКИ SQL-запити (fin_categories, fin_transactions) з workspace_id
│           ├── service.go          # Шар 2 (Бізнес-логіка): НЕ знає про SQL/HTTP; працює через інтерфейс Repository та core.AI
│           ├── service_test.go     # Unit-тести бізнес-логіки з мок-реалізацією Repository та core.AI
│           ├── http_handler.go     # Шар 1 (Тонкий контролер): парсинг HTTP -> виклик Service -> JSON-відповідь
│           └── bot_handler.go      # Шар 1 (Тонкий контролер): прийом голосу/тексту в TG -> виклик Service -> відповідь-чек
├── web/                            # Mobile-First клієнтський додаток (Capacitor + PWA + Desktop Web)
│   ├── capacitor.config.ts         # Конфігурація нативної збірки для iOS (App Store) та Android (Google Play)
│   ├── src/
│   │   ├── core/
│   │   │   ├── config.ts           # Єдина точка зчитування конфігурації та констант з .env (VITE_*)
│   │   │   ├── utils/              # Спільні DRY-утиліти: formatMoneyMinor, formatDate, getCategoryLabel
│   │   │   ├── i18n/               # Мультимовність на фронті (uk.ts, en.ts з суворою типізацією ключів + I18nContext)
│   │   │   ├── api/                # HTTP-клієнт з JWT, X-Workspace-ID та конфігом API_BASE_URL для мобільного білда
│   │   │   ├── offline/            # IndexedDB офлайн-черга голосових/текстових записів + авто-синхронізація
│   │   │   ├── haptics/            # Обгортка над Capacitor Haptics / Web Vibration API
│   │   │   ├── auth/               # LoginPage, RegisterPage, AuthContext
│   │   │   ├── workspace/          # Перемикач Workspace, модалка інвайтів та прив'язки Telegram
│   │   │   └── layout/             # Mobile-First Layout (нижній таб-бар для телефона + адаптивний сайдбар для ПК)
│   │   └── modules/
│   │       └── finance/            # UI модуля фінансів (Суворий 3-шаровий поділ)
│   │           ├── components/     # Шар 1 (View): чисті UI-компоненти без fetch/IndexedDB (BigVoiceButton, BalanceCard тощо)
│   │           ├── hooks/          # Шар 2 (Бізнес-логіка UI): координація стану, офлайн-черги, haptics та помилок 402
│   │           ├── api.ts          # Шар 3 (Мережа): виключно типізовані HTTP-запити до /api/v1/finance/...
│   │           └── FinancePage.tsx # Головний мобільний екран («Пульт») + вкладка «Аналітика та Історія»
│   ├── package.json
│   └── vite.config.ts              # Проксі /api -> localhost:8080 + PWA конфіг
├── arch_test.go                    # Full-Stack автотест архітектури (перевіряє і Go internal/, і React web/src/)
├── Makefile                        # Єдині команди: make check, make dev, make tunnel, make build
├── ARCHITECTURE_RULES.md           # Жорсткі правила NEVER / ALWAYS для агентів
├── SPEC.md
├── GUIDE.md
├── .env.example
├── docker-compose.yml              # Локальний PostgreSQL для розробки (make db-up / make db-down)
├── .gitignore
└── go.mod
```

---

## 4. Ядро системи (`internal/core`)

### 4.1. Системні таблиці БД (Core Schema)
1. **`users`**
   - `id` (BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)
   - `email` (TEXT UNIQUE NOT NULL)
   - `password_hash` (TEXT NOT NULL)
   - `name` (TEXT NOT NULL)
   - `telegram_id` (BIGINT UNIQUE NULL) — заповнюється лише після прив'язки в налаштуваннях
   - `active_workspace_id` (BIGINT NULL) — поточний обраний воркспейс
   - `created_at` (TIMESTAMPTZ)

2. **`workspaces`**
   - `id` (BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)
   - `name` (TEXT NOT NULL) — наприклад, «Мій ФОП» або «Сімейний бюджет»
   - `currency` (TEXT NOT NULL DEFAULT 'UAH')
   - `owner_id` (BIGINT NOT NULL REFERENCES users(id))
   - `plan_status` (TEXT NOT NULL DEFAULT 'trial') — `'trial'` | `'active'` | `'expired'`
   - `subscription_ends_at` (TIMESTAMPTZ NOT NULL) — при створенні автоматично `+14 днів` (безкоштовний тріал)
   - `ai_requests_today` (INTEGER NOT NULL DEFAULT 0) — лічильник викликів AI за поточну добу
   - `ai_requests_date` (DATE NOT NULL DEFAULT CURRENT_DATE) — дата для авто-скидання денного лічильника
   - `created_at` (TIMESTAMPTZ)

3. **`workspace_members`**
   - `workspace_id` (BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE)
   - `user_id` (BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE)
   - `role` (TEXT NOT NULL DEFAULT 'member') — `'owner'` | `'member'`
   - `PRIMARY KEY (workspace_id, user_id)`

4. **`workspace_invites`** (для спільного доступу до воркспейсу)
   - `code` (TEXT PRIMARY KEY)
   - `workspace_id` (BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE)
   - `expires_at` (TIMESTAMPTZ NOT NULL)

5. **`telegram_link_tokens`** (одноразові коди прив'язки Telegram з додатку)
   - `token` (TEXT PRIMARY KEY)
   - `user_id` (BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE)
   - `expires_at` (TIMESTAMPTZ NOT NULL)

6. **`schema_migrations`** (облік застосованих міграцій модулів, тільки вперед)
   - `module` (TEXT NOT NULL)
   - `version` (INTEGER NOT NULL)
   - `applied_at` (TIMESTAMPTZ NOT NULL)
   - `PRIMARY KEY (module, version)`

### 4.2. Авторизація та робота з Workspaces
- При реєстрації (`POST /api/v1/auth/register`) автоматично створюється дефолтний `workspace` (з базовим набором категорій) і запис у `workspace_members` з роллю `owner`.
- Клієнт передає `Authorization: Bearer <jwt>` та заголовок `X-Workspace-ID: <id>` (якщо не передано — береться `user.active_workspace_id`). Middleware перевіряє, що користувач є учасником цього `workspace_id`.
- **Прив'язка Telegram (опціонально):**
  1. У додатку користувач натискає «Підключити Telegram» → `POST /api/v1/auth/telegram-link` створює запис у `telegram_link_tokens` (TTL 10 хвилин) і повертає код та deep-link `https://t.me/<bot_username>?start=link_<token>`.
  2. Бот перехоплює `/start link_<token>`, знаходить `user_id`, записує `telegram_id` користувачу і видаляє використаний токен.
  3. Якщо в бот пише неприв'язаний `telegram_id`, бот відповідає інструкцією зареєструватися в додатку.

### 4.3. Інтерфейс модуля (`core.Module`)
```go
type Migration struct {
    Version int
    Name    string
    SQL     string
}

type Module interface {
    Name() string
    Migrations() []Migration
    RegisterRoutes(r chi.Router)          // Монтується під /api/v1/<module_name> з Auth + Workspace middleware
    RegisterBot(b *telebot.Bot)           // Опціонально: викликається тільки якщо TG_BOT_TOKEN активний
    OnWorkspaceCreated(ctx context.Context, workspaceID int64) error // Сід базових даних модуля (напр. категорії)
}
```

### 4.4. AI-клієнт (`core.AI`)
- Приймає текстовий запит або аудіо-потік (`[]byte`, `mimeType`), системний промпт та `responseSchema` (JSON Schema).
- Обмеження безпеки на аудіо: максимум **3 МБ** на один файл.
- Завжди передає в системний промпт поточну дату/час (`Europe/Kyiv`), щоб AI коректно розпізнавав відносні дати (*«вчора»*, *«в суботу»*).

### 4.5. Підписка, Тріал та Захист лімітів AI (Монетизація)
- **Безкоштовний старт (14 днів Trial):** При реєстрації кожен новий `workspace` отримує `plan_status = 'trial'` та `subscription_ends_at = NOW + 14 days`.
- **М'яке блокування (Read-Only після завершення тріалу/підписки):**
  - Якщо `NOW > subscription_ends_at`, користувач зберігає повний доступ на **читання** своїх даних (бачить історію витрат, баланс та аналітику).
  - При спробі голосового/AI-вводу (`POST /api/v1/finance/parse` або через Telegram) сервер повертає `402 Payment Required` з повідомленням про необхідність продовжити підписку.
- **Схема оплати без комісії App Store / Google Play (Web Checkout Flow):**
  - Оплата та рекурентні автосписання проводяться через веб-версію (WayForPay / plata by mono з комісією ~1.3% замість 15–30% у сторах) через вебхук `POST /api/v1/billing/webhook`.
  - **Правило UI для модерації App Store:** У клієнтському коді (`web/`) при отриманні `402 Payment Required` перевіряється `Capacitor.isNativePlatform()`:
    - Якщо це **Web / PWA у браузері** — відображається пряма кнопка *«Оплатити підписку (Apple Pay / Google Pay)»*.
    - Якщо це **нативна iOS/Android збірка з App Store** — прямі посилання на зовнішній платіжний шлюз приховуються (щоб пройти модерацію Apple Guideline 3.1.1 Reader/Multiplatform rule), натомість виводиться нейтральне повідомлення: *«Термін дії вашого плану завершився. Налаштування тарифу доступне у веб-кабінеті вашого акаунту»*.
- **Денні квоти AI (Захист витрат на Gemini API):**
  - Для `trial`: до **15 AI-запитів на добу** на воркспейс.
  - Для `active` (оплачена підписка): до **60 AI-запитів на добу** на воркспейс (конфігурується через `.env`).
  - Лічильник `ai_requests_today` інкрементується **тільки після успішної відповіді від Gemini API** (при мережевих збоях або помилках валідації квота користувача не згорає).
  - При перевищенні денного ліміту повертається `429 Too Many Requests` (ручне додавання записів через форму залишається доступним).

### 4.6. Мультимовність (`i18n`: `uk` + `en` на Фронтенді)
- **Локалізація на клієнті (`web/src/core/i18n/`):**
  - З першого дня підтримуються 2 мови інтерфейсу: **Українська (`uk`)** та **Англійська (`en`)**.
  - Усі тексти інтерфейсу виносяться у два типізовані словники: `web/src/core/i18n/locales/uk.ts` та `web/src/core/i18n/locales/en.ts`.
  - Словник `en.ts` імпортує тип `TranslationDictionary` із `uk.ts` — якщо агент забуде додати переклад ключа хоча б в одну мову, `npm run build` (`tsc`) не скомпілюється.
  - У React-компонентах (`.tsx`) **заборонено** писати живий текст напряму — використовується тільки хук `const { t, locale, setLocale } = useI18n()`.
- **Помилки API та Системні категорії:**
  - Бекенд повертає помилки зі стабільним кодом `{"error_code": "ERR_...", "message": "..."}`, який фронтенд перекладає через `t('errors.' + error_code)`.
  - Системні категорії (`fin_categories` з `is_system = TRUE`) мають фіксовані ключі перекладу на фронтенді (`t('finance.categories.' + name)`), завдяки чому при перемиканні мови `UA <-> EN` весь інтерфейс і системні категорії миттєво змінюють мову.

---

## 5. Модуль №1: `finance` (Облік доходів та витрат)

### 5.1. Таблиці БД модуля `finance`
1. **`fin_categories`** (фіксований довідник категорій на воркспейс)
   - `id` (BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)
   - `workspace_id` (BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE)
   - `name` (TEXT NOT NULL)
   - `type` (TEXT NOT NULL) — `'expense'` | `'income'`
   - `is_system` (BOOLEAN NOT NULL DEFAULT TRUE)
   - `UNIQUE(workspace_id, name, type)`

   *Базовий сід при створенні воркспейсу (`OnWorkspaceCreated`):*
   - **Витрати (`expense`):** `Продукти`, `Кафе та ресторани`, `Паливо та авто`, `Закупівля`, `Логістика та доставка`, `Реклама та маркетинг`, `Податки та комісії`, `Оренда та комуналка`, `Побут та покупки`, `Інше`.
   - **Доходи (`income`):** `Продажі`, `Оплата послуг`, `Повернення`, `Інший дохід`.

2. **`fin_transactions`**
   - `id` (BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)
   - `workspace_id` (BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE)
   - `user_id` (BIGINT NOT NULL REFERENCES users(id))
   - `type` (TEXT NOT NULL) — `'expense'` | `'income'`
   - `amount_minor` (BIGINT NOT NULL) — сума в копійках (`165.50 грн` = `16550`), завжди `> 0`
   - `currency` (TEXT NOT NULL DEFAULT 'UAH')
   - `category` (TEXT NOT NULL) — значення з `fin_categories.name`
   - `note` (TEXT NOT NULL DEFAULT '') — короткий опис (*«Заправка WOG»*, *«Оплата від Івана»*)
   - `account` (TEXT NOT NULL DEFAULT 'main') — закладка під майбутнє розділення рахунків (готівка/картка/ФОП)
   - `source` (TEXT NOT NULL DEFAULT 'mobile') — `'mobile'` | `'web'` | `'telegram'`
   - `idempotency_key` (TEXT NULL) — ключ дедуплікації (UUIDv4) з клієнтської `OfflineQueue` для захисту від повторного збереження при обриві зв'язку
   - `raw_input` (TEXT NOT NULL DEFAULT '') — оригінальний текст або транскрипт голосу
   - `occurred_at` (DATE NOT NULL) — дата операції (`YYYY-MM-DD`)
   - `created_at` (TIMESTAMPTZ NOT NULL)

### 5.2. Мобільний UX («Однією рукою»), Офлайн-черга та AI-парсинг
1. **Екран «Пульт» (Головний екран на телефоні):**
   - Верхня частина: компактна картка балансу (сьогодні / місяць) + останні 5 операцій з кнопкою швидкого скасування (`Скасувати`).
   - Нижня зона під великий палець: велика центральна кнопка мікрофона (`BigVoiceButton`) з тактильним вібро-відгуком (`Haptics`) + поле швидкого вводу одним рядком.
   - Нижній таб-бар: `Пульт` | `Аналітика` | `Простір`.
2. **Офлайн-стійкість та Ідемпотентність (`OfflineQueue`):**
   - Кожен запис голосу чи тексту отримує на клієнті унікальний `idempotency_key` (UUIDv4).
   - Якщо при завершенні запису немає інтернету (або таймаут запиту), запис зберігається в `IndexedDB` телефону і відображається у списку зі статусом *«⏳ Збережено офлайн — розпізнається при появі мережі»*.
   - При відновленні зв'язку (`window.ononline` / періодичний пінг) черга автоматично вивантажується на `POST /api/v1/finance/parse` із тим самим `idempotency_key`. Якщо сервер уже встиг обробити цей ключ раніше, він повертає збережені транзакції без повторного виклику Gemini і без дублів.
3. **AI-парсинг з розбивкою чека:**
   - `finance.Service` дістає список категорій поточного `workspace_id` і формує JSON Schema для Gemini, де `category` обмежена суворим `enum` із дозволених категорій воркспейсу.
   - Якщо користувач сказав кілька операцій в одній фразі (*«Сільпо продукти 840 і кава 75»*), Gemini повертає масив `items`:
   ```json
   {
     "transcript": "Сільпо продукти 840 і кава 75",
     "items": [
       {
         "type": "expense",
         "amount": 840.0,
         "category": "Продукти",
         "note": "Сільпо продукти",
         "occurred_at": "2026-11-19"
       },
       {
         "type": "expense",
         "amount": 75.0,
         "category": "Кафе та ресторани",
         "note": "Кава",
         "occurred_at": "2026-11-19"
       }
     ]
   }
   ```
4. **Оптимістичне збереження («за 3 секунди»):**
   - Усі розпізнані `items` **одразу зберігаються** в `fin_transactions` без додаткових діалогів підтвердження.
   - У додатку записи миттєво з'являються на екрані «Пульт» із кнопкою швидкого видалення («Скасувати»).
   - У Telegram бот відповідає карткою збережених операцій з inline-кнопкою **`❌ Скасувати`** (`callback_data: fin_undo:<tx_id>`).

### 5.3. REST API ендпоінти

#### Core API (`/api/v1`)
- `POST /api/v1/auth/register` — реєстрація (`email`, `password`, `name`, `workspace_name`) -> JWT + user + workspace
- `POST /api/v1/auth/login` — вхід (`email`, `password`) -> JWT + user + workspaces
- `GET  /api/v1/auth/me` — поточний профіль, список воркспейсів та статус прив'язки Telegram
- `POST /api/v1/auth/telegram-link` — згенерувати одноразовий токен/лінк для підключення Telegram-бота
- `POST /api/v1/workspaces` — створити новий воркспейс
- `POST /api/v1/workspaces/{id}/activate` — змінити активний воркспейс
- `POST /api/v1/workspaces/{id}/invites` — створити інвайт-код для партнера
- `POST /api/v1/workspaces/join` — приєднатися до воркспейсу за інвайт-кодом

#### Finance Module API (`/api/v1/finance`)
- `POST   /api/v1/finance/parse` — універсальний AI-ввід: приймає JSON `{"text": "..."}` або `multipart/form-data` з полем `audio`, парсить через Gemini, зберігає і повертає створені транзакції + `transcript`.
- `POST   /api/v1/finance/transactions` — ручне створення транзакції (fallback без AI).
- `GET    /api/v1/finance/transactions?period=day|week|month|all&from=YYYY-MM-DD&to=YYYY-MM-DD` — список транзакцій воркспейсу.
- `DELETE /api/v1/finance/transactions/{id}` — видалення транзакції (кнопка «Скасувати»).
- `GET    /api/v1/finance/summary?period=day|week|month` — агрегований баланс (`total_income`, `total_expense`, `net_balance`) + розбивка по категоріях (`by_category` із сумою та відсотком).
- `GET    /api/v1/finance/categories` — список категорій поточного воркспейсу.
- `POST   /api/v1/finance/categories` — додати власну категорію у воркспейс.
