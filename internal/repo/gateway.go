package repo

import (
	"context"
	"database/sql"
)

type Gateways struct{ DB *sql.DB }

type Gateway struct {
	ID      int64
	Code    string
	Driver  string
	Name    string
	Config  map[string]string
	Enabled bool
	Sort    int
}

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

func (g *Gateways) Enabled(ctx context.Context) ([]Gateway, error) {
	rows, err := g.DB.QueryContext(ctx, `SELECT id,code,driver,name,config,enabled,sort FROM gateways WHERE enabled=true ORDER BY sort,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gateway
	for rows.Next() {
		var v Gateway
		var raw []byte
		if err := rows.Scan(&v.ID, &v.Code, &v.Driver, &v.Name, &raw, &v.Enabled, &v.Sort); err != nil {
			return nil, err
		}
		v.Config = decodeJSONStrings(raw)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (g *Gateways) Get(ctx context.Context, code string) (Gateway, error) {
	var v Gateway
	var raw []byte
	err := g.DB.QueryRowContext(ctx,
		`SELECT id,code,driver,name,config,enabled,sort FROM gateways WHERE code=$1`, code).
		Scan(&v.ID, &v.Code, &v.Driver, &v.Name, &raw, &v.Enabled, &v.Sort)
	if err != nil {
		return Gateway{}, err
	}
	v.Config = decodeJSONStrings(raw)
	return v, nil
}

func (g *Gateways) List(ctx context.Context) ([]Gateway, error) {
	rows, err := g.DB.QueryContext(ctx, `SELECT id,code,driver,name,config,enabled,sort FROM gateways ORDER BY sort,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gateway
	for rows.Next() {
		var v Gateway
		var raw []byte
		if err := rows.Scan(&v.ID, &v.Code, &v.Driver, &v.Name, &raw, &v.Enabled, &v.Sort); err != nil {
			return nil, err
		}
		v.Config = decodeJSONStrings(raw)
		out = append(out, v)
	}
	return out, rows.Err()
}

// SaveConfig upserts gateway config.
func (g *Gateways) SaveConfig(ctx context.Context, code, driver, name string, cfg map[string]string, enabled bool, sort int) error {
	raw := encodeJSONStrings(cfg)
	_, err := g.DB.ExecContext(ctx,
		`INSERT INTO gateways(code,driver,name,config,enabled,sort) VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(code) DO UPDATE SET driver=EXCLUDED.driver,name=EXCLUDED.name,config=EXCLUDED.config,enabled=EXCLUDED.enabled,sort=EXCLUDED.sort`,
		code, driver, name, raw, enabled, sort)
	return err
}

func (g *Gateways) Delete(ctx context.Context, code string) error {
	_, err := g.DB.ExecContext(ctx, `DELETE FROM gateways WHERE code=$1`, code)
	return err
}

func (g *Gateways) BindAttempt(ctx context.Context, invoiceID int64, code, amount string) (int64, error) {
	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO payment_attempts(invoice_id,gateway_code,amount) VALUES($1,$2,$3) RETURNING id`, invoiceID, code, amount).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoices SET gateway=$2 WHERE id=$1 AND status=0`, invoiceID, code); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (g *Gateways) InvoiceGateway(ctx context.Context, invoiceNo string) (string, error) {
	var code string
	err := g.DB.QueryRowContext(ctx, `SELECT gateway FROM invoices WHERE no=$1`, invoiceNo).Scan(&code)
	return code, err
}
