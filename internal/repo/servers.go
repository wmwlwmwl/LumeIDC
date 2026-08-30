package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
}

type Servers struct{ DB *sql.DB }

var ErrServerNotFound = fixedErr("服务器不存在")

func (s *Servers) List(ctx context.Context) ([]Server, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id,name,provider,api_url,api_username,api_key,disabled,credential_revision,coalesce(profit_type,0),coalesce(profit_value,0) FROM servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var sv Server
		if err := rows.Scan(&sv.ID, &sv.Name, &sv.Provider, &sv.APIURL, &sv.APIUsername, &sv.APIKey, &sv.Disabled, &sv.CredentialRevision, &sv.ProfitType, &sv.ProfitValue); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (s *Servers) Get(ctx context.Context, id int64) (*Server, error) {
	var sv Server
	err := s.DB.QueryRowContext(ctx,
		`SELECT id,name,provider,api_url,api_username,api_key,disabled,credential_revision,coalesce(profit_type,0),coalesce(profit_value,0) FROM servers WHERE id=$1`, id).
		Scan(&sv.ID, &sv.Name, &sv.Provider, &sv.APIURL, &sv.APIUsername, &sv.APIKey, &sv.Disabled, &sv.CredentialRevision, &sv.ProfitType, &sv.ProfitValue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	return &sv, err
}

func (s *Servers) Create(ctx context.Context, sv *Server) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,api_username,api_key) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		sv.Name, sv.Provider, sv.APIURL, sv.APIUsername, sv.APIKey).Scan(&id)
	return id, err
}

func (s *Servers) Update(ctx context.Context, sv *Server) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE servers SET name=$2,provider=$3,api_url=$4,api_username=$5,api_key=$6,disabled=$7,
		 profit_type=$8,profit_value=$9,
		 credential_revision=CASE WHEN api_key!=$6 OR api_username!=$5 OR api_url!=$4 THEN credential_revision+1 ELSE credential_revision END
		 WHERE id=$1`,
		sv.ID, sv.Name, sv.Provider, sv.APIURL, sv.APIUsername, sv.APIKey, sv.Disabled, sv.ProfitType, sv.ProfitValue)
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
	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM products WHERE server_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fixedErr("仍有产品绑定该服务器，先解绑再删除")
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM servers WHERE id=$1`, id)
	return err
}

// IsAllowedProxyHost 判断给定主机名是否为已登记（且未禁用）的上下游面板主机，
// 供资源代理做 SSRF 防护：只允许代理到管理员配置的服务器。
func (s *Servers) IsAllowedProxyHost(ctx context.Context, host string) (bool, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM servers WHERE disabled=false AND lower(host(api_url))=$1`, strings.ToLower(host)).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
