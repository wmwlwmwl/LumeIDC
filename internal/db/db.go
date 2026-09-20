package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"sort"
	"strings"
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
// 核心迁移 version 键保持裸文件名（兼容已部署库）。
func Migrate(ctx context.Context, d *sql.DB, migrations fs.FS) error {
	return migrate(ctx, d, migrations, "")
}

// MigratePrefixed 以 prefix 隔离 version 命名空间，供插件迁移使用。
// version 键为 "{prefix}/{文件名}"，避免插件与核心、插件之间同名文件冲突。
func MigratePrefixed(ctx context.Context, d *sql.DB, migrations fs.FS, prefix string) error {
	return migrate(ctx, d, migrations, strings.TrimSuffix(prefix, "/"))
}

func migrate(ctx context.Context, d *sql.DB, migrations fs.FS, prefix string) error {
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
	// 解锁同样要绑定专用连接，且失败时必须让连接作废：手动解锁落空（如报错/超时）
	// 时若把仍持有锁的连接归还连接池，后续任何迁移尝试都会一直得到
	// 「另一个迁移进程正在运行」。ErrBadConn 让 database/sql 真正关掉会话，
	// 会话断开即由 PostgreSQL 释放 advisory lock。
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtext('lumeidc_migrate'))`); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		version := name
		if prefix != "" {
			version = prefix + "/" + name
		}
		var exists bool
		if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
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
			return fmt.Errorf("迁移 %s 失败: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("迁移 %s 提交失败: %w", version, err)
		}
	}
	return nil
}
