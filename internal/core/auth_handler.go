package core

import (
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// AuthHandler реалізує тонкі HTTP-хендлери для /auth/* та /workspaces/*.
type AuthHandler struct {
	auth   *AuthService
	ws     *WorkspaceService
	cfg    *Config
	logger *Logger
}

// NewAuthHandler створює контролер авторизації та воркспейсів.
func NewAuthHandler(auth *AuthService, ws *WorkspaceService, cfg *Config, logger *Logger) *AuthHandler {
	return &AuthHandler{
		auth:   auth,
		ws:     ws,
		cfg:    cfg,
		logger: logger,
	}
}

// Routes повертає роутер з ендпоінтами /auth/* та /workspaces/* згідно з SPEC §5.3.
func (h *AuthHandler) Routes() chi.Router {
	r := chi.NewRouter()

	// Публічні маршрути
	r.Post("/auth/register", h.handleRegister)
	r.Post("/auth/login", h.handleLogin)

	// Захищені маршрути (потрібен лише валідний JWT, без обов'язкового X-Workspace-ID)
	r.Group(func(pr chi.Router) {
		pr.Use(AuthMiddleware(h.auth, h.logger))

		pr.Get("/auth/me", h.handleMe)
		pr.Post("/auth/telegram-link", h.handleCreateTelegramLink)

		pr.Post("/workspaces", h.handleCreateWorkspace)
		pr.Post("/workspaces/join", h.handleJoinWorkspace)
		pr.Post("/workspaces/{id}/activate", h.handleActivateWorkspace)
		pr.Post("/workspaces/{id}/invites", h.handleCreateInvite)
	})

	return r
}

type registerRequest struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
}

func (h *AuthHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := DecodeJSON(w, r, &req, h.cfg.MaxJSONBodyBytes); err != nil {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusBadRequest, ErrValidation, "invalid request body", err))
		return
	}

	res, err := h.auth.Register(r.Context(), req.Email, req.Password, req.Name, req.WorkspaceName)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := DecodeJSON(w, r, &req, h.cfg.MaxJSONBodyBytes); err != nil {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusBadRequest, ErrValidation, "invalid request body", err))
		return
	}

	res, err := h.auth.Login(r.Context(), req.Email, req.Password, extractClientIP(r))
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *AuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	res, err := h.auth.Me(r.Context(), userID)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *AuthHandler) handleCreateTelegramLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	res, err := h.auth.CreateTelegramLink(r.Context(), userID)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

type createWorkspaceRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

func (h *AuthHandler) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	var req createWorkspaceRequest
	if err := DecodeJSON(w, r, &req, h.cfg.MaxJSONBodyBytes); err != nil {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusBadRequest, ErrValidation, "invalid request body", err))
		return
	}

	ws, err := h.ws.CreateWorkspace(r.Context(), userID, req.Name, req.Currency)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusCreated, ws)
}

func (h *AuthHandler) handleActivateWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	wsID, err := parsePositiveIDParam(r, "id")
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}

	if err := h.ws.ActivateWorkspace(r.Context(), userID, wsID); err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"active_workspace_id": wsID})
}

func (h *AuthHandler) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	wsID, err := parsePositiveIDParam(r, "id")
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}

	res, err := h.ws.CreateInvite(r.Context(), userID, wsID)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

type joinWorkspaceRequest struct {
	Code string `json:"code"`
}

func (h *AuthHandler) handleJoinWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromCtx(r.Context())
	if !ok {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusUnauthorized, ErrUnauthorized, "unauthorized", nil))
		return
	}

	var req joinWorkspaceRequest
	if err := DecodeJSON(w, r, &req, h.cfg.MaxJSONBodyBytes); err != nil {
		WriteAppError(w, r, h.logger, NewAppError(http.StatusBadRequest, ErrValidation, "invalid request body", err))
		return
	}

	ws, err := h.ws.JoinWorkspace(r.Context(), userID, req.Code)
	if err != nil {
		WriteAppError(w, r, h.logger, err)
		return
	}
	WriteJSON(w, http.StatusOK, ws)
}

func parsePositiveIDParam(r *http.Request, name string) (int64, error) {
	raw := chi.URLParam(r, name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, NewAppError(http.StatusBadRequest, ErrValidation, "invalid "+name+" parameter", nil)
	}
	return id, nil
}

func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
