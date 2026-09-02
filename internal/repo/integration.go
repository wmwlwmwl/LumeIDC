package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type IntegrationPlugin struct {
	ID           int64
	Domain       string
	Key          string
	Name         string
	Version      string
	Capabilities string
	ConfigJSON   string
	HasSecrets   bool
	Enabled      bool
	UpdatedAt    time.Time
}

type Integrations struct{ db *sql.DB }

func (r *Integrations) List(ctx context.Context, domain string) ([]IntegrationPlugin, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,domain,plugin_key,name,version,capabilities::text,config_json::text,(length(secret_json)>0),enabled,updated_at FROM integration_plugins WHERE ($1='' OR domain=$1) ORDER BY domain,plugin_key`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntegrationPlugin
	for rows.Next() {
		var p IntegrationPlugin
		if err := rows.Scan(&p.ID, &p.Domain, &p.Key, &p.Name, &p.Version, &p.Capabilities, &p.ConfigJSON, &p.HasSecrets, &p.Enabled, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Upsert 注册受信任的编译内 provider；不接受上传代码或任意执行入口。
func (r *Integrations) Upsert(ctx context.Context, domain, key, name, version, capabilities, configJSON string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO integration_plugins(domain,plugin_key,name,version,capabilities,config_json) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb) ON CONFLICT(domain,plugin_key) DO UPDATE SET name=EXCLUDED.name,version=EXCLUDED.version,capabilities=EXCLUDED.capabilities,config_json=EXCLUDED.config_json,updated_at=now() RETURNING id`, domain, key, name, version, capabilities, configJSON).Scan(&id)
	return id, err
}

// SetSecrets 写入已在应用层加密的 provider secrets；明文不得进入日志或响应。
func (r *Integrations) SetSecrets(ctx context.Context, id int64, encrypted []byte) error {
	_, err := r.db.ExecContext(ctx, `UPDATE integration_plugins SET secret_json=$2,updated_at=now() WHERE id=$1`, id, encrypted)
	return err
}

func (r *Integrations) Enable(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var domain string
	if err := tx.QueryRowContext(ctx, `SELECT domain FROM integration_plugins WHERE id=$1`, id).Scan(&domain); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_plugins SET enabled=false,updated_at=now() WHERE domain=$1`, domain); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_plugins SET enabled=true,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO integration_bindings(domain,plugin_id) VALUES($1,$2) ON CONFLICT(domain) DO UPDATE SET plugin_id=EXCLUDED.plugin_id,updated_at=now()`, domain, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Integrations) Disable(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE integration_plugins SET enabled=false,updated_at=now() WHERE id=$1`, id)
	return err
}
