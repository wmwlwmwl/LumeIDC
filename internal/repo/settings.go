package repo

import (
	"context"
	"database/sql"
)

type Settings struct{ DB *sql.DB }

func (s *Settings) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	return v, err
}

func (s *Settings) Set(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`,
		key, value)
	return err
}
