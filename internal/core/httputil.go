package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota + 1
	ctxKeyWorkspaceID
)

// ErrorResponse — єдиний формат помилки REST API для двомовного перекладу на фронтенді.
type ErrorResponse struct {
	ErrorCode ErrorCode `json:"error_code"`
	Message   string    `json:"message"`
}

// WriteJSON записує структуру у форматі JSON з відповідним HTTP-статусом.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError повертає стандартизовану JSON-помилку з машиночитаним кодом ErrorCode.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	WriteJSON(w, status, ErrorResponse{
		ErrorCode: code,
		Message:   message,
	})
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

// WithUserID зберігає ID авторизованого користувача в context.Context.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// UserIDFromCtx повертає ID користувача з контексту запиту.
func UserIDFromCtx(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKeyUserID).(int64)
	return id, ok && id > 0
}

// WithWorkspaceID зберігає ID поточного воркспейсу в context.Context.
func WithWorkspaceID(ctx context.Context, workspaceID int64) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspaceID, workspaceID)
}

// WorkspaceIDFromCtx повертає ID активного воркспейсу з контексту запиту.
func WorkspaceIDFromCtx(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKeyWorkspaceID).(int64)
	return id, ok && id > 0
}
