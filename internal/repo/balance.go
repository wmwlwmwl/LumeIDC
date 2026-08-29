package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Balance 用户余额操作（原子扣减/充值）。
type Balance struct{ DB *sql.DB }

var ErrInsufficientBalance = errors.New("余额不足")

// Recharge 充值（管理员操作或在线充值回调）。
func (b *Balance) Recharge(ctx context.Context, userID int64, amount string, note string) error {
	if amount == "" {
		return fmt.Errorf("金额必须大于 0")
	}
	return b.change(ctx, userID, amount, "recharge", note)
}

// AdminAdjust 管理员调整（正负皆可）。
func (b *Balance) AdminAdjust(ctx context.Context, userID int64, amount string, note string) error {
	if amount == "" {
		return fmt.Errorf("金额不能为 0")
	}
	return b.change(ctx, userID, amount, "admin", note)
}

// ConsumeAmount 在已有事务内扣减余额（string 金额，NUMERIC 精度）。
func (b *Balance) ConsumeAmount(ctx context.Context, tx *sql.Tx, userID int64, amount string, note string) error {
	if amount == "" {
		return fmt.Errorf("金额必须大于 0")
	}
	var enough bool
	if err := tx.QueryRowContext(ctx,
		`SELECT balance >= $2::numeric FROM users WHERE id=$1 FOR UPDATE`, userID, amount).Scan(&enough); err != nil {
		return err
	}
	if !enough {
		return ErrInsufficientBalance
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance-$2::numeric WHERE id=$1`, userID, amount); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,-$2::numeric,balance,'consume',$3 FROM users WHERE id=$1`, userID, amount, note)
	return err
}

func (b *Balance) change(ctx context.Context, userID int64, amount string, typ, note string) error {
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enough bool
	if typ == "recharge" {
		// 充值：金额必须 > 0
		if err := tx.QueryRowContext(ctx,
			`SELECT $2::numeric > 0`, userID, amount).Scan(&enough); err != nil {
			return err
		}
		if !enough {
			return fmt.Errorf("金额必须大于 0")
		}
	} else {
		// 管理员调整：正负皆可，但不能为 0
		if err := tx.QueryRowContext(ctx,
			`SELECT $2::numeric != 0`, userID, amount).Scan(&enough); err != nil {
			return err
		}
		if !enough {
			return fmt.Errorf("金额不能为 0")
		}
		// 负数调整时检查余额是否充足
		var canAfford bool
		if err := tx.QueryRowContext(ctx,
			`SELECT balance + $2::numeric >= 0 FROM users WHERE id=$1 FOR UPDATE`, userID, amount).Scan(&canAfford); err != nil {
			return err
		}
		if !canAfford {
			return ErrInsufficientBalance
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET balance = balance + $2::numeric WHERE id=$1`, userID, amount); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,$2::numeric,balance,$3,$4 FROM users WHERE id=$1`, userID, amount, typ, note); err != nil {
		return err
	}
	return tx.Commit()
}

// Get 读取余额（返回字符串，NUMERIC 精度）。
func (b *Balance) Get(ctx context.Context, userID int64) (string, error) {
	var v string
	err := b.DB.QueryRowContext(ctx,
		`SELECT balance::text FROM users WHERE id=$1`, userID).Scan(&v)
	return v, err
}

// Logs 流水。
func (b *Balance) Logs(ctx context.Context, userID int64) ([]map[string]any, error) {
	rows, err := b.DB.QueryContext(ctx,
		`SELECT to_char(created_at,'YYYY-MM-DD HH24:MI'), amount::text, balance_after::text, type, note
		 FROM balance_logs WHERE user_id=$1 ORDER BY id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var t, typ, note, amt, after string
		if err := rows.Scan(&t, &amt, &after, &typ, &note); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"time": t, "amount": amt, "after": after, "type": typ, "note": note})
	}
	return out, rows.Err()
}
