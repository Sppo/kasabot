package core

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Фіксований ключ advisory-блокування PostgreSQL для ініціалізації схеми та накатування міграцій.
	migrationsAdvisoryLockID int64 = 748291035
	advisoryUnlockTimeout          = 5 * time.Second
)

// OpenDB створює пул з'єднань pgxpool, перевіряє доступність PostgreSQL через Ping та ініціалізує системну схему ядра.
func OpenDB(ctx context.Context, cfg *Config, logger *Logger) (*pgxpool.Pool, error) {
	if cfg == nil {
		return nil, fmt.Errorf("open db: config is nil")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		// Не логуємо cfg.DatabaseURL, щоб пароль не потрапив у логи чи повідомлення про помилки
		return nil, fmt.Errorf("parse database config: invalid DATABASE_URL format")
	}

	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.ConnConfig.ConnectTimeout = cfg.DBConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		if logger != nil {
			logger.LogError(ctx, ErrInternal, "postgres_ping_failed", slog.String("error", err.Error()))
		}
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := initCoreSchema(ctx, pool); err != nil {
		pool.Close()
		if logger != nil {
			logger.LogError(ctx, ErrInternal, "init_core_schema_failed", slog.String("error", err.Error()))
		}
		return nil, fmt.Errorf("init core schema: %w", err)
	}

	return pool, nil
}

// withAdvisoryLock бере сесійний pg_advisory_lock на одному з'єднанні з пулу, виконує fn
// і гарантовано знімає замок (а при помилці/false знищує з'єднання через Hijack+Close, щоб замок не залип).
func withAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, lockID int64, fn func(conn *pgxpool.Conn) error) error {
	if pool == nil {
		return fmt.Errorf("advisory lock: pool is nil")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for advisory lock: %w", err)
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		conn.Release()
		return fmt.Errorf("acquire advisory lock: %w", err)
	}

	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), advisoryUnlockTimeout)
		defer cancel()

		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			// Закриваємо фізичне з'єднання, щоб PostgreSQL гарантовано зняв сесійний замок
			rawConn := conn.Hijack()
			_ = rawConn.Close(unlockCtx)
			return
		}
		conn.Release()
	}()

	return fn(conn)
}

// initCoreSchema створює системні таблиці ядра за SPEC.md під advisory-блокуванням.
func initCoreSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const coreSchemaSQL = `
CREATE TABLE IF NOT EXISTS users (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	email TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	name TEXT NOT NULL,
	telegram_id BIGINT UNIQUE NULL,
	active_workspace_id BIGINT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workspaces (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	name TEXT NOT NULL,
	currency TEXT NOT NULL DEFAULT 'UAH',
	owner_id BIGINT NOT NULL REFERENCES users(id),
	plan_status TEXT NOT NULL DEFAULT 'trial',
	subscription_ends_at TIMESTAMPTZ NOT NULL,
	ai_requests_today INTEGER NOT NULL DEFAULT 0,
	ai_requests_date DATE NOT NULL DEFAULT CURRENT_DATE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workspace_members (
	workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL DEFAULT 'member',
	PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_members_user_id ON workspace_members(user_id);

CREATE TABLE IF NOT EXISTS workspace_invites (
	code TEXT PRIMARY KEY,
	workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS telegram_link_tokens (
	token TEXT PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
	module TEXT NOT NULL,
	version INTEGER NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (module, version)
);
`
	return withAdvisoryLock(ctx, pool, migrationsAdvisoryLockID, func(conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, coreSchemaSQL); err != nil {
			return fmt.Errorf("exec core schema DDL: %w", err)
		}
		return nil
	})
}

// RunModuleMigrations накатує нові міграції модулів у транзакціях із захистом pg_advisory_lock.
func RunModuleMigrations(ctx context.Context, pool *pgxpool.Pool, modules []Module, logger *Logger) error {
	return withAdvisoryLock(ctx, pool, migrationsAdvisoryLockID, func(conn *pgxpool.Conn) error {
		for _, mod := range modules {
			if err := applyModuleMigrations(ctx, conn, mod, logger); err != nil {
				return err
			}
		}
		return nil
	})
}

func applyModuleMigrations(ctx context.Context, conn *pgxpool.Conn, mod Module, logger *Logger) error {
	modName := mod.Name()
	migs := append([]Migration(nil), mod.Migrations()...)
	if len(migs) == 0 {
		return nil
	}

	sort.Slice(migs, func(i, j int) bool {
		return migs[i].Version < migs[j].Version
	})

	seenVersions := make(map[int]bool, len(migs))
	for _, m := range migs {
		if m.Version <= 0 {
			return fmt.Errorf("module %q migration %q: version must be > 0, got %d", modName, m.Name, m.Version)
		}
		if seenVersions[m.Version] {
			return fmt.Errorf("module %q: duplicate migration version %d", modName, m.Version)
		}
		seenVersions[m.Version] = true
	}

	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations WHERE module = $1", modName)
	if err != nil {
		return fmt.Errorf("query applied migrations for %q: %w", modName, err)
	}
	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if scanErr := rows.Scan(&v); scanErr != nil {
			rows.Close()
			return fmt.Errorf("scan migration version for %q: %w", modName, scanErr)
		}
		applied[v] = true
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return fmt.Errorf("iterate migration versions for %q: %w", modName, rowsErr)
	}
	rows.Close()

	for _, m := range migs {
		if applied[m.Version] {
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration tx (%s v%d): %w", modName, m.Version, err)
		}

		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("exec migration (%s v%d %s): %w", modName, m.Version, m.Name, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (module, version, applied_at) VALUES ($1, $2, NOW())", modName, m.Version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration (%s v%d): %w", modName, m.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration (%s v%d): %w", modName, m.Version, err)
		}

		if logger != nil {
			logger.Slog().InfoContext(ctx, "migration_applied",
				slog.String("module", modName),
				slog.Int("version", m.Version),
				slog.String("name", m.Name),
			)
		}
	}

	return nil
}
