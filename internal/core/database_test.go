package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/telebot.v3"
)

const fallbackTestDatabaseURL = "postgres://kasabot:kasabot@127.0.0.1:5432/kasabot?sslmode=disable"

type testModule struct {
	name       string
	migrations []Migration
}

func (m *testModule) Name() string                { return m.name }
func (m *testModule) Migrations() []Migration     { return m.migrations }
func (m *testModule) RegisterRoutes(_ chi.Router) {}
func (m *testModule) RegisterBot(_ *telebot.Bot)  {}
func (m *testModule) OnWorkspaceCreated(_ context.Context, _ int64) error {
	return nil
}

// openTestSchemaPool підключається до реального Postgres (або падає з підказкою «запусти make db-up»),
// створює ізольовану тимчасову схему з search_path та видаляє її після завершення тесту.
func openTestSchemaPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	t.Setenv("JWT_SECRET", "test-secret-at-least-16-chars")
	cfg, err := LoadConfig("../../.env")
	if err != nil {
		// Якщо .env не створено локально, використовуємо стандартний рядок підключення з .env.example
		t.Setenv("DATABASE_URL", fallbackTestDatabaseURL)
		cfg, err = LoadConfig("")
		if err != nil {
			t.Fatalf("load test config: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DBConnectTimeout)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("PostgreSQL недоступний — запусти make db-up: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("PostgreSQL недоступний — запусти make db-up: %v", err)
	}

	schemaName := fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), NewErrorID())
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
		adminPool.Close()
		t.Fatalf("create temp schema %s: %v", schemaName, err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		adminPool.Close()
		t.Fatalf("parse test DatabaseURL: %v", err)
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.ConnConfig.ConnectTimeout = cfg.DBConnectTimeout
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schemaName

	schemaPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		_, _ = adminPool.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		adminPool.Close()
		t.Fatalf("create schema pool: %v", err)
	}

	t.Cleanup(func() {
		schemaPool.Close()
		_, _ = adminPool.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		adminPool.Close()
	})

	if err := initCoreSchema(context.Background(), schemaPool); err != nil {
		t.Fatalf("initCoreSchema failed: %v", err)
	}

	return schemaPool, schemaName
}

func TestModuleRegistryUniqueness(t *testing.T) {
	reg := NewModuleRegistry()

	if err := reg.Register(nil); err == nil {
		t.Fatal("expected error for nil module")
	}
	if err := reg.Register(&testModule{name: "   "}); err == nil {
		t.Fatal("expected error for empty module name")
	}
	if err := reg.Register(&testModule{name: "finance"}); err != nil {
		t.Fatalf("register finance failed: %v", err)
	}
	if err := reg.Register(&testModule{name: "finance"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate module error, got: %v", err)
	}

	if got := len(reg.Modules()); got != 1 {
		t.Fatalf("expected 1 registered module, got %d", got)
	}
}

func TestOpenDBAndCoreSchema(t *testing.T) {
	pool, schemaName := openTestSchemaPool(t)
	ctx := context.Background()

	expectedTables := []string{
		"users",
		"workspaces",
		"workspace_members",
		"workspace_invites",
		"telegram_link_tokens",
		"schema_migrations",
	}

	for _, table := range expectedTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = $1 AND table_name = $2
			)`,
			schemaName, table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected core table %q to exist in schema %q", table, schemaName)
		}
	}

	// Перевіряємо BIGINT GENERATED ALWAYS AS IDENTITY та зовнішні ключі
	var userID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name) VALUES ($1, $2, $3) RETURNING id`,
		"owner@example.com", "hash", "Owner",
	).Scan(&userID)
	if err != nil || userID <= 0 {
		t.Fatalf("insert user failed: id=%d err=%v", userID, err)
	}

	var wsID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO workspaces (name, currency, owner_id, subscription_ends_at) VALUES ($1, $2, $3, NOW() + INTERVAL '14 days') RETURNING id`,
		"ФОП", "UAH", userID,
	).Scan(&wsID)
	if err != nil || wsID <= 0 {
		t.Fatalf("insert workspace failed: id=%d err=%v", wsID, err)
	}
}

func TestRunModuleMigrationsIdempotency(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	ctx := context.Background()

	mod := &testModule{
		name: "finance",
		migrations: []Migration{
			{
				Version: 2,
				Name:    "create_items",
				SQL:     `CREATE TABLE fin_items (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, title TEXT NOT NULL);`,
			},
			{
				Version: 1,
				Name:    "create_categories",
				SQL:     `CREATE TABLE fin_categories (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name TEXT NOT NULL);`,
			},
		},
	}

	if err := RunModuleMigrations(ctx, pool, []Module{mod}, nil); err != nil {
		t.Fatalf("first RunModuleMigrations failed: %v", err)
	}

	var firstAppliedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE module = $1 AND version = 1`, "finance").Scan(&firstAppliedAt); err != nil {
		t.Fatalf("query applied_at: %v", err)
	}

	// Повторний запуск має бути ідемпотентним і не перезаписувати вже застосовані міграції
	if err := RunModuleMigrations(ctx, pool, []Module{mod}, nil); err != nil {
		t.Fatalf("second RunModuleMigrations failed: %v", err)
	}

	var count int
	var secondAppliedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE module = $1`, "finance").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 applied migrations, got %d", count)
	}
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE module = $1 AND version = 1`, "finance").Scan(&secondAppliedAt); err != nil {
		t.Fatalf("query second applied_at: %v", err)
	}
	if !firstAppliedAt.Equal(secondAppliedAt) {
		t.Errorf("expected applied_at to remain unchanged on re-run: %v != %v", firstAppliedAt, secondAppliedAt)
	}
}

func TestRunModuleMigrationsRollbackOnError(t *testing.T) {
	pool, schemaName := openTestSchemaPool(t)
	ctx := context.Background()

	mod := &testModule{
		name: "finance",
		migrations: []Migration{
			{
				Version: 1,
				Name:    "valid_v1",
				SQL:     `CREATE TABLE fin_valid (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY);`,
			},
			{
				Version: 2,
				Name:    "broken_v2",
				SQL:     `CREATE TABLE fin_broken (id BIGINT PRIMARY KEY); INVALID SQL SYNTAX HERE;`,
			},
		},
	}

	err := RunModuleMigrations(ctx, pool, []Module{mod}, nil)
	if err == nil {
		t.Fatal("expected error on broken v2 migration, got nil")
	}

	// v1 має зберегтися, а v2 та її таблиця fin_broken мають відкотитися транзакцією
	var v2Applied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE module = 'finance' AND version = 2)`).Scan(&v2Applied); err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}
	if v2Applied {
		t.Error("expected v2 NOT to be recorded in schema_migrations after rollback")
	}

	var brokenTableExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'fin_broken')`,
		schemaName,
	).Scan(&brokenTableExists); err != nil {
		t.Fatalf("check fin_broken table: %v", err)
	}
	if brokenTableExists {
		t.Error("expected fin_broken DDL to be rolled back on migration error")
	}
}

func TestRunModuleMigrationsConcurrentLock(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	ctx := context.Background()

	mod := &testModule{
		name: "finance",
		migrations: []Migration{
			{
				Version: 1,
				Name:    "concurrent_table",
				SQL:     `CREATE TABLE fin_concurrent (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY);`,
			},
		},
	}

	const workers = 5
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- RunModuleMigrations(ctx, pool, []Module{mod}, nil)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RunModuleMigrations returned error: %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE module = 'finance'`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 recorded migration after concurrent runs, got %d", count)
	}

	// Перевіряємо з іншого з'єднання, що advisory-замок гарантовано вільний після завершення RunModuleMigrations
	assertAdvisoryLockFree(t, pool)
}

func TestRunModuleMigrationsCanceledContextReleasesLock(t *testing.T) {
	pool, _ := openTestSchemaPool(t)

	// Створюємо контекст, який скасовується під час виконання довгої міграції
	cancelCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	mod := &testModule{
		name: "finance",
		migrations: []Migration{
			{
				Version: 1,
				Name:    "slow_migration",
				SQL:     `SELECT pg_sleep(1);`,
			},
		},
	}

	err := RunModuleMigrations(cancelCtx, pool, []Module{mod}, nil)
	if err == nil {
		t.Fatal("expected error when context is canceled during migration")
	}

	// Навіть при скасованому ctx інше з'єднання має вільно отримати advisory-замок
	assertAdvisoryLockFree(t, pool)
}

func TestInitCoreSchemaConcurrent(t *testing.T) {
	pool, _ := openTestSchemaPool(t)
	ctx := context.Background()

	// Очищаємо таблиці у тимчасовій схемі, щоб протестувати одночасну ініціалізацію з нуля
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations, telegram_link_tokens, workspace_invites, workspace_members, workspaces, users CASCADE`); err != nil {
		t.Fatalf("drop core tables: %v", err)
	}

	const workers = 5
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- initCoreSchema(ctx, pool)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent initCoreSchema failed: %v", err)
		}
	}

	assertAdvisoryLockFree(t, pool)
}

func assertAdvisoryLockFree(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire check conn: %v", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", migrationsAdvisoryLockID).Scan(&acquired); err != nil {
		t.Fatalf("pg_try_advisory_lock failed: %v", err)
	}
	if !acquired {
		t.Fatal("advisory lock is still held after operation completed")
	}
	_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationsAdvisoryLockID)
}
