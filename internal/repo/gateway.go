package repo

import (
	"context"
	"database/sql"
)

type Gateways struct{ DB *sql.DB }

// Config returns the JSONB config decoded as key/value strings for a gateway code.
func (g *Gateways) Config(ctx context.Context, code string) (map[string]string, error) {
	var raw []byte
	err := g.DB.QueryRowContext(ctx,
		`SELECT config FROM gateways WHERE code=$1 AND enabled`, code).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return decodeJSONStrings(raw), nil
}

// SaveConfig upserts gateway config.
func (g *Gateways) SaveConfig(ctx context.Context, code, name string, cfg map[string]string) error {
	raw := encodeJSONStrings(cfg)
	_, err := g.DB.ExecContext(ctx,
		`INSERT INTO gateways(code,name,config) VALUES($1,$2,$3)
		 ON CONFLICT(code) DO UPDATE SET name=EXCLUDED.name, config=EXCLUDED.config`,
		code, name, raw)
	return err
}
