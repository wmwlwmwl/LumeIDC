package easypanel

// 开通幂等的密码一致性测试。
//
// 崩溃续跑有两条「站点已经建好了」的路径（检查点命中、add_vh 返回 500 重名），
// 两条都必须保证「返回给调用方的密码 == 站点真实密码」。req.Password 为空时
// 密码是每次调用新生成的随机值，续跑时返回本次新值而不改上游，就会把用户
// 登不进去的密码写进 services.password_crypt。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"lumeidc/internal/server"
)

type memoryCheckpoints map[string]string

func (m memoryCheckpoints) GetCheckpoint(key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memoryCheckpoints) SetCheckpoint(key, val string) error { m[key] = val; return nil }
func (m memoryCheckpoints) DeleteCheckpoint(key string) error   { delete(m, key); return nil }

// checkpointEnv 起一个假 EP，记录各 action 收到的查询参数。
func checkpointEnv(t *testing.T, results map[string]string) (server.Config, func(string) url.Values) {
	t.Helper()
	seen := map[string]url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		action := q.Get("a")
		seen[action] = q
		code, ok := results[action]
		if !ok {
			code = "200"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":` + code + `}`))
	}))
	t.Cleanup(srv.Close)
	return server.Config{APIURL: srv.URL, APIKey: "skey"}, func(action string) url.Values { return seen[action] }
}

// 检查点命中（上次建站成功但结果未落库）：必须 change_password 同步本次密码，
// 否则返回的密码从未在上游生效。
func TestProvisionCheckpointHitSyncsPassword(t *testing.T) {
	cfg, seen := checkpointEnv(t, nil)
	ck := memoryCheckpoints{ckAddVH: SiteName(42)}

	res, err := (Provider{}).Provision(context.Background(), cfg,
		server.ProvisionRequest{ServiceID: 42}, ck) // 不传密码 → 每次调用随机生成
	if err != nil {
		t.Fatalf("检查点续跑不应失败: %v", err)
	}
	if seen("add_vh") != nil {
		t.Fatal("检查点命中时不应重复 add_vh")
	}
	sync := seen("change_password")
	if sync == nil {
		t.Fatal("返回的是本次新生成的密码，必须 change_password 同步到站点")
	}
	if sync.Get("passwd") != res.Password || sync.Get("name") != SiteName(42) {
		t.Fatalf("同步的站点/密码与返回值不一致: name=%q passwd=%q 返回=%q",
			sync.Get("name"), sync.Get("passwd"), res.Password)
	}
	if !server.ValidHostPassword(res.Password) {
		t.Fatalf("返回密码不合规: %q", res.Password)
	}
	if res.UpstreamHostID != 42 {
		t.Fatalf("上游主机标识应为服务 ID，实得 %d", res.UpstreamHostID)
	}
}

// 检查点还在但上游站点已被清掉：必须回落 add_vh 重建，不能假装开通成功。
func TestProvisionCheckpointStaleFallsBackToAddVH(t *testing.T) {
	cfg, seen := checkpointEnv(t, map[string]string{"getVh": "500"})
	ck := memoryCheckpoints{ckAddVH: SiteName(7)}

	res, err := (Provider{}).Provision(context.Background(), cfg,
		server.ProvisionRequest{ServiceID: 7}, ck)
	if err != nil {
		t.Fatalf("检查点失效应回落重建: %v", err)
	}
	add := seen("add_vh")
	if add == nil {
		t.Fatal("站点不存在时必须重新 add_vh")
	}
	if add.Get("name") != SiteName(7) || add.Get("passwd") != res.Password {
		t.Fatalf("重建参数与返回值不一致: name=%q passwd=%q 返回=%q",
			add.Get("name"), add.Get("passwd"), res.Password)
	}
	if v, ok, _ := ck.GetCheckpoint(ckAddVH); !ok || v != SiteName(7) {
		t.Fatalf("重建后检查点应为站点名，实得 %q/%v", v, ok)
	}
}

// 重名分支（add_vh 返回 500 且站点确实存在）：沿用原有行为，同步密码后返回成功。
func TestProvisionDuplicateNameSyncsPassword(t *testing.T) {
	cfg, seen := checkpointEnv(t, map[string]string{"add_vh": "500"})

	res, err := (Provider{}).Provision(context.Background(), cfg,
		server.ProvisionRequest{ServiceID: 9}, nil)
	if err != nil {
		t.Fatalf("重名幂等路径不应失败: %v", err)
	}
	sync := seen("change_password")
	if sync == nil || sync.Get("passwd") != res.Password {
		t.Fatalf("重名后必须同步密码: %v 返回=%q", sync, res.Password)
	}
}

// 密码同步失败必须报错：否则调用方会把一个未生效的密码存进数据库。
func TestProvisionPasswordSyncFailureIsReported(t *testing.T) {
	cfg, _ := checkpointEnv(t, map[string]string{"change_password": "500"})
	ck := memoryCheckpoints{ckAddVH: SiteName(11)}

	if _, err := (Provider{}).Provision(context.Background(), cfg,
		server.ProvisionRequest{ServiceID: 11}, ck); err == nil {
		t.Fatal("密码同步失败时必须报错")
	}
}

// 显式传入合规密码：两次调用用的是同一个密码，续跑无需同步也应返回一致结果。
func TestProvisionCheckpointHitKeepsProvidedPassword(t *testing.T) {
	cfg, seen := checkpointEnv(t, nil)
	ck := memoryCheckpoints{ckAddVH: SiteName(13)}
	const pw = "Abc12345678"

	res, err := (Provider{}).Provision(context.Background(), cfg,
		server.ProvisionRequest{ServiceID: 13, Password: pw}, ck)
	if err != nil {
		t.Fatalf("检查点续跑不应失败: %v", err)
	}
	if res.Password != pw {
		t.Fatalf("应返回调用方指定的密码，实得 %q", res.Password)
	}
	if sync := seen("change_password"); sync == nil || sync.Get("passwd") != pw {
		t.Fatalf("同步到站点的密码应与指定值一致: %v", sync)
	}
}
