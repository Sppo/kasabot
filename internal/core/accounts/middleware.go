package accounts

import (
	"net/http"
	"strconv"
	"strings"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/httpx"
	"kasabot/internal/core/logging"
	"kasabot/internal/core/reqctx"
)

// AuthMiddleware перевіряє Bearer JWT-токен і зберігає user_id у контекст запиту.
func AuthMiddleware(auth *AuthService, logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				httpx.WriteAppError(w, r, logger, apperr.NewAppError(http.StatusUnauthorized, apperr.ErrUnauthorized, "missing authorization header", nil))
				return
			}

			scheme, token, found := strings.Cut(authHeader, " ")
			if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
				httpx.WriteAppError(w, r, logger, apperr.NewAppError(http.StatusUnauthorized, apperr.ErrUnauthorized, "invalid authorization header", nil))
				return
			}

			userID, err := auth.ParseToken(strings.TrimSpace(token))
			if err != nil {
				httpx.WriteAppError(w, r, logger, err)
				return
			}

			ctx := reqctx.WithUserID(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WorkspaceMiddleware визначає поточний воркспейс (із X-Workspace-ID або active_workspace_id)
// та перевіряє, що авторизований користувач є його учасником.
func WorkspaceMiddleware(auth *AuthService, ws *WorkspaceService, logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := reqctx.UserIDFromCtx(r.Context())
			if !ok {
				httpx.WriteAppError(w, r, logger, apperr.NewAppError(http.StatusUnauthorized, apperr.ErrUnauthorized, "unauthorized", nil))
				return
			}

			var workspaceID int64
			rawHeader := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
			if rawHeader != "" {
				parsedID, err := strconv.ParseInt(rawHeader, 10, 64)
				if err != nil || parsedID <= 0 {
					httpx.WriteAppError(w, r, logger, apperr.NewAppError(http.StatusBadRequest, apperr.ErrValidation, "invalid X-Workspace-ID header", nil))
					return
				}
				workspaceID = parsedID
			} else {
				activeID, err := auth.GetActiveWorkspaceID(r.Context(), userID)
				if err != nil {
					httpx.WriteAppError(w, r, logger, err)
					return
				}
				workspaceID = activeID
			}

			role, err := ws.CheckMembership(r.Context(), workspaceID, userID)
			if err != nil {
				httpx.WriteAppError(w, r, logger, err)
				return
			}

			ctx := reqctx.WithWorkspaceID(r.Context(), workspaceID)
			ctx = reqctx.WithWorkspaceRole(ctx, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireOwnerMiddleware обмежує доступ лише для власника воркспейсу (role = 'owner').
func RequireOwnerMiddleware(logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reqctx.WorkspaceRoleFromCtx(r.Context()) != RoleOwner {
				httpx.WriteAppError(w, r, logger, apperr.NewAppError(http.StatusForbidden, apperr.ErrForbidden, "owner role required", nil))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
