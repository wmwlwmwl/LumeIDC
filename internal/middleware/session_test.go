package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRevokeScopesDoNotCrossAdminBoundary(t *testing.T) {
	store := &Store{sess: map[string]*Session{
		"user":  {UserID: 1, IsAdmin: false},
		"admin": {UserID: 1, IsAdmin: true},
		"other": {UserID: 2, IsAdmin: false},
	}}
	store.RevokeUser(1)
	if _, ok := store.sess["user"]; ok {
		t.Fatal("普通用户会话未被撤销")
	}
	if _, ok := store.sess["admin"]; !ok {
		t.Fatal("撤销普通用户会话时误删管理员会话")
	}
	store.RevokeAdmin(1)
	if _, ok := store.sess["admin"]; ok {
		t.Fatal("管理员会话未被撤销")
	}
	if _, ok := store.sess["other"]; !ok {
		t.Fatal("撤销管理员会话时误删其他用户会话")
	}
}

// 双通道：同一浏览器可同时持有普通用户与管理员会话，互不顶替；
// 请求按路径归属通道，登出只清本通道。
func TestUserAndAdminChannelsCoexist(t *testing.T) {
	store := &Store{sess: map[string]*Session{}, key: []byte("0123456789abcdef0123456789abcdef"), ttl: time.Hour}

	recUser := httptest.NewRecorder()
	us := store.Start(httptest.NewRequest("POST", "/login", nil), recUser)
	us.UserID = 7
	recAdmin := httptest.NewRecorder()
	as := store.StartAdmin(httptest.NewRequest("POST", "/admin/login", nil), recAdmin)
	as.UserID = 1

	req := func(path string) *http.Request {
		r := httptest.NewRequest("GET", path, nil)
		for _, c := range append(recUser.Result().Cookies(), recAdmin.Result().Cookies()...) {
			r.AddCookie(c)
		}
		return r
	}

	// 同一请求同时携带两份 cookie，两个通道都能取回各自身份
	if got := store.GetUser(req("/services")); got == nil || got.UserID != 7 || got.IsAdmin {
		t.Fatalf("用户通道会话错误: %+v", got)
	}
	if got := store.GetAdmin(req("/admin")); got == nil || got.UserID != 1 || !got.IsAdmin {
		t.Fatalf("管理员通道会话错误: %+v", got)
	}

	// 中间件按路径选通道：前台路径取用户会话，后台路径取管理员会话
	picked := map[string]int64{}
	h := store.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := FromSession(r.Context()); s != nil {
			picked[r.URL.Path] = s.UserID
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), req("/services"))
	h.ServeHTTP(httptest.NewRecorder(), req("/admin/users"))
	if picked["/services"] != 7 || picked["/admin/users"] != 1 {
		t.Fatalf("通道选择错误: %v", picked)
	}

	// 后台登出只清管理员通道
	store.DestroyAdmin(req("/admin"), httptest.NewRecorder())
	if store.GetAdmin(req("/admin")) != nil {
		t.Fatal("管理员通道未清除")
	}
	if store.GetUser(req("/services")) == nil {
		t.Fatal("管理员登出不应影响用户通道")
	}
}
