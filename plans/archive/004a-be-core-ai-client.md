# План #004a: Універсальний клієнт Gemini AI (`internal/core/ai`)

- **Статус:** `Закрито / В архіві`
- **Модуль / Область:** `core`

## 1. Мета задачі
Один клієнт для всіх модулів: приймає текст **або** аудіо, системний промпт і JSON Schema відповіді, повертає суворий JSON. Модулі (фінанси зараз, нотатки/календар пізніше) працюють лише через інтерфейс `ai.Client`, а не через HTTP до Gemini. Перевірку квоти (`EnsureAIAllowed`) і списання (`ConsumeAIQuota`) викликає модуль до і після `Parse`; сам клієнт квоти не знає.

Рішення (потребують підтвердження):
1. **Без SDK, лише `net/http` + `encoding/json`:** запит `POST {base}/v1beta/models/{GEMINI_MODEL}:generateContent` із заголовком `x-goog-api-key` (ключ **не** в URL, щоб не потрапив у логи). Нових залежностей немає. Базова URL — параметр конструктора (у тестах це `httptest.Server`), не змінна `.env`.
2. **Контракт:**
   ```go
   type Client interface {
       Parse(ctx context.Context, req Request) (Result, error)
   }
   type Request struct {
       SystemPrompt   string         // правила розбору, пише модуль
       Text           string         // або текст...
       Audio          []byte         // ...або аудіо
       MimeType       string         // для аудіо
       ResponseSchema map[string]any // JSON Schema (OpenAPI-підмножина Gemini)
   }
   type Result struct { JSON json.RawMessage }
   ```
   Рівно одне з `Text`/`Audio` має бути задане, інакше `ERR_VALIDATION`.
3. **Захист від prompt injection:** вміст користувача (текст або аудіо) іде **лише** в `contents` з роллю `user`; `systemInstruction` містить тільки промпт модуля + службовий рядок із поточною датою/часом у `DEFAULT_TIMEZONE`; `responseMimeType: application/json` + `responseSchema`; після відповіді перевіряємо, що це валідний JSON. Змістовну валідацію (сума > 0, категорія з `enum`) робить сервіс модуля.
4. **Валідація входу до мережі:** розмір аудіо ≤ `MAX_AUDIO_SIZE_BYTES` (`ERR_AUDIO_TOO_LARGE`, 413); MIME нормалізується (прибираємо параметри: `audio/webm;codecs=opus` → `audio/webm`) і звіряється з білим списком із ARCHITECTURE_RULES §2.2 (`ERR_UNSUPPORTED_MEDIA`, 415).
5. **Помилки й квота:** таймаут (`AI_REQUEST_TIMEOUT_SEC`), мережеві збої та відповіді `5xx`/`429` від Gemini → новий код `ERR_AI_UNAVAILABLE` (503); невалідний/порожній JSON або заблокована відповідь → `ERR_AI_PARSE_FAILED` (422). **Усі помилки означають «квоту не списувати»** (правило ARCHITECTURE_RULES §2.3). Автоматичних повторів у клієнті немає (повторює офлайн-черга з `idempotency_key`, щоб не платити двічі).
6. **Логи без вмісту:** лише модель, тривалість, HTTP-статус, `error_id`, розмір аудіо в байтах і кількість токенів із `usageMetadata`. Ніколи: ключ API, текст користувача, аудіо, тіло відповіді моделі. Тіло помилки Gemini логуємо лише обрізаним (до N байт) і без запиту.
7. **Ключ Gemini і приватність.** Автотести ключа не потребують (мок-сервер). Для ручного кроку 7 достатньо безкоштовного ключа з Google AI Studio, але на безкоштовному рівні Google може використовувати запити для покращення продуктів, тому лише тестові фрази; для реальних витрат справжніх користувачів потрібен платний рівень. Актуальні ліміти й ціни перевіряти на ai.google.dev/pricing. Ключ лише в локальному `.env` (у `.gitignore`), ніколи в репозиторії, логах чи звітах.
8. **Ризик, який треба перевірити вручну з реальним ключем:** документація розходиться щодо `audio/webm` та `audio/mp4`. Vertex AI їх згадує для нових моделей, а гайд Gemini API перелічує лише WAV/MP3/AIFF/AAC/OGG/FLAC. Браузери дають саме `webm` (Chrome) та `mp4` (iOS Safari). Крок 7 плану (розділ 3) — ручна перевірка обох форматів; якщо не підтримуються, окремим планом додаємо перекодування (на клієнті в WAV або на сервері через `ffmpeg`), а білий список у цьому плані лишається, як є.

## 2. Файли для створення або зміни
- `internal/core/ai/client.go` — інтерфейс `Client`, типи `Request`/`Result`, валідація входу (розмір, MIME, рівно одне з text/audio), нормалізація MIME.
- `internal/core/ai/gemini.go` — реалізація `Client` поверх `net/http` (формування запиту, розбір відповіді, мапінг помилок, `usageMetadata`).
- `internal/core/ai/client_test.go` — тести з `httptest`-сервером (без реального Gemini).
- `internal/core/apperr/errors.go` — `ErrAIUnavailable` (`ERR_AI_UNAVAILABLE`).
- `arch_test.go` — у `isAllowedCoreEdge` додати `ai`: залежить від `apperr`, `config`, `logging`.
- `ARCHITECTURE_RULES.md`, `SPEC.md` — шлях `internal/core/ai`, новий код помилки, порядок «перевірка квоти → `Parse` → списання».
- Не змінюються: `go.mod`, `go.sum`, `.env.example`, `Makefile` (змінні `GEMINI_API_KEY`, `GEMINI_MODEL`, `AI_REQUEST_TIMEOUT_SEC`, `MAX_AUDIO_SIZE_BYTES` уже є).

## 3. Покроковий план реалізації
1. [x] `ErrAIUnavailable` в `apperr`; допустиме ребро `ai` в `arch_test.go` + фікстура-порушення (`ai` імпортує `accounts`).
2. [x] `client.go`: типи, інтерфейс, `NewGeminiClient(cfg, logger, opts...)` з опціями `WithBaseURL`, `WithHTTPClient`, `WithClock` (для тестів); валідація входу.
3. [x] `gemini.go`: тіло запиту (`systemInstruction`, `contents[role=user]` з `text` або `inlineData{mimeType, data(base64)}`, `generationConfig{responseMimeType, responseSchema}`), заголовок `x-goog-api-key`, контекст із таймаутом `cfg.AIRequestTimeout`.
4. [x] Розбір відповіді: перший кандидат → текст → `json.Valid`; порожньо, `finishReason` ≠ `STOP` або `promptFeedback.blockReason` → `ERR_AI_PARSE_FAILED`; `5xx`/`429`/таймаут/мережа → `ERR_AI_UNAVAILABLE`; логування за пунктом 6.
5. [x] Тести (розділ 4).
6. [x] Документація: `ARCHITECTURE_RULES.md`, `SPEC.md` (§4.4: сигнатура, коди помилок, порядок квоти).
7. [x] **Ручна перевірка користувачем з реальним `GEMINI_API_KEY`** (не автотест, ключ не потрапляє в репозиторій): короткий запис голосом у форматах `webm` (Chrome) та `mp4` (iOS Safari) + текстовий запит; результат записати в підсумок.
8. [x] `make check` без помилок.

## 4. План перевірки та Архітектурний аудит
- [x] `make check` пройшов без помилок (`go vet ./...`, `go test ./...` вкл. з `arch_test.go`, `go build ./...`, `npm run build`); без цього статус не змінювати на `Виконано — очікує перевірки користувачем`
- [x] **Ізоляція:** `internal/core/ai` не імпортує `accounts`, `db`, `pgx` та жодного модуля; залежить лише від `apperr`, `config`, `logging`.
- [x] **Валідація входу:** аудіо понад `MAX_AUDIO_SIZE_BYTES` → `ERR_AUDIO_TOO_LARGE` **без звернення до мережі** (мок-сервер не отримав запит); MIME поза списком → `ERR_UNSUPPORTED_MEDIA`; `audio/webm;codecs=opus` приймається; передано і текст, і аудіо або жодного → `ERR_VALIDATION`.
- [x] **Структура запиту (перевіряється на мок-сервері):** вміст користувача лише в `contents` з роллю `user` і **ніколи** в `systemInstruction`; у `systemInstruction` є поточна дата/час у `DEFAULT_TIMEZONE`; передано `responseSchema` та `responseMimeType: application/json`; ключ у заголовку `x-goog-api-key`, а **не** в URL чи тілі; текст користувача зі шкідливою інструкцією («ignore previous...») не потрапляє в `systemInstruction`.
- [x] **Помилки:** `5xx`, `429`, обрив з'єднання, таймаут (мок відповідає довше за `AI_REQUEST_TIMEOUT_SEC`) → `ERR_AI_UNAVAILABLE`; порожня відповідь, не-JSON, `finishReason=SAFETY`, `blockReason` → `ERR_AI_PARSE_FAILED`; валідна відповідь → `Result.JSON`, що збігається з відповіддю мока.
- [x] **Скасування:** скасований `ctx` перериває запит без зависання (тест із `-race`).
- [x] **Логи без вмісту:** тест, що ключ API, текст користувача, байти аудіо та тіло відповіді моделі не потрапляють у лог; `error_id` є в лозі помилки.
- [x] **Нуль хардкоду:** модель, таймаут, ліміт аудіо — лише з `config.Config`; базовий URL — з конструктора; жодних змін у `.env.example`.
- [x] Ручна перевірка (крок 7) виконана користувачем, результат по `webm` та `mp4` записаний у плані.

## 6. Доопрацювання за код-ревю
Автоматична частина прийнята: `make check` і `go test -race -count=1 ./internal/core/...` зелені, залежності `ai` лише `apperr`/`config`/`logging`, ключ іде в заголовку, вміст користувача не потрапляє в `systemInstruction` і в логи. Лишається одне змістове зауваження.

1. [x] **Будь-яка не-2xx відповідь Gemini зараз стає `ERR_AI_UNAVAILABLE` (503)**, зокрема `400`, `401`, `403`, `404` (`gemini.go`, блок `resp.StatusCode < 200 || >= 300`). `503` означає «спробуй пізніше», і офлайн-черга з Плану #008 повторюватиме такий запит безкінечно. Але `400` (наприклад, Gemini не приймає `audio/webm`/`audio/mp4` або схема невалідна) від повтору не зникне, а `401`/`403` означає хибний ключ. Зробити мапінг за статусом:
   - `429`, `5xx`, таймаут, мережеві збої → `ERR_AI_UNAVAILABLE` (503, повторювані);
   - `400`, `404`, `422` → `ERR_AI_PARSE_FAILED` (422, **не** повторюється; у лог `gemini_request_rejected` зі статусом і обрізаним тілом);
   - `401`, `403` → `ERR_INTERNAL` (500) з `error_id` та окремим повідомленням у лозі `gemini_auth_failed` (помилка конфігурації ключа, не вина користувача).
   Тести для кожної групи статусів. У `SPEC.md` §4.4 і `ARCHITECTURE_RULES.md` оновити опис кодів помилок: яка група повторюється, а яка ні. Квоту, як і раніше, не списувати в жодному з цих випадків.

**Нотатки без змін у цьому плані (для обговорення з користувачем)**
- У `generationConfig` не задано `temperature` (для розбору витрат варто низьку, 0–0.2) і `maxOutputTokens`; для `gemini-2.5-flash` також можна вимкнути «thinking» (`thinkingBudget: 0`), що зменшить вартість і затримку. Це впливає на якість розбору, тому вирішуємо після ручної перевірки (крок 7), окремою дрібною правкою.
- Відсутній `GEMINI_API_KEY` дає помилку лише під час виклику `Parse` (`503`), а не при старті. Чи вважати це фатальним при запуску — вирішуємо в Плані #004c (`app.go`).

## 7. Результат ручної перевірки (крок 7), 2026-11-20
Перевірка виконана локальною сторінкою (запит у тій самій формі, що й `GeminiClient`: `systemInstruction`, `contents[role=user]`, `responseMimeType` + `responseSchema`), безкоштовний ключ Google AI Studio.
- **`audio/webm;codecs=opus` (Chrome, 5 с, ~97 КБ): ✅ `HTTP 200`.** Модель `gemini-3.8-flash` повернула валідний JSON за схемою (`type=expense`, `amount=20`, `description="батон"`). Аудіо враховано як 150 токенів, усього 294 токени, з них 83 токени «міркування» при 15 токенах відповіді.
- **`audio/mp4; codecs=mp4a.40.2` (Safari, 5 с, ~129 КБ): ✅ `HTTP 200`.** Той самий JSON (`expense`, 20, «батон»); аудіо 140 токенів, усього 281, з них 80 токенів «міркування». Формат збігається з тим, що записує iOS Safari, тож перекодування не потрібне.
- **Модель `gemini-2.5-flash` для нових ключів знята:** відповідь `404 NOT_FOUND` («no longer available to new users», рекомендовано `gemini-3.8-flash`). Типове значення `GEMINI_MODEL` у `config.go`, `.env.example` і `SPEC.md` застаріло.
- **`503 UNAVAILABLE` («high demand»)** траплялося на нових запитах: перевантаження на боці Google, не помилка формату. Після кількох повторів запит пройшов.
- Формат відповіді (`parts[].text` + службове `thoughtSignature`) наш парсер обробляє коректно.
