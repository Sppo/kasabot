# План #003: Авторизація (JWT), Workspaces (інвайти), Telegram-токени та квоти підписки/AI

- **Статус:** `Закрито / В архіві`
- **Модуль / Область:** `core`

## 1. Мета задачі
Реалізувати в `internal/core` усе, на чому стоять модулі: реєстрація та вхід (bcrypt + JWT), воркспейси з ролями, інвайти для партнера (дружини), одноразові токени прив'язки Telegram, перевірка підписки (`402`) та денної квоти AI (`429`). HTTP-маршрути з SPEC §5.3 (`/auth/*`, `/workspaces/*`) монтуються тут як готовий роутер; фінальне складання `app.go` — у Плані #004.

Рішення, які закладаємо (вони не прописані в SPEC і потребують підтвердження):
1. **Новий воркспейс у транзакції, сід модулів після коміту.** `core.Module.OnWorkspaceCreated` не отримує `tx`, тому спочатку в одній транзакції створюємо дані, потім викликаємо `OnWorkspaceCreated` кожного модуля. Сіди модулів мусять бути ідемпотентними (`ON CONFLICT DO NOTHING`). Якщо сід упав — компенсація, відповідь `500` з `error_id`:
   - **Register:** у транзакції `user` + `workspace` + `workspace_members(owner)` + `active_workspace_id`. При падінні сіда видаляємо воркспейс **і** щойно створеного користувача (`workspaces.owner_id` посилається на `users(id)` без `CASCADE`, тому спочатку воркспейс, потім користувач), щоб повторна реєстрація не давала `ERR_EMAIL_TAKEN`.
   - **CreateWorkspace (додатковий воркспейс існуючого користувача):** у транзакції `workspace` + `workspace_members(owner)`, **без** зміни `active_workspace_id`. Після успішного сіду виставляємо `active_workspace_id` на новий воркспейс; при падінні видаляємо лише новий воркспейс, активний лишається попереднім.
2. **Ліміт воркспейсів на користувача** (`MAX_WORKSPACES_PER_USER`, за замовчуванням 5), інакше можна безкінечно створювати воркспейси й отримувати новий trial та нові 15 AI-запитів на добу.
3. **Захист входу від підбору пароля без Redis:** лічильник невдалих спроб у пам'яті процесу (ключ — email + IP), блокування на `LOGIN_LOCK_MINUTES`. Це достатньо для одного інстансу; при масштабуванні на кілька інстансів переносимо в Redis окремим планом.
4. **Ефективний статус підписки рахується з `subscription_ends_at`**, а не з фонового job: `trial`/`active` із простроченою датою вважаються `expired` (читання даних лишається, AI-ввід → `402`).
5. **Квота AI:** перевірка (`402`/`429`) — до виклику AI, списання — окремим викликом тільки після успішної відповіді (правило ARCHITECTURE_RULES §2.3). Списання атомарне в SQL (`UPDATE ... SET ai_requests_today = CASE WHEN ai_requests_date < today THEN 1 ELSE ai_requests_today + 1 END`); «сьогодні» рахується в `DEFAULT_TIMEZONE`. Мала похибка при двох одночасних запитах на межі ліміту допустима (перевірка і списання не в одній транзакції).
6. **Інвайт одноразовий:** код видаляється при вході учасника; повторне приєднання вже існуючого учасника — успіх без дублювання. Інвайти і токени генерує `crypto/rand`.
7. **Залежності (дозволені цим планом, SPEC §2):** `github.com/golang-jwt/jwt/v5` (підпис і розбір JWT, парсер дозволяє лише `HS256`), `golang.org/x/crypto/bcrypt` (хеш паролів). Інших немає.

## 2. Файли для створення або зміни
- `go.mod`, `go.sum` — `golang-jwt/jwt/v5`, `golang.org/x/crypto`.
- `internal/core/errors.go` — тип `AppError{Status, Code, Err}`; код `ErrTooManyAttempts` (`ERR_TOO_MANY_ATTEMPTS`).
- `internal/core/httputil.go` — `WriteAppError(w, r, err)` (мапить `AppError` у статус + `error_code`, 5xx — з `error_id`).
- `internal/core/config.go`, `internal/core/config_test.go`, `.env.example` — лише нові змінні (таблиця нижче), валідація; наявні TTL не дублюємо.
- `internal/core/database.go` — індекс `workspace_members(user_id)` (`CREATE INDEX IF NOT EXISTS`).
- `internal/core/store.go` — **єдине** місце SQL для таблиць ядра (users, workspaces, workspace_members, workspace_invites, telegram_link_tokens).
- `internal/core/auth.go` — сервіс: Register, Login, Me, видача і перевірка JWT, bcrypt, лічильник невдалих входів, токени Telegram (створення + `ConsumeTelegramLinkToken` для бота з Плану #004).
- `internal/core/workspace.go` — сервіс: створення воркспейсу (+ сід модулів), активація, список, інвайти, join, перевірка членства/ролі, `EnsureAIAllowed` (402/429), `ConsumeAIQuota`.
- `internal/core/middleware.go` — `Auth` (Bearer JWT → `UserID` у ctx), `Workspace` (`X-Workspace-ID` або `active_workspace_id`, перевірка членства → `WorkspaceID` + роль у ctx), `RequireOwner`.
- `internal/core/auth_handler.go` — тонкі HTTP-хендлери та `Routes()` для `/auth/*` і `/workspaces/*`.
- `internal/core/auth_test.go`, `internal/core/workspace_test.go`, `internal/core/middleware_test.go` — тести на реальному Postgres у тимчасовій схемі (хелпер `openTestSchemaPool`).
- `arch_test.go` — правило: `auth.go`, `workspace.go`, `middleware.go` у `internal/core` не імпортують `pgx`/`database/sql` (SQL лише в `store.go`/`database.go`), + фікстура-порушення.
- `ARCHITECTURE_RULES.md` — одним рядком зафіксувати це правило для core. `SPEC.md` — оновити перелік файлів core і додати `AppError`/нові змінні.

### Нові змінні `.env` (з українськими коментарями в `.env.example`)
| Змінна | За замовч. | Призначення |
|---|---|---|
| `BCRYPT_COST` | 12 | вартість bcrypt (діапазон 10–14) |
| `LOGIN_MAX_ATTEMPTS` | 5 | невдалих входів до блокування |
| `LOGIN_LOCK_MINUTES` | 15 | тривалість блокування |
| `MAX_WORKSPACES_PER_USER` | 5 | ліміт воркспейсів, які створює користувач |
| `TG_BOT_USERNAME` | порожньо | для deep-link `https://t.me/<bot>?start=link_<token>`; якщо порожньо — відповідь без `deep_link` |

Уже наявні в `config.go` / `.env.example` і **використовуються без дублів**: `WORKSPACE_INVITE_TTL_HOURS` (48, `cfg.WorkspaceInviteTTL`), `TG_LINK_TOKEN_TTL_MIN` (10, `cfg.TGLinkTokenTTL`), `JWT_SECRET`, `JWT_TTL_HOURS`, `TRIAL_DAYS`, `TRIAL_DAILY_AI_LIMIT`, `ACTIVE_DAILY_AI_LIMIT`, `DEFAULT_TIMEZONE`, `DEFAULT_CURRENCY`.

## 3. Покроковий план реалізації
1. [x] Додати залежності, `AppError`, `ErrTooManyAttempts`, `WriteAppError`; нові змінні в `config.go` / `.env.example` / тестах конфігу.
2. [x] `store.go`: SQL з параметрами `$n`, явні списки колонок; операції — створення користувача, пошук за email/id, воркспейси користувача, членство/роль, інвайти, токени Telegram, лічильник квоти; індекс у `database.go`.
3. [x] `auth.go`: нормалізація email (trim + lower-case), пароль від 8 символів і не довший за 72 байти (ліміт bcrypt), `ERR_EMAIL_TAKEN` при дублі (обробка unique violation, а не «спочатку SELECT»), однакова відповідь `ERR_INVALID_CREDENTIALS` для невідомого email і хибного пароля (фіктивне порівняння хешу для вирівнювання часу), JWT (`sub`, `exp`, `iat`; лише `HS256`), блокування за невдалими спробами.
4. [x] `workspace.go`: Register створює користувача + дефолтний воркспейс (`trial`, `subscription_ends_at = NOW + TRIAL_DAYS`) + `owner` + `active_workspace_id` в одній транзакції, далі сід модулів із компенсацією; створення додаткових воркспейсів із лімітом; активація (лише учасник); інвайти (лише `owner`) і join; `EnsureAIAllowed`/`ConsumeAIQuota`.
5. [x] `middleware.go` та `auth_handler.go`: розподіл middleware за маршрутами:
   - публічні: `POST /auth/register`, `POST /auth/login`;
   - лише `Auth` (JWT), без `X-Workspace-ID`: `GET /auth/me`, `POST /auth/telegram-link`, `POST /workspaces`, `POST /workspaces/join` (користувач може створювати/приєднуватися до воркспейсу ще до його вибору);
   - `Auth`, воркспейс береться з `{id}` в URL (а не з заголовка): `POST /workspaces/{id}/activate` (учасник), `POST /workspaces/{id}/invites` (лише `role = 'owner'`);
   - `Auth` + `Workspace` (заголовок або активний воркспейс) — для `RegisterRoutes` модулів.
   Маршрути з SPEC §5.3 (`register`, `login`, `me`, `telegram-link`, `POST /workspaces`, `{id}/activate`, `{id}/invites`, `join`); ліміт розміру тіла через `DecodeJSON`; жодних паролів, токенів, email у логах (лише `user_id`/`workspace_id`).
6. [x] Тести (див. розділ 4) та правило в `arch_test.go`; оновлення `ARCHITECTURE_RULES.md` і `SPEC.md`.
7. [x] `make check` без помилок.

## 4. План перевірки та Архітектурний аудит
- [x] `make check` пройшов без помилок (`go vet ./...`, `go test ./...` вкл. з `arch_test.go`, `go build ./...`, `npm run build`); без цього статус не змінювати на `Виконано — очікує перевірки користувачем`
- [x] **3-шарова ізоляція:** хендлери тонкі; `auth.go`/`workspace.go`/`middleware.go` не імпортують `pgx`/`database/sql`; увесь SQL лише в `store.go`.
- [x] **DRY:** використано `WriteJSON`, `DecodeJSON`, `UserIDFromCtx`, `WorkspaceIDFromCtx`; відповіді помилок — тільки через `WriteAppError`.
- [x] **Нуль хардкоду:** TTL, ліміти, bcrypt cost, квоти — лише з `Config`; коди помилок — лише з `errors.go`.
- [x] **Тести реєстрації/входу:** успішна реєстрація створює воркспейс `trial` (14 днів), роль `owner`, `active_workspace_id`; дубль email (у т.ч. в іншому регістрі) → `ERR_EMAIL_TAKEN`; слабкий/задовгий пароль → `ERR_VALIDATION`; хибний пароль і невідомий email дають однакову відповідь; після `LOGIN_MAX_ATTEMPTS` помилок → `429` `ERR_TOO_MANY_ATTEMPTS`, правильний пароль під час блокування теж відхиляється.
- [x] **Тести JWT:** прострочений токен, чужий секрет, `alg=none` та інший алгоритм → `401`.
- [x] **Тести воркспейсів і ролей:** `X-Workspace-ID` чужого воркспейсу → `403`; без заголовка береться активний; `member` не створює інвайт (`403`); інвайт одноразовий, прострочений → `ERR_INVITE_INVALID_OR_EXPIRED`; повторний join не дублює запис; ліміт `MAX_WORKSPACES_PER_USER`; активація чужого воркспейсу → `403`.
- [x] **Тести квот і підписки:** після `subscription_ends_at` → `402` `ERR_SUBSCRIPTION_EXPIRED`; на межі `TRIAL_DAILY_AI_LIMIT` → `429` `ERR_AI_QUOTA_EXCEEDED`; без `ConsumeAIQuota` лічильник не росте; зміна дня (в `DEFAULT_TIMEZONE`) скидає лічильник; `active` має свій ліміт; конкурентне списання не втрачає інкременти.
- [x] **Тести Telegram-токена:** токен створюється з TTL, одноразовий, прострочений → `ERR_TELEGRAM_LINK_EXPIRED`, повторна прив'язка того ж `telegram_id` іншому користувачу відхиляється.
- [x] **Сід модулів:** при помилці `OnWorkspaceCreated` у Register видалено воркспейс і користувача (повторна реєстрація з тим самим email проходить), у CreateWorkspace видалено лише новий воркспейс, а `active_workspace_id` не змінився; відповідь `500` з `error_id`.
- [x] **Логи:** тест, що пароль, JWT та email не потрапляють у лог.
- [ ] Ручна перевірка: `curl` реєстрація → логін → `/auth/me` → створення інвайту → join другим користувачем. *(HTTP-цикл наразі перевірено через `TestAuthHandlerEndToEnd`; ручний `curl` буде доступний після підключення `cmd/app/main.go` у Плані #004/#006).*

## 6. Доопрацювання за код-ревю
Після ревю `make check` зелений (також `go test -race` на `internal/core`), основна логіка відповідає плану. Нижче те, що треба виправити перед закриттям.

**Обов'язкове**
1. [x] **Лічильник невдалих входів росте без обмежень** (`auth.go`, мапа `attempts`): записи видаляються лише коли той самий ключ `email|IP` повторно потрапляє в `isLocked` після закінчення блокування. Бот, що шле входи з випадковими email, роздуває пам'ять; невдалі спроби, які не дійшли до ліміту, не згасають ніколи (4 помилки на тиждень → блокування). Зробити: зберігати час останньої невдачі, вважати лічильник скинутим, якщо від неї минуло `LOGIN_LOCK_MINUTES`; прибирати застарілі записи (при кожному `recordFailure` або фоновим проходом із зупинкою через `context`/`Close`); обмежити верхню межу кількості записів. Тест: лічильник згасає за часом; застарілі записи видаляються.
2. [x] **Помилки компенсації мовчки ігноруються** (`auth.go:122`, `workspace.go:74`: `_ = s.store.Delete...(context.Background(), ...)`). Якщо видалення впаде, лишиться «осиротілий» користувач (і `ERR_EMAIL_TAKEN` при повторі) без жодного сліду в логах. Зробити: контекст із таймаутом (іменована константа), результат логувати через `logger` (`error_id`, `user_id`/`workspace_id`, без email); відповідь клієнту лишається `500`. Те саме для `SetActiveWorkspace` у `CreateWorkspace`: якщо він упав після успішного сіду, видалити щойно створений воркспейс (інакше він лишається й займає ліміт).
3. [x] **`go.mod` не приведено до ладу.** `go mod tidy -diff` показує, що `golang-jwt/jwt/v5` і `golang.org/x/crypto` позначені `// indirect`, хоча імпортуються напряму. Виконати `go mod tidy`. Також `go 1.25.0` → `go 1.26.0`, оновлено `x/sync` та `x/text`: це не було в плані. У підсумку пояснити причину (яка залежність вимагає 1.26) і перевірити, що `make check` проходить після `tidy`. Нові залежності понад `jwt/v5` і `x/crypto` не додавати.
4. [x] **Довжини полів:** `email` ≤ 254 символи, `name` і `workspace_name` ≤ 100 символів (іменовані константи), інакше `400 ERR_VALIDATION`; тест. Зараз обмеження лише розмір тіла запиту.

**Бажане (малі правки)**
5. [x] `ParseToken`: додати `jwt.WithExpirationRequired()` (токен без `exp` має відхилятись) + тест.
6. [x] Тести, яких не вистачає: гілка `ai_requests_date < today` в `IncrementAIRequestsToday` (після зміни дня списання дає 1, а не `N+1`); ключ блокування `email|IP` (інша IP не блокується разом із першою).

**Свідомо не в цьому плані (нотатки для #004)**
- `extractClientIP` бере `r.RemoteAddr`. За `cloudflared`/reverse-proxy усі запити матимуть одну адресу, і блокування за `email|IP` стане блокуванням за email (хто завгодно зможе заблокувати вхід чужого акаунта 5 хибними спробами). У #004 при складанні `app.go` потрібні довірені проксі (`RealIP`, налаштовується в `.env`).
- `POST /auth/register` без обмеження частоти: bcrypt cost 12 — це CPU. У #004 додати загальний rate-limit middleware на публічні маршрути.
- `JoinWorkspaceByInvite` / `ConsumeTelegramLinkToken` у `store.go` порівнюють строк із `time.Now()`, а сервіси мають свій `now` — непослідовно, але працює; можна вирівняти при #003b.
