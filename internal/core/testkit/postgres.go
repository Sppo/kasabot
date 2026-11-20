package testkit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kasabot/internal/core/config"
	"kasabot/internal/core/db"
	"kasabot/internal/core/logging"
)

const fallbackTestDatabaseURL = "postgres://kasabot:kasabot@127.0.0.1:5432/kasabot?sslmode=disable"

// OpenTestSchemaPool підключається до реального Postgres, створює ізольовану тимчасову схему з search_path
// та видаляє її після завершення тесту.
func OpenTestSchemaPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	t.Setenv("JWT_SECRET", "test-secret-at-least-16-chars")
	cfg, err := config.LoadConfig("../../../.env")
	if err != nil {
		// Якщо .env не створено локально, використовуємо стандартний рядок підключення з .env.example
		t.Setenv("DATABASE_URL", fallbackTestDatabaseURL)
		cfg, err = config.LoadConfig("")
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

	schemaName := fmt.Sprintf("test_%d_%s", time.Now().UnixNano(), logging.NewErrorID())
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

	if err := db.InitCoreSchema(context.Background(), schemaPool); err != nil {
		t.Fatalf("InitCoreSchema failed: %v", err)
	}

	return schemaPool, schemaName
}
