package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// snapshotFuncs 允许下发的快照/备份操作（对齐上游 module func 名）。
var snapshotFuncs = map[string]bool{
	"createSnap": true, "delSnap": true, "restoreSnap": true,
	"createBackup": true, "delBackup": true, "restoreBackup": true,
}

// SnapshotInfo 实现 server.SnapshotProvider：v10 模式拉结构化 JSON（快照+备份+磁盘）。
func (p Provider) SnapshotInfo(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.SnapshotInfo, error) {
	token, err := ensureToken(ctx, cfg)
	if err != nil {
		return server.SnapshotInfo{}, err
	}
	q := url.Values{
		"id":  {strconv.FormatInt(upstreamHostID, 10)},
		"key": {"snapshot"},
		"jwt": {token},
		"v10": {"true"},
	}
	u := strings.TrimRight(cfg.APIURL, "/") + "/provision/custom/content?" + q.Encode()
	body, err := doRaw(ctx, cfg, "GET", u, "", "", "JWT "+token)
	if err != nil {
		return server.SnapshotInfo{}, err
	}
	var out struct {
		Data struct {
			List []struct {
				ID         any    `json:"id"`
				Name       string `json:"name"`
				Type       string `json:"type"`
				Status     any    `json:"status"`
				Remarks    string `json:"remarks"`
				CreateTime string `json:"create_time"`
			} `json:"list"`
			Disk []struct {
				ID   any    `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
				Size any    `json:"size"`
				Dev  string `json:"dev"`
			} `json:"disk"`
			SnapNum   any `json:"snap_num"`
			BackupNum any `json:"backup_num"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return server.SnapshotInfo{}, fmt.Errorf("快照数据解析失败: %w", err)
	}
	info := server.SnapshotInfo{
		SnapNum:   anyToInt(out.Data.SnapNum),
		BackupNum: anyToInt(out.Data.BackupNum),
	}
	for _, it := range out.Data.List {
		info.List = append(info.List, server.SnapshotItem{
			ID:         anyToInt64(it.ID),
			Name:       it.Name,
			Type:       it.Type,
			Status:     anyToInt(it.Status),
			Remarks:    it.Remarks,
			CreateTime: it.CreateTime,
		})
	}
	for _, d := range out.Data.Disk {
		info.Disk = append(info.Disk, server.DiskItem{
			ID:   anyToInt64(d.ID),
			Name: d.Name,
			Type: d.Type,
			Size: anyToInt64(d.Size),
			Dev:  d.Dev,
		})
	}
	return info, nil
}

// SnapshotAction 实现 server.SnapshotProvider：复用 ModuleAction（POST /provision/custom/{id}），
// 仅收敛允许的 func 白名单。
func (p Provider) SnapshotAction(ctx context.Context, cfg server.Config, upstreamHostID int64, fn string, form url.Values) (string, error) {
	if !snapshotFuncs[fn] {
		return "", fmt.Errorf("不支持的快照操作: %s", fn)
	}
	f := url.Values{}
	for k, vs := range form {
		for _, v := range vs {
			f.Add(k, v)
		}
	}
	f.Set("func", fn)
	return p.ModuleAction(ctx, cfg, upstreamHostID, f)
}

func anyToInt(v any) int { return int(anyToInt64(v)) }

func anyToInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}
