package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func Open(dsn string) (*sql.DB, error) {
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}
	return d, nil
}

// Migrate applies embedded migration files in order. schema_migrations tracks applied versions.
// Uses advisory lock to prevent concurrent migrations.
func Migrate(ctx context.Context, d *sql.DB, migrations fs.FS) error {
	// 获取迁移锁，防止多实例并发迁移
	var gotLock bool
	if err := d.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('lumeidc_migrate'))`).Scan(&gotLock); err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}
	if !gotLock {
		return fmt.Errorf("另一个迁移进程正在运行")
	}
	defer d.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('lumeidc_migrate'))`)

	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		var exists bool
		if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		b, err := fs.ReadFile(migrations, name)
		if err != nil {
			return err
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(b)); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("迁移 %s 提交失败: %w", name, err)
		}
	}
	return nil
}
