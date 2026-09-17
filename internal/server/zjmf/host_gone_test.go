package zjmf

import (
	"context"
	"net/http"
	"testing"

	"lumeidc/internal/server"
)

// hostProbeEnv 起一个只回答 /host/header 的假上游。
func hostProbeEnv(t *testing.T, body string) server.Config {
	t.Helper()
	return newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		case "/host/header":
			w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	})
}

// 上游如实返回 domainstatus=Deleted：原样回传，由 mapUpstreamStatus 映射成本地 3。
func TestStatusUpstreamDeleted(t *testing.T) {
	cfg := hostProbeEnv(t, `{"status":200,"data":{"host_data":{"domainstatus":"Deleted","domain":"a.example.com"}}}`)
	st, err := Provider{}.Status(context.Background(), cfg, 1001)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if st.Status != "Deleted" {
		t.Fatalf("应原样返回上游状态，实得: %s", st.Status)
	}
}

// 上游只回业务错误「产品不存在」：必须判为已终止。
// 否则本地会长期挂着幽灵服务——机器在上游已销毁，本地仍显示可续费，用户续费必然失败。
func TestStatusHostGoneBusinessError(t *testing.T) {
	cfg := hostProbeEnv(t, `{"status":400,"msg":"产品不存在"}`)
	st, err := Provider{}.Status(context.Background(), cfg, 1001)
	if err != nil {
		t.Fatalf("实例不存在不应作为查询失败: %v", err)
	}
	if st.Status != "terminated" {
		t.Fatalf("应判为 terminated，实得: %s", st.Status)
	}
}

// 真实上游措辞（2026-09-17 实测 ccyidc：HTTP 200 + {"status":406,"msg":"未找到该产品"}）：
// 上游不给 domainstatus，只给业务错误码 + 中文 msg，必须靠文案命中。
func TestStatusHostGoneRealUpstreamWording(t *testing.T) {
	cfg := hostProbeEnv(t, `{"status":406,"msg":"未找到该产品"}`)
	st, err := Provider{}.Status(context.Background(), cfg, 1607)
	if err != nil {
		t.Fatalf("实例不存在不应作为查询失败: %v", err)
	}
	if st.Status != "terminated" {
		t.Fatalf("应判为 terminated，实得: %s", st.Status)
	}
}

// 业务错误但措辞与「不存在」无关（权限/参数/鉴权类）：
// 标记 ErrHostMissing 交同步侧累计次数兜底，不直接判死，避免上游一次异常就删服务。
func TestStatusBizErrorNotGoneMarkedMissing(t *testing.T) {
	cfg := hostProbeEnv(t, `{"status":400,"msg":"参数错误"}`)
	_, err := Provider{}.Status(context.Background(), cfg, 1001)
	if !server.IsHostMissing(err) {
		t.Fatalf("未识别的业务错误应标记 ErrHostMissing，实得: %v", err)
	}
}

// 传输故障（HTTP 500）属于我方/链路问题，绝不能标记成实例缺失——否则会误删服务。
func TestStatusTransportErrorNotMissing(t *testing.T) {
	cfg := newUpgEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zjmf_api_login":
			loginOK(w)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	_, err := Provider{}.Status(context.Background(), cfg, 1001)
	if err == nil {
		t.Fatal("HTTP 500 应返回错误")
	}
	if server.IsHostMissing(err) {
		t.Fatalf("传输故障不得判定为实例缺失: %v", err)
	}
}
