package repo

import (
	"context"
	"database/sql"
	"errors"
)

type Gateways struct{ db *sql.DB }

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
	err := g.db.QueryRowContext(ctx,
		`SELECT config FROM gateways WHERE code=$1 AND enabled`, code).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return decodeJSONStrings(raw), nil
}

func (g *Gateways) Enabled(ctx context.Context) ([]Gateway, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT id,code,driver,name,config,enabled,sort FROM gateways WHERE enabled=true ORDER BY sort,id`)
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
	err := g.db.QueryRowContext(ctx,
		`SELECT id,code,driver,name,config,enabled,sort FROM gateways WHERE code=$1`, code).
		Scan(&v.ID, &v.Code, &v.Driver, &v.Name, &raw, &v.Enabled, &v.Sort)
	if err != nil {
		return Gateway{}, err
	}
	v.Config = decodeJSONStrings(raw)
	return v, nil
}

func (g *Gateways) List(ctx context.Context) ([]Gateway, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT id,code,driver,name,config,enabled,sort FROM gateways ORDER BY sort,id`)
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
	_, err := g.db.ExecContext(ctx,
		`INSERT INTO gateways(code,driver,name,config,enabled,sort) VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(code) DO UPDATE SET driver=EXCLUDED.driver,name=EXCLUDED.name,config=EXCLUDED.config,enabled=EXCLUDED.enabled,sort=EXCLUDED.sort`,
		code, driver, name, raw, enabled, sort)
	return err
}

func (g *Gateways) Delete(ctx context.Context, code string) error {
	var pending bool
	if err := g.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE gateway_code=$1 AND status=0)`, code).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errors.New("该网关有进行中的支付，暂不能删除")
	}
	res, err := g.db.ExecContext(ctx, `DELETE FROM gateways WHERE code=$1`, code)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (g *Gateways) BindAttempt(ctx context.Context, invoiceID int64, code, amount string, fee ...string) (int64, error) {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	var status int16
	if err := tx.QueryRowContext(ctx, `SELECT id,status FROM invoices WHERE id=$1 FOR UPDATE`, invoiceID).Scan(&id, &status); err != nil {
		return 0, err
	}
	if status != 0 {
		return 0, errors.New("账单不可支付")
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE invoice_id=$1 AND status=0)`, invoiceID).Scan(&pending); err != nil {
		return 0, err
	}
	if pending {
		return 0, errors.New("账单已有支付进行中")
	}
	feePercent, feeAmount := "0.00", "0.00"
	if len(fee) > 0 && fee[0] != "" {
		feePercent = fee[0]
	}
	if len(fee) > 1 && fee[1] != "" {
		feeAmount = fee[1]
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO payment_attempts(invoice_id,gateway_code,amount,fee_percent,fee_amount) VALUES($1,$2,$3,$4,$5) RETURNING id`, invoiceID, code, amount, feePercent, feeAmount).Scan(&id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE invoices SET gateway=$2 WHERE id=$1 AND status=0`, invoiceID, code)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return 0, errors.New("账单状态已变更")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// PaymentAttempt is the immutable amount snapshot for one gateway attempt.
type PaymentAttempt struct {
	ID          int64
	InvoiceID   int64
	GatewayCode string
	Amount      string
	FeePercent  string
	FeeAmount   string
	Status      int16
}

// LatestAttempt returns the latest attempt for an invoice and gateway. Pending
// attempts are preferred so a late callback cannot be matched to a failed one.
func (g *Gateways) LatestAttempt(ctx context.Context, invoiceNo, code string, pendingOnly bool) (PaymentAttempt, error) {
	query := `SELECT p.id,p.invoice_id,p.gateway_code,p.amount::text,p.fee_percent::text,p.fee_amount::text,p.status
		FROM payment_attempts p JOIN invoices i ON i.id=p.invoice_id
		WHERE i.no=$1 AND i.gateway=$2 AND p.gateway_code=$2`
	if pendingOnly {
		query += ` AND p.status=0`
	}
	query += ` ORDER BY p.id DESC LIMIT 1`
	var p PaymentAttempt
	err := g.db.QueryRowContext(ctx, query, invoiceNo, code).
		Scan(&p.ID, &p.InvoiceID, &p.GatewayCode, &p.Amount, &p.FeePercent, &p.FeeAmount, &p.Status)
	return p, err
}

func (g *Gateways) MarkAttemptFailedByID(ctx context.Context, id int64) error {
	_, err := g.db.ExecContext(ctx, `UPDATE payment_attempts SET status=2 WHERE id=$1 AND status=0`, id)
	return err
}

func (g *Gateways) InvoiceGateway(ctx context.Context, invoiceNo string) (string, error) {
	var code string
	err := g.db.QueryRowContext(ctx, `SELECT gateway FROM invoices WHERE no=$1`, invoiceNo).Scan(&code)
	return code, err
}
