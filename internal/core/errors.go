package core

// ErrorCode визначає машиночитаний код помилки API, який фронтенд перекладає через i18n (uk/en).
type ErrorCode string

const (
	// Авторизація та доступ
	ErrUnauthorized       ErrorCode = "ERR_UNAUTHORIZED"
	ErrForbidden          ErrorCode = "ERR_FORBIDDEN"
	ErrInvalidCredentials ErrorCode = "ERR_INVALID_CREDENTIALS"
	ErrEmailTaken         ErrorCode = "ERR_EMAIL_TAKEN"

	// Загальні помилки запитів та ресурсів
	ErrNotFound   ErrorCode = "ERR_NOT_FOUND"
	ErrValidation ErrorCode = "ERR_VALIDATION"
	ErrInternal   ErrorCode = "ERR_INTERNAL"

	// Воркспейси та інвайти
	ErrInviteInvalidOrExpired ErrorCode = "ERR_INVITE_INVALID_OR_EXPIRED"
	ErrTelegramLinkExpired    ErrorCode = "ERR_TELEGRAM_LINK_EXPIRED"

	// Монетизація, підписка та ліміти AI
	ErrSubscriptionExpired ErrorCode = "ERR_SUBSCRIPTION_EXPIRED"
	ErrAIQuotaExceeded     ErrorCode = "ERR_AI_QUOTA_EXCEEDED"

	// Голосовий ввід та парсинг AI
	ErrAudioTooLarge    ErrorCode = "ERR_AUDIO_TOO_LARGE"
	ErrUnsupportedMedia ErrorCode = "ERR_UNSUPPORTED_MEDIA"
	ErrAIParseFailed    ErrorCode = "ERR_AI_PARSE_FAILED"
)
