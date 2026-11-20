package core

import "fmt"

// ErrorCode визначає машиночитаний код помилки API, який фронтенд перекладає через i18n (uk/en).
type ErrorCode string

const (
	// Авторизація та доступ
	ErrUnauthorized       ErrorCode = "ERR_UNAUTHORIZED"
	ErrForbidden          ErrorCode = "ERR_FORBIDDEN"
	ErrInvalidCredentials ErrorCode = "ERR_INVALID_CREDENTIALS"
	ErrEmailTaken         ErrorCode = "ERR_EMAIL_TAKEN"
	ErrTooManyAttempts    ErrorCode = "ERR_TOO_MANY_ATTEMPTS"

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

// AppError представляє контрольовану бізнес- або HTTP-помилку з відповідним статусом та ErrorCode.
type AppError struct {
	Status  int
	Code    ErrorCode
	Message string
	Err     error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError створює *AppError для повернення із сервісного шару до HTTP-хендлерів.
func NewAppError(status int, code ErrorCode, message string, err error) *AppError {
	return &AppError{
		Status:  status,
		Code:    code,
		Message: message,
		Err:     err,
	}
}
