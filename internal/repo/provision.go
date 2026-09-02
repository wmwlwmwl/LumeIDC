package repo

import (
	"context"
	"database/sql"
)

// ProvisionRepo 服务实例的上游凭据与检查点存取。
type ProvisionRepo struct{ db *sql.DB }

// SetUpstreamHostID 写入开通结果。
func (p *ProvisionRepo) SetUpstreamHostID(ctx context.Context, serviceID, hostID int64) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE services SET upstream_host_id=$2 WHERE id=$1`, serviceID, hostID)
	return err
}

// Checkpoints 读写 provision_data JSONB 中的检查点键值。
func (p *ProvisionRepo) GetCheckpoint(ctx context.Context, serviceID int64, key string) (string, bool, error) {
	var raw []byte
	if err := p.db.QueryRowContext(ctx,
		`SELECT provision_data FROM services WHERE id=$1`, serviceID).Scan(&raw); err != nil {
		return "", false, err
	}
	m := decodeJSONStrings(raw)
	v, ok := m[key]
	return v, ok, nil
}

func (p *ProvisionRepo) SetCheckpoint(ctx context.Context, serviceID int64, key, val string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE services
		 SET provision_data = jsonb_set(coalesce(provision_data, '{}'::jsonb), ARRAY[$2]::text[], to_jsonb($3::text), true)
		 WHERE id=$1`,
		serviceID, key, val)
	return err
}
