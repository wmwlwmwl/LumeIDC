package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// 连接池上限为固定常量。ponytail: 单二进制单机部署足够；多实例部署需改为配置项。
const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute
)

func Open(dsn string) (*sql.DB, error) {
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// 不设上限时 pgx 默认 MaxIdle=2，高峰会频繁重建连接。
	d.SetMaxOpenConns(maxOpenConns)
	d.SetMaxIdleConns(maxIdleConns)
	d.SetConnMaxLifetime(connMaxLifetime)
	d.SetConnMaxIdleTime(connMaxIdleTime)
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}
	return d, nil
}

// Migrate applies embedded migration files in order. schema_migrations tracks applied versions.
// Uses advisory lock to prevent concurrent migrations.
func Migrate(ctx context.Context, d *sql.DB, migrations fs.FS) error {
	// 会话级 advisory lock 必须绑定专用连接：连接池上执行时加锁/解锁会落到不同连接，
	// 解锁无效且锁残留后其他实例永远报"另一个迁移进程正在运行"。
	conn, err := d.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接失败: %w", err)
	}
	defer conn.Close()
	var gotLock bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('lumeidc_migrate'))`).Scan(&gotLock); err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}
	if !gotLock {
		return fmt.Errorf("另一个迁移进程正在运行")
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('lumeidc_migrate'))`)

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
