package module

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/telebot.v3"
)

// Migration описує одну ідемпотентну міграцію модуля, яка накатується лише вперед.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Module визначає контракт ізольованого функціонального модуля системи.
type Module interface {
	Name() string
	Migrations() []Migration
	RegisterRoutes(r chi.Router)
	RegisterBot(b *telebot.Bot)
	OnWorkspaceCreated(ctx context.Context, workspaceID int64) error
}

// ModuleRegistry зберігає зареєстровані модулі в порядку додавання та гарантує унікальність їхніх імен.
type ModuleRegistry struct {
	modules []Module
	byName  map[string]Module
}

// NewModuleRegistry створює порожній реєстр модулів.
func NewModuleRegistry() *ModuleRegistry {
	return &ModuleRegistry{
		byName: make(map[string]Module),
	}
}

// Register додає модуль до реєстру; повертає помилку, якщо ім'я порожнє або вже зареєстроване.
func (r *ModuleRegistry) Register(m Module) error {
	if m == nil {
		return fmt.Errorf("register module: module is nil")
	}
	name := strings.TrimSpace(m.Name())
	if name == "" {
		return fmt.Errorf("register module: module name must not be empty")
	}
	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("register module: duplicate module name %q", name)
	}
	r.byName[name] = m
	r.modules = append(r.modules, m)
	return nil
}

// Modules повертає копію списку зареєстрованих модулів.
func (r *ModuleRegistry) Modules() []Module {
	out := make([]Module, len(r.modules))
	copy(out, r.modules)
	return out
}
