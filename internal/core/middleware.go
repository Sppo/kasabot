package core

import (
	"net/http"
	"strconv"
	"strings"
)

// AuthMiddleware перевіряє Bearer JWT-токен і зберігає user_id у контекст запиту.
func AuthMiddleware(auth *AuthService, logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				WriteAppError(w, r, logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "missing authorization header", nil))
				return
			}

			scheme, token, found := strings.Cut(authHeader, " ")
			if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
				WriteAppError(w, r, logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "invalid authorization header", nil))
				return
			}

			userID, err := auth.ParseToken(strings.TrimSpace(token))
			if err != nil {
				WriteAppError(w, r, logger, err)
				return
			}

			ctx := WithUserID(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WorkspaceMiddleware визначає поточний воркспейс (із X-Workspace-ID або active_workspace_id)
// та перевіряє, що авторизований користувач є його учасником.
func WorkspaceMiddleware(auth *AuthService, ws *WorkspaceService, logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserIDFromCtx(r.Context())
			if !ok {
				WriteAppError(w, r, logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
				return
			}

			var workspaceID int64
			rawHeader := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
			if rawHeader != "" {
				parsedID, err := strconv.ParseInt(rawHeader, 10, 64)
				if err != nil || parsedID <= 0 {
					WriteAppError(w, r, logger, NewAppError(http.StatusBadRequest, ErrValidation, "invalid X-Workspace-ID header", nil))
					return
				}
				workspaceID = parsedID
			} else {
				activeID, err := auth.GetActiveWorkspaceID(r.Context(), userID)
				if err != nil {
					WriteAppError(w, r, logger, err)
					return
				}
				workspaceID = activeID
			}

			role, err := ws.CheckMembership(r.Context(), workspaceID, userID)
			if err != nil {
				WriteAppError(w, r, logger, err)
				return
			}

			ctx := WithWorkspaceID(r.Context(), workspaceID)
			ctx = WithWorkspaceRole(ctx, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireOwnerMiddleware обмежує доступ лише для власника воркспейсу (role = 'owner').
func RequireOwnerMiddleware(logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if WorkspaceRoleFromCtx(r.Context()) != RoleOwner {
				WriteAppError(w, r, logger, NewAppError(http.StatusForbidden, ErrForbidden, "owner role required", nil))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
