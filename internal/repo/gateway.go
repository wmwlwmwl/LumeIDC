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

type PendingPaymentAttempt struct {
	ID, InvoiceID int64
	InvoiceNo     string
	GatewayCode   string
	Driver        string
	Config        map[string]string
	Amount        string
}

func (g *Gateways) PendingPaymentAttempts(ctx context.Context) ([]PendingPaymentAttempt, error) {
	rows, err := g.db.QueryContext(ctx, `
		SELECT p.id,p.invoice_id,i.no,p.gateway_code,g.driver,g.config,p.amount::text
		FROM payment_attempts p
		JOIN invoices i ON i.id=p.invoice_id
		JOIN gateways g ON g.code=p.gateway_code
		WHERE p.status=0 AND i.status=0 AND g.enabled
		  AND (i.due_at IS NULL OR i.due_at > now())
		ORDER BY p.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingPaymentAttempt
	for rows.Next() {
		var v PendingPaymentAttempt
		var raw []byte
		if err := rows.Scan(&v.ID, &v.InvoiceID, &v.InvoiceNo, &v.GatewayCode, &v.Driver, &raw, &v.Amount); err != nil {
			return nil, err
		}
		v.Config = decodeJSONStrings(raw)
		out = append(out, v)
	}
	return out, rows.Err()
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

// AttemptByInvoiceGatewayAmount 返回账单在指定网关下金额一致的最近一次尝试
// （不限状态、不要求账单当前仍绑定该网关）。用于识别“切换网关或重新选择支付
// 方式后到达的迟到回调”：旧尝试会被新尝试置为失效，只看最新一条会漏掉它，
// 导致已到账的钱既不核销也不退回。金额按数值比较，容忍 12.5/12.50 的写法差异。
func (g *Gateways) AttemptByInvoiceGatewayAmount(ctx context.Context, invoiceNo, code, amount string) (PaymentAttempt, error) {
	var p PaymentAttempt
	err := g.db.QueryRowContext(ctx,
		`SELECT p.id,p.invoice_id,p.gateway_code,p.amount::text,p.fee_percent::text,p.fee_amount::text,p.status
		 FROM payment_attempts p JOIN invoices i ON i.id=p.invoice_id
		 WHERE i.no=$1 AND p.gateway_code=$2 AND p.amount=$3::numeric
		 ORDER BY p.id DESC LIMIT 1`, invoiceNo, code, amount).
		Scan(&p.ID, &p.InvoiceID, &p.GatewayCode, &p.Amount, &p.FeePercent, &p.FeeAmount, &p.Status)
	return p, err
}

func (g *Gateways) InvoiceGateway(ctx context.Context, invoiceNo string) (string, error) {
	var code string
	err := g.db.QueryRowContext(ctx, `SELECT gateway FROM invoices WHERE no=$1`, invoiceNo).Scan(&code)
	return code, err
}

// UpdateAttemptAmount 把指定支付尝试的金额更新为上游返回的实际金额。
// 用于易支付 mapi.php 风控浮动场景：下游金额（如 10.01）与本地请求金额（如 10.00）
// 可能有微小差异，必须同步 payment_attempts.amount 否则回调 equalAmount 会对不上。
func (g *Gateways) UpdateAttemptAmount(ctx context.Context, attemptID int64, newAmount string) error {
	_, err := g.db.ExecContext(ctx,
		`UPDATE payment_attempts SET amount=$1::numeric WHERE id=$2`, newAmount, attemptID)
	return err
}
