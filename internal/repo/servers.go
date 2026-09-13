package repo

import (
	"context"
	"database/sql"
	"errors"
)

type Server struct {
	ID                 int64
	Name               string
	Provider           string
	APIURL             string
	APIUsername        string
	APIKey             string
	Disabled           bool
	CredentialRevision int
	ProfitType         int16
	ProfitValue        float64
	// RetryLaterEnabled 该上游"等外部条件"类失败（如余额不足）是否保持自动重试；
	// 关闭则立即转人工复核。RetryLaterMinutes 为重试间隔（分钟）。
	RetryLaterEnabled bool
	RetryLaterMinutes int
}

type Servers struct{ db *sql.DB }

const serverCols = `SELECT id,name,provider,api_url,api_username,api_key,disabled,credential_revision,coalesce(profit_type,0),coalesce(profit_value,0),` +
	`retry_later_enabled,retry_later_interval_minutes`

func scanServer(row interface{ Scan(dest ...any) error }, sv *Server) error {
	return row.Scan(&sv.ID, &sv.Name, &sv.Provider, &sv.APIURL, &sv.APIUsername, &sv.APIKey,
		&sv.Disabled, &sv.CredentialRevision, &sv.ProfitType, &sv.ProfitValue,
		&sv.RetryLaterEnabled, &sv.RetryLaterMinutes)
}

var ErrServerNotFound = fixedErr("服务器不存在")

func (s *Servers) List(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, serverCols+` FROM servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var sv Server
		if err := scanServer(rows, &sv); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (s *Servers) Get(ctx context.Context, id int64) (*Server, error) {
	var sv Server
	err := scanServer(s.db.QueryRowContext(ctx, serverCols+` FROM servers WHERE id=$1`, id), &sv)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	return &sv, err
}

// DefaultRetryLaterMinutes 服务未绑定上游、或上游未配置时的默认重试间隔（分钟）。
const DefaultRetryLaterMinutes = 10

// RetryLaterPolicy 读取服务所属上游的"等外部条件"重试策略：是否保持自动重试、间隔多少分钟。
// 服务未绑定服务器（本地服务）时返回默认值。
func (s *Servers) RetryLaterPolicy(ctx context.Context, serviceID int64) (bool, int, error) {
	var enabled bool
	var minutes int
	err := s.db.QueryRowContext(ctx,
		`SELECT s.retry_later_enabled, s.retry_later_interval_minutes
		   FROM services sv
		   JOIN servers s ON s.id = coalesce(sv.server_id, (SELECT server_id FROM products WHERE id=sv.product_id))
		  WHERE sv.id=$1`, serviceID).Scan(&enabled, &minutes)
	if errors.Is(err, sql.ErrNoRows) {
		return true, DefaultRetryLaterMinutes, nil
	}
	return enabled, minutes, err
}

func (s *Servers) Create(ctx context.Context, sv *Server) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,api_username,api_key,profit_type,profit_value,
		 retry_later_enabled,retry_later_interval_minutes)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		sv.Name, sv.Provider, sv.APIURL, sv.APIUsername, sv.APIKey,
		sv.ProfitType, sv.ProfitValue, sv.RetryLaterEnabled, sv.RetryLaterMinutes).Scan(&id)
	return id, err
}

func (s *Servers) Update(ctx context.Context, sv *Server) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name=$2,provider=$3,api_url=$4,api_username=$5,api_key=$6,disabled=$7,
		 profit_type=$8,profit_value=$9,
		 retry_later_enabled=$10,retry_later_interval_minutes=$11,
		 credential_revision=CASE WHEN api_key!=$6 OR api_username!=$5 OR api_url!=$4 THEN credential_revision+1 ELSE credential_revision END
		 WHERE id=$1`,
		sv.ID, sv.Name, sv.Provider, sv.APIURL, sv.APIUsername, sv.APIKey, sv.Disabled,
		sv.ProfitType, sv.ProfitValue, sv.RetryLaterEnabled, sv.RetryLaterMinutes)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrServerNotFound
	}
	return nil
}

func (s *Servers) Delete(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM products WHERE server_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fixedErr("仍有产品绑定该服务器，先解绑再删除")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id=$1`, id)
	return err
}
