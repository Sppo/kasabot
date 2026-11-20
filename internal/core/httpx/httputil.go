package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/logging"
)

// ErrorResponse — єдиний формат помилки REST API для двомовного перекладу на фронтенді.
type ErrorResponse struct {
	ErrorCode apperr.ErrorCode `json:"error_code"`
	ErrorID   string           `json:"error_id,omitempty"`
	Message   string           `json:"message"`
}

// WriteJSON записує структуру у форматі JSON з відповідним HTTP-статусом.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError повертає стандартизовану JSON-помилку з машиночитаним кодом ErrorCode.
func WriteError(w http.ResponseWriter, status int, code apperr.ErrorCode, message string) {
	WriteErrorWithID(w, status, code, message, "")
}

// WriteErrorWithID повертає JSON-помилку з кодом ErrorCode та ідентифікатором error_id для пошуку в логах.
func WriteErrorWithID(w http.ResponseWriter, status int, code apperr.ErrorCode, message, errorID string) {
	WriteJSON(w, status, ErrorResponse{
		ErrorCode: code,
		ErrorID:   errorID,
		Message:   message,
	})
}

// WriteAppError мапить AppError (або неочікувану помилку) у JSON-відповідь; 5xx помилки логуються з error_id.
func WriteAppError(w http.ResponseWriter, r *http.Request, logger *logging.Logger, err error) {
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		var errorID string
		if logger != nil {
			errorID = logger.LogError(r.Context(), apperr.ErrInternal, err.Error())
		} else {
			errorID = logging.NewErrorID()
		}
		WriteErrorWithID(w, http.StatusInternalServerError, apperr.ErrInternal, "internal server error", errorID)
		return
	}

	if appErr.Status >= http.StatusInternalServerError {
		var errorID string
		if logger != nil {
			errorID = logger.LogError(r.Context(), appErr.Code, appErr.Error())
		} else {
			errorID = logging.NewErrorID()
		}
		WriteErrorWithID(w, appErr.Status, appErr.Code, appErr.Message, errorID)
		return
	}

	WriteError(w, appErr.Status, appErr.Code, appErr.Message)
}

// DecodeJSON декодує JSON-тіло запиту з обмеженням розміру maxBytes, забороною невідомих полів та перевіркою на зайвий хвіст після першого JSON-об'єкта.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	// Перевіряємо, що після першого JSON-об'єкта немає зайвих даних
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode json: unexpected trailing data after JSON object")
	}

	return nil
}
