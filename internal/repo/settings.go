package repo

import (
	"context"
	"database/sql"
)

type Settings struct{ db *sql.DB }

func (s *Settings) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	return v, err
}

func (s *Settings) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`,
		key, value)
	return err
}

// Bool 读取开关类设置：值恰为 "1" 视为开；缺失或出错回退 fallback。
func (s *Settings) Bool(ctx context.Context, key string, fallback bool) bool {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v == "1"
}

// GetMany 一次读取多项设置。返回 map 中只包含数据库实际存在的键；
// 调用方可自行提供各键的默认值。空 keys 返回空 map。
func (s *Settings) GetMany(ctx context.Context, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM settings WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}
