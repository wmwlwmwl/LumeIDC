package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/config"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// 营销活动 API 级全流程校验：真实 PostgreSQL（隔离测试库）+ 真实 handler + 真实 mux 路由，
// 覆盖五种活动类型的后台管理、前台展示、领券、配额与统计全链路。
// 需要 TEST_DATABASE_DSN 指向隔离测试库，绝不连接正式数据库。

const promoE2EPrefix = "promo-e2e-"

var (
	promoE2EAdminTimeRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$`)
	promoE2EAdminListTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)
	promoE2EFrontTimeRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)
	promoE2EUserTimeRe      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)
	promoE2EExpiresRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	promoE2ESupportedSet    = []string{"discount", "flash_sale", "full_reduction", "new_user", "coupon_giveaway"}
)

type promoE2EEnv struct {
	t        *testing.T
	d        *sql.DB
	mux      *http.ServeMux
	promos   *repo.Promotions
	promoSvc *service.PromotionService
	products *repo.Products

	userID     int64
	otherUID   int64
	productID  int64
	noPricePID int64
	pricesetID int64
	tplCoupon  int64

	adminSess *middleware.Session
	userSess  *middleware.Session
	otherSess *middleware.Session
}

// cleanPromoE2EData 只清理本用例前缀的数据，避免影响其它测试。
func cleanPromoE2EData(ctx context.Context, t *testing.T, d *sql.DB) {
	t.Helper()
	scoped := `WHERE promotion_id IN (SELECT id FROM promotions WHERE name LIKE '` + promoE2EPrefix + `%')`
	stmts := []string{
		`DELETE FROM promotion_coupon_claims ` + scoped,
		`DELETE FROM promotion_stats ` + scoped,
		`DELETE FROM promotion_quota WHERE promotion_product_id IN (
			SELECT id FROM promotion_products ` + scoped + `)`,
		`DELETE FROM promotion_products ` + scoped,
		`DELETE FROM promotions WHERE name LIKE '` + promoE2EPrefix + `%'`,
		`DELETE FROM coupons WHERE code LIKE 'PROMO-E2E-%' OR code LIKE 'PROMO\_%'`,
		`DELETE FROM users WHERE email LIKE 'promo-e2e-%@example.com'`,
		`DELETE FROM products WHERE name LIKE 'promo-e2e-%'`,
	}
	for _, s := range stmts {
		if _, err := d.ExecContext(ctx, s); err != nil {
			t.Fatalf("清理营销活动测试数据失败: %s: %v", s, err)
		}
	}
}

func newPromoE2EEnv(t *testing.T) *promoE2EEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过营销活动 API 级全流程校验")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 注意顺序：t.Cleanup 后进先出，先注册关闭，数据清理才能跑在连接关闭之前。
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	cleanPromoE2EData(ctx, t, d)

	store, err := middleware.NewStore(&config.Config{
		SecretKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32)),
	})
	if err != nil {
		t.Fatalf("建会话存储失败: %v", err)
	}
	coupons := repo.NewCoupons(d)
	promos := repo.NewPromotions(d)
	products := repo.NewProducts(d)
	promoSvc := service.NewPromotionService(d, promos, coupons)
	deps := &Deps{PageStore: store, AdminStore: store}

	mux := http.NewServeMux()
	(&Admin{DB: d, Coupons: coupons, Promotions: promos, Deps: deps}).Register(mux)
	(&Pages{DB: d, Products: products, Promotion: promoSvc, Deps: deps}).Register(mux)

	users := repo.NewUsers(d)
	uid, err := users.CreateAccount(ctx, "promo-e2e-user@example.com", "", "correct-password-1", "活动用户", true)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	otherUID, err := users.CreateAccount(ctx, "promo-e2e-other@example.com", "", "correct-password-1", "其他用户", true)
	if err != nil {
		t.Fatalf("建其他用户失败: %v", err)
	}
	productID, err := products.Create(ctx, sql.NullInt64{}, promoE2EPrefix+"云服务器", "", 100)
	if err != nil {
		t.Fatalf("建商品失败: %v", err)
	}
	// 无价格行商品：前台详情页必须跳过（否则前端拿到无价卡片）。
	noPricePID, err := products.Create(ctx, sql.NullInt64{}, promoE2EPrefix+"无价商品", "", 100)
	if err != nil {
		t.Fatalf("建无价商品失败: %v", err)
	}
	pricesetID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Fatalf("取默认价格组失败: %v", err)
	}
	if err := products.UpsertPrice(ctx, productID, pricesetID, "100", "300", "1200"); err != nil {
		t.Fatalf("写商品价格失败: %v", err)
	}
	if err := coupons.Create(ctx, "PROMO-E2E-TPL", "fixed", 20, 0, 1, nil); err != nil {
		t.Fatalf("建优惠券模板失败: %v", err)
	}
	var tplCoupon int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM coupons WHERE code=$1`, "PROMO-E2E-TPL").Scan(&tplCoupon); err != nil {
		t.Fatalf("取优惠券模板 ID 失败: %v", err)
	}

	t.Cleanup(func() { cleanPromoE2EData(ctx, t, d) })

	return &promoE2EEnv{
		t: t, d: d, mux: mux, promos: promos, promoSvc: promoSvc, products: products,
		userID: uid, otherUID: otherUID, productID: productID, noPricePID: noPricePID,
		pricesetID: pricesetID, tplCoupon: tplCoupon,
		adminSess: &middleware.Session{UserID: 1, IsAdmin: true},
		userSess:  &middleware.Session{UserID: uid},
		otherSess: &middleware.Session{UserID: otherUID},
	}
}

func (e *promoE2EEnv) req(method, target string, sess *middleware.Session, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Accept", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if sess != nil {
		r = r.WithContext(middleware.WithSession(r.Context(), sess))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *promoE2EEnv) reqJSON(method, target string, sess *middleware.Session, payload any, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatal(err)
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if sess != nil {
		r = r.WithContext(middleware.WithSession(r.Context(), sess))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func promoE2EDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, w.Body.String())
	}
	return out
}

func promoE2EObj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少对象字段 %q: %s", key, mustJSON(m))
	}
	return v
}

func promoE2EArr(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key].([]any)
	if !ok {
		t.Fatalf("响应缺少数组字段 %q: %s", key, mustJSON(m))
	}
	return v
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func promoE2ENum(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("字段 %q 不是数字: %s", key, mustJSON(m))
	}
	return v
}

func promoE2EStr(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("字段 %q 不是字符串: %s", key, mustJSON(m))
	}
	return v
}

func promoE2EBool(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key].(bool)
	if !ok {
		t.Fatalf("字段 %q 不是布尔: %s", key, mustJSON(m))
	}
	return v
}

// promoE2EWindow 生成后台保存用的时间窗（一律按 UTC 拼串，避免本地时区把「进行中」挪到未来）。
func promoE2EWindow(startOffset, endOffset time.Duration) (startsAt, endsAt string) {
	now := time.Now().UTC()
	return now.Add(startOffset).Format("2006-01-02T15:04"), now.Add(endOffset).Format("2006-01-02T15:04")
}

// promoE2ESaveForm 组装后台保存活动的表单（products 为 JSON 字符串，表单值不支持嵌套结构）。
func promoE2ESaveForm(name, typ, rules string, startOffset, endOffset time.Duration, enabled bool, limitPerUser int, productIDs ...int64) url.Values {
	startsAt, endsAt := promoE2EWindow(startOffset, endOffset)
	items := make([]map[string]any, 0, len(productIDs))
	for _, pid := range productIDs {
		item := map[string]any{"product_id": pid}
		if rules != "" {
			var r map[string]any
			if err := json.Unmarshal([]byte(rules), &r); err != nil {
				panic(err)
			}
			item["rules"] = r
		}
		items = append(items, item)
	}
	raw, _ := json.Marshal(items)
	en := "0"
	if enabled {
		en = "1"
	}
	return url.Values{
		"name":           {name},
		"description":    {name + " 简介"},
		"type":           {typ},
		"notice":         {name + " 公告"},
		"rules_text":     {name + " 规则"},
		"starts_at":      {startsAt},
		"ends_at":        {endsAt},
		"enabled":        {en},
		"limit_per_user": {itoa(int64(limitPerUser))},
		"products":       {string(raw)},
	}
}

func (e *promoE2EEnv) findByName(t *testing.T, list []any, name string) map[string]any {
	t.Helper()
	for _, it := range list {
		m, ok := it.(map[string]any)
		if ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("列表里找不到活动 %q: %s", name, mustJSON(list))
	return nil
}

func (e *promoE2EEnv) findByNameOK(list []any, name string) bool {
	for _, it := range list {
		m, ok := it.(map[string]any)
		if ok && m["name"] == name {
			return true
		}
	}
	return false
}

// TestPromotionAPIEndToEnd 营销活动 API 级全流程校验。
func TestPromotionAPIEndToEnd(t *testing.T) {
	env := newPromoE2EEnv(t)
	ctx := context.Background()

	type saved struct {
		name string
		id   int64
	}
	promoNames := map[string]string{
		"discount":        promoE2EPrefix + "折扣活动",
		"flash_sale":      promoE2EPrefix + "抢购活动",
		"full_reduction":  promoE2EPrefix + "满减活动",
		"new_user":        promoE2EPrefix + "新客活动",
		"coupon_giveaway": promoE2EPrefix + "赠券活动",
	}
	ids := map[string]int64{}

	// ---------- 后台：保存五种活动类型 ----------
	t.Run("后台保存五种活动类型", func(t *testing.T) {
		cases := []struct {
			typ   string
			rules string
		}{
			{"discount", `{"price":60}`},
			{"flash_sale", `{"price":30,"stock":2}`},
			{"full_reduction", `{"threshold":200,"reduce":50}`},
			{"new_user", `{"price":45}`},
			{"coupon_giveaway", `{"coupon_id":0}`},
		}
		for _, c := range cases {
			rules := c.rules
			if c.typ == "coupon_giveaway" {
				rules = `{"coupon_id":` + itoa(env.tplCoupon) + `}`
			}
			form := promoE2ESaveForm(promoNames[c.typ], c.typ, rules, -time.Hour, 72*time.Hour, true, 2, env.productID)
			w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess, form, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("%s 保存应返回 200，实得 %d，响应: %s", c.typ, w.Code, w.Body.String())
			}
			resp := promoE2EDecode(t, w)
			if promoE2ENum(t, resp, "ok") != 1 {
				t.Fatalf("%s 保存响应 ok 应为 1: %s", c.typ, mustJSON(resp))
			}
			id := int64(promoE2ENum(t, resp, "id"))
			if id <= 0 {
				t.Fatalf("%s 保存应返回正数 id: %s", c.typ, mustJSON(resp))
			}
			if got := promoE2EStr(t, resp, "msg"); got != "保存成功" {
				t.Fatalf("%s 保存 msg 应为「保存成功」，实得 %q", c.typ, got)
			}
			ids[c.typ] = id
		}
	})

	// ---------- 后台：保存校验 ----------
	t.Run("后台保存拒绝空名称", func(t *testing.T) {
		form := promoE2ESaveForm("   ", "discount", `{"price":60}`, -time.Hour, 72*time.Hour, true, 0, env.productID)
		form.Set("name", "   ")
		w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess, form, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("空名称应返回 400，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		resp := promoE2EDecode(t, w)
		if got := promoE2EStr(t, resp, "msg"); got != "活动名称不能为空" {
			t.Fatalf("空名称 msg 应为「活动名称不能为空」，实得 %q", got)
		}
	})

	t.Run("后台保存拒绝未实现的活动类型", func(t *testing.T) {
		// DB CHECK 允许 group_buy / bogo，但业务未实现：入口必须拒绝，否则会配置出用户可见却无法生效的活动。
		for _, typ := range []string{"group_buy", "bogo", ""} {
			form := promoE2ESaveForm(promoE2EPrefix+"未实现活动", typ, `{"price":1}`, -time.Hour, 72*time.Hour, true, 0, env.productID)
			w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess, form, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("未实现类型 %q 应被拒绝（400），实得 %d，响应: %s", typ, w.Code, w.Body.String())
			}
			if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); !strings.Contains(got, "暂不支持该活动类型") {
				t.Fatalf("未实现类型 %q 的 msg 应提示暂不支持，实得 %q", typ, got)
			}
		}
		// 更新路径同样必须校验（不能借旧记录绕过）。
		w := env.req(http.MethodPost, "/admin/promotions/"+itoa(ids["discount"])+"/save", env.adminSess,
			promoE2ESaveForm(promoNames["discount"], "group_buy", `{"price":1}`, -time.Hour, 72*time.Hour, true, 0, env.productID), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("更新为未实现类型应被拒绝（400），实得 %d，响应: %s", w.Code, w.Body.String())
		}
	})

	// ---------- 后台：未登录拦截 ----------
	t.Run("后台接口未登录返回401", func(t *testing.T) {
		w := env.req(http.MethodGet, "/admin/promotions", nil, nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("未登录访问后台活动列表应返回 401，实得 %d，响应: %s", w.Code, w.Body.String())
		}
	})

	// ---------- 后台：列表与详情 ----------
	t.Run("后台列表与详情口径", func(t *testing.T) {
		w := env.req(http.MethodGet, "/admin/promotions", env.adminSess, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("后台列表应返回 200，实得 %d", w.Code)
		}
		list := promoE2EArr(t, promoE2EDecode(t, w), "list")
		for _, typ := range promoE2ESupportedSet {
			item := env.findByName(t, list, promoNames[typ])
			if got := promoE2EStr(t, item, "type"); got != typ {
				t.Fatalf("活动 %s 的 type 应为 %s，实得 %s", promoNames[typ], typ, got)
			}
			if got := promoE2EStr(t, item, "starts_at"); !promoE2EAdminListTimeRe.MatchString(got) {
				t.Fatalf("后台列表 starts_at 应为 2006-01-02 15:04，实得 %q", got)
			}
			if got := promoE2EStr(t, item, "ends_at"); !promoE2EAdminListTimeRe.MatchString(got) {
				t.Fatalf("后台列表 ends_at 应为 2006-01-02 15:04，实得 %q", got)
			}
			if got := promoE2EStr(t, item, "status"); got != "ongoing" {
				t.Fatalf("活动 %s 状态应为 ongoing，实得 %s", promoNames[typ], got)
			}
			if promoE2ENum(t, item, "limit_per_user") != 2 {
				t.Fatalf("活动 %s limit_per_user 应为 2", promoNames[typ])
			}
			if !promoE2EBool(t, item, "enabled") {
				t.Fatalf("活动 %s 应为启用", promoNames[typ])
			}
		}

		// 详情：含商品绑定与规则；赠券活动必须带回模板券 ID。
		w = env.req(http.MethodGet, "/admin/promotions/"+itoa(ids["coupon_giveaway"]), env.adminSess, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("后台详情应返回 200，实得 %d", w.Code)
		}
		detail := promoE2EDecode(t, w)
		promo := promoE2EObj(t, detail, "promotion")
		if got := promoE2EStr(t, promo, "starts_at"); !promoE2EAdminTimeRe.MatchString(got) {
			t.Fatalf("后台详情 starts_at 格式应为 2006-01-02T15:04，实得 %q", got)
		}
		prods := promoE2EArr(t, detail, "products")
		if len(prods) != 1 {
			t.Fatalf("赠券活动应绑定 1 个商品，实得 %d", len(prods))
		}
		pp := prods[0].(map[string]any)
		if promoE2ENum(t, pp, "product_id") != float64(env.productID) {
			t.Fatalf("绑定商品 ID 不符: %s", mustJSON(pp))
		}
		rules := promoE2EObj(t, pp, "rules")
		if promoE2ENum(t, rules, "coupon_id") != float64(env.tplCoupon) {
			t.Fatalf("赠券规则 coupon_id 应为 %d: %s", env.tplCoupon, mustJSON(rules))
		}

		// 详情 404
		w = env.req(http.MethodGet, "/admin/promotions/99999999", env.adminSess, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("不存在活动应返回 404，实得 %d", w.Code)
		}
	})

	// ---------- 后台：更新（原子重写商品绑定） ----------
	t.Run("后台更新活动与商品绑定", func(t *testing.T) {
		form := promoE2ESaveForm(promoNames["discount"], "discount", `{"price":55}`, -time.Hour, 96*time.Hour, true, 5, env.productID, env.noPricePID)
		w := env.req(http.MethodPost, "/admin/promotions/"+itoa(ids["discount"])+"/save", env.adminSess, form, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("更新应返回 200，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		resp := promoE2EDecode(t, w)
		if int64(promoE2ENum(t, resp, "id")) != ids["discount"] {
			t.Fatalf("更新应返回原 id: %s", mustJSON(resp))
		}
		w = env.req(http.MethodGet, "/admin/promotions/"+itoa(ids["discount"]), env.adminSess, nil, nil)
		detail := promoE2EDecode(t, w)
		promo := promoE2EObj(t, detail, "promotion")
		if promoE2ENum(t, promo, "limit_per_user") != 5 {
			t.Fatalf("更新后 limit_per_user 应为 5: %s", mustJSON(promo))
		}
		prods := promoE2EArr(t, detail, "products")
		if len(prods) != 2 {
			t.Fatalf("更新后应绑定 2 个商品，实得 %d", len(prods))
		}
		// 恢复单价，避免影响后续卡片金额断言。
		form = promoE2ESaveForm(promoNames["discount"], "discount", `{"price":60}`, -time.Hour, 72*time.Hour, true, 2, env.productID)
		w = env.req(http.MethodPost, "/admin/promotions/"+itoa(ids["discount"])+"/save", env.adminSess, form, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("恢复更新应返回 200，实得 %d", w.Code)
		}
	})

	// ---------- 前台：列表只放行「已启用且未结束」 ----------
	t.Run("前台列表过滤禁用与已结束活动", func(t *testing.T) {
		// 追加一个禁用活动和一个已结束活动。
		disabled := promoE2EPrefix + "已禁用活动"
		w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess,
			promoE2ESaveForm(disabled, "discount", `{"price":10}`, -time.Hour, 72*time.Hour, false, 0, env.productID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("保存禁用活动失败: %s", w.Body.String())
		}
		ended := promoE2EPrefix + "已结束活动"
		w = env.req(http.MethodPost, "/admin/promotions/save", env.adminSess,
			promoE2ESaveForm(ended, "discount", `{"price":10}`, -72*time.Hour, -time.Hour, true, 0, env.productID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("保存已结束活动失败: %s", w.Body.String())
		}

		w = env.req(http.MethodGet, "/promotions", nil, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("前台列表应返回 200，实得 %d", w.Code)
		}
		list := promoE2EArr(t, promoE2EDecode(t, w), "list")
		supported := map[string]bool{}
		for _, typ := range promoE2ESupportedSet {
			supported[typ] = true
		}
		for _, it := range list {
			m := it.(map[string]any)
			name, _ := m["name"].(string)
			if name == disabled || name == ended {
				t.Fatalf("前台列表不应出现 %q（禁用/已结束必须过滤）", name)
			}
			typ, _ := m["type"].(string)
			if !supported[typ] {
				t.Fatalf("前台列表出现了未实现类型 %q 的活动: %s", typ, mustJSON(m))
			}
			if status, _ := m["status"].(string); status == "ended" {
				t.Fatalf("前台列表不应出现已结束活动: %s", mustJSON(m))
			}
			if got := promoE2EStr(t, m, "starts_at"); !promoE2EFrontTimeRe.MatchString(got) {
				t.Fatalf("前台 starts_at 应为 RFC3339，实得 %q", got)
			}
		}
		for _, typ := range promoE2ESupportedSet {
			env.findByName(t, list, promoNames[typ])
		}
	})

	// ---------- 前台：历史脏数据（未实现类型）不得透出 ----------
	t.Run("前台不透出未实现类型的脏数据", func(t *testing.T) {
		// 绕过 handler 直接落库，模拟升级前遗留的 group_buy 活动。
		dirty := promoE2EPrefix + "脏数据活动"
		var dirtyID int64
		err := env.d.QueryRowContext(ctx,
			`INSERT INTO promotions (name, description, type, banner, notice, rules_text, starts_at, ends_at, enabled, limit_per_user)
			 VALUES ($1,$2,'group_buy','','','',$3,$4,true,0) RETURNING id`,
			dirty, promoE2EPrefix+"脏数据", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(72*time.Hour),
		).Scan(&dirtyID)
		if err != nil {
			t.Fatalf("插入脏数据失败: %v", err)
		}
		t.Cleanup(func() {
			_, _ = env.d.ExecContext(ctx, `DELETE FROM promotions WHERE id=$1`, dirtyID)
		})

		// 后台管理面必须仍可见（否则无法停用/清理）。
		w := env.req(http.MethodGet, "/admin/promotions", env.adminSess, nil, nil)
		env.findByName(t, promoE2EArr(t, promoE2EDecode(t, w), "list"), dirty)

		// 前台列表与详情都不得透出。
		w = env.req(http.MethodGet, "/promotions", nil, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("前台列表应返回 200，实得 %d", w.Code)
		}
		if env.findByNameOK(promoE2EArr(t, promoE2EDecode(t, w), "list"), dirty) {
			t.Fatalf("前台列表不得透出未实现类型的脏数据")
		}
		w = env.req(http.MethodGet, "/promotion/"+itoa(dirtyID), nil, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("未实现类型的详情应返回 404，实得 %d，响应: %s", w.Code, w.Body.String())
		}
	})

	// ---------- 前台：详情卡片按类型计算 ----------
	t.Run("前台详情按活动类型计算卡片", func(t *testing.T) {
		// 无价商品也必须出现在绑定里但被跳过：给折扣活动临时绑上无价商品。
		w := env.req(http.MethodPost, "/admin/promotions/"+itoa(ids["discount"])+"/save", env.adminSess,
			promoE2ESaveForm(promoNames["discount"], "discount", `{"price":60}`, -time.Hour, 72*time.Hour, true, 2, env.productID, env.noPricePID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("临时绑定无价商品失败: %s", w.Body.String())
		}
		defer func() {
			w := env.req(http.MethodPost, "/admin/promotions/"+itoa(ids["discount"])+"/save", env.adminSess,
				promoE2ESaveForm(promoNames["discount"], "discount", `{"price":60}`, -time.Hour, 72*time.Hour, true, 2, env.productID), nil)
			if w.Code != http.StatusOK {
				t.Fatalf("恢复绑定失败: %s", w.Body.String())
			}
		}()

		type want struct {
			typ       string
			check     func(t *testing.T, card map[string]any)
			wantCards int
		}
		cases := []want{
			{"discount", func(t *testing.T, card map[string]any) {
				if promoE2ENum(t, card, "promo_price") != 60 {
					t.Fatalf("折扣活动价应为 60: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "discount") != 40 {
					t.Fatalf("折扣优惠额应为 40: %s", mustJSON(card))
				}
				if promoE2EStr(t, card, "original_price") != "100.00" {
					t.Fatalf("原价应为 100.00: %s", mustJSON(card))
				}
			}, 1},
			{"flash_sale", func(t *testing.T, card map[string]any) {
				if promoE2ENum(t, card, "promo_price") != 30 {
					t.Fatalf("抢购活动价应为 30: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "quota_total") != 2 {
					t.Fatalf("抢购总名额应为 2: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "quota_sold") != 0 {
					t.Fatalf("抢购已售应为 0: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "quota_left") != 2 {
					t.Fatalf("抢购剩余应为 2: %s", mustJSON(card))
				}
				if promoE2EBool(t, card, "sold_out") {
					t.Fatalf("未售罄时 sold_out 应为 false: %s", mustJSON(card))
				}
			}, 1},
			{"full_reduction", func(t *testing.T, card map[string]any) {
				if promoE2ENum(t, card, "threshold") != 200 {
					t.Fatalf("满减门槛应为 200: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "reduce") != 50 {
					t.Fatalf("满减额度应为 50: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "promo_price") != 100 {
					t.Fatalf("月价 100 未达门槛，活动价应等于原价 100: %s", mustJSON(card))
				}
			}, 1},
			{"new_user", func(t *testing.T, card map[string]any) {
				if promoE2ENum(t, card, "promo_price") != 45 {
					t.Fatalf("新客活动价应为 45: %s", mustJSON(card))
				}
				if promoE2ENum(t, card, "discount") != 55 {
					t.Fatalf("新客优惠额应为 55: %s", mustJSON(card))
				}
			}, 1},
			{"coupon_giveaway", func(t *testing.T, card map[string]any) {
				if promoE2ENum(t, card, "coupon_id") != float64(env.tplCoupon) {
					t.Fatalf("赠券 coupon_id 应为 %d: %s", env.tplCoupon, mustJSON(card))
				}
				if promoE2ENum(t, card, "promo_price") != 100 {
					t.Fatalf("赠券活动不降价，活动价应等于原价 100: %s", mustJSON(card))
				}
			}, 1},
		}
		for _, c := range cases {
			w := env.req(http.MethodGet, "/promotion/"+itoa(ids[c.typ]), nil, nil, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("%s 详情应返回 200，实得 %d，响应: %s", c.typ, w.Code, w.Body.String())
			}
			resp := promoE2EDecode(t, w)
			promo := promoE2EObj(t, resp, "promotion")
			if promoE2EStr(t, promo, "type") != c.typ {
				t.Fatalf("%s 详情 type 不符: %s", c.typ, mustJSON(promo))
			}
			if got := promoE2EStr(t, promo, "starts_at"); !promoE2EFrontTimeRe.MatchString(got) {
				t.Fatalf("前台 starts_at 应为 RFC3339，实得 %q", got)
			}
			cards := promoE2EArr(t, resp, "products")
			if len(cards) != c.wantCards {
				t.Fatalf("%s 应有 %d 张商品卡片（无价商品必须跳过），实得 %d: %s", c.typ, c.wantCards, len(cards), mustJSON(cards))
			}
			card := cards[0].(map[string]any)
			if promoE2ENum(t, card, "product_id") != float64(env.productID) {
				t.Fatalf("%s 卡片商品 ID 不符: %s", c.typ, mustJSON(card))
			}
			if promoE2EStr(t, card, "name") != promoE2EPrefix+"云服务器" {
				t.Fatalf("%s 卡片商品名不符: %s", c.typ, mustJSON(card))
			}
			c.check(t, card)
		}
	})

	// ---------- 前台：详情 404 ----------
	t.Run("前台详情不存在活动返回404", func(t *testing.T) {
		w := env.req(http.MethodGet, "/promotion/99999999", nil, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("不存在活动应返回 404，实得 %d", w.Code)
		}
	})

	// ---------- 领券：登录门槛 ----------
	t.Run("领券未登录被拦截", func(t *testing.T) {
		w := env.reqJSON(http.MethodPost, "/promotion/"+itoa(ids["coupon_giveaway"])+"/claim", nil,
			map[string]any{"promotion_product_id": 0}, nil)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("未登录领券应跳登录页（303），实得 %d，响应: %s", w.Code, w.Body.String())
		}
		w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(ids["coupon_giveaway"])+"/claim", nil,
			map[string]any{"promotion_product_id": 1}, map[string]string{"Sec-Fetch-Mode": "cors"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("AJAX 未登录领券应返回 401，实得 %d，响应: %s", w.Code, w.Body.String())
		}
	})

	// ---------- 领券：参数与类型门槛 ----------
	t.Run("领券参数与类型校验", func(t *testing.T) {
		giveaway := itoa(ids["coupon_giveaway"])
		w := env.reqJSON(http.MethodPost, "/promotion/"+giveaway+"/claim", env.userSess,
			map[string]any{"promotion_product_id": 0}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("promotion_product_id<=0 应返回 400，实得 %d", w.Code)
		}
		if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); got != "活动商品参数无效" {
			t.Fatalf("参数无效 msg 不符: %q", got)
		}
		for _, typ := range []string{"discount", "flash_sale", "full_reduction", "new_user"} {
			w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(ids[typ])+"/claim", env.userSess,
				map[string]any{"promotion_product_id": 1}, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s 活动领券应返回 400，实得 %d", typ, w.Code)
			}
			if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); got != "该活动无优惠券可领" {
				t.Fatalf("%s 领券 msg 应为「该活动无优惠券可领」，实得 %q", typ, got)
			}
		}
		// 不存在的活动商品绑定。
		w = env.reqJSON(http.MethodPost, "/promotion/"+giveaway+"/claim", env.userSess,
			map[string]any{"promotion_product_id": 99999999}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("不存在的活动商品应返回 400，实得 %d，响应: %s", w.Code, w.Body.String())
		}
	})

	// ---------- 领券：成功 + 重复领取 + 跨用户 ----------
	t.Run("领券成功重复领取与专属券归属", func(t *testing.T) {
		giveaway := ids["coupon_giveaway"]
		var ppID int64
		if err := env.d.QueryRowContext(ctx,
			`SELECT id FROM promotion_products WHERE promotion_id=$1`, giveaway).Scan(&ppID); err != nil {
			t.Fatalf("取活动商品 ID 失败: %v", err)
		}

		w := env.reqJSON(http.MethodPost, "/promotion/"+itoa(giveaway)+"/claim", env.userSess,
			map[string]any{"promotion_product_id": ppID}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("首次领券应返回 200，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		resp := promoE2EDecode(t, w)
		if promoE2ENum(t, resp, "ok") != 1 || promoE2EStr(t, resp, "msg") != "领取成功" {
			t.Fatalf("首次领券响应不符: %s", mustJSON(resp))
		}

		// 重复领取必须被拒。
		w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(giveaway)+"/claim", env.userSess,
			map[string]any{"promotion_product_id": ppID}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("重复领券应返回 400，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); got != "您已领取过该活动优惠券" {
			t.Fatalf("重复领券 msg 应为「您已领取过该活动优惠券」，实得 %q", got)
		}

		// 生成的券必须绑定到领取人本人（专属券不可跨用户）。
		var owner sql.NullInt64
		var code string
		if err := env.d.QueryRowContext(ctx,
			`SELECT code,user_id FROM coupons WHERE code=$1`, "PROMO_"+itoa(giveaway)+"_"+itoa(env.userID)).
			Scan(&code, &owner); err != nil {
			t.Fatalf("查询生成的专属券失败: %v", err)
		}
		if !owner.Valid || owner.Int64 != env.userID {
			t.Fatalf("专属券必须绑定领取用户 %d，实得 %v", env.userID, owner)
		}
		// 其他用户领到的是另一张券。
		w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(giveaway)+"/claim", env.otherSess,
			map[string]any{"promotion_product_id": ppID}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("其他用户首次领券应返回 200，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		var otherOwner sql.NullInt64
		if err := env.d.QueryRowContext(ctx,
			`SELECT user_id FROM coupons WHERE code=$1`, "PROMO_"+itoa(giveaway)+"_"+itoa(env.otherUID)).
			Scan(&otherOwner); err != nil {
			t.Fatalf("查询其他用户的专属券失败: %v", err)
		}
		if !otherOwner.Valid || otherOwner.Int64 != env.otherUID {
			t.Fatalf("其他用户的券必须绑定 %d，实得 %v", env.otherUID, otherOwner)
		}
		// 认领记录唯一：(promotion_id, user_id)。
		var claims int
		if err := env.d.QueryRowContext(ctx,
			`SELECT count(*) FROM promotion_coupon_claims WHERE promotion_id=$1 AND user_id=$2`, giveaway, env.userID).
			Scan(&claims); err != nil {
			t.Fatalf("查询领券记录失败: %v", err)
		}
		if claims != 1 {
			t.Fatalf("同一用户同一活动只允许 1 条领券记录，实得 %d", claims)
		}

		// 我的活动券列表。
		w = env.req(http.MethodGet, "/user/promotion-coupons", env.userSess, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("我的活动券应返回 200，实得 %d", w.Code)
		}
		list := promoE2EArr(t, promoE2EDecode(t, w), "list")
		if len(list) != 1 {
			t.Fatalf("应只有 1 张活动券，实得 %d: %s", len(list), mustJSON(list))
		}
		item := list[0].(map[string]any)
		if promoE2ENum(t, item, "promotion_id") != float64(giveaway) {
			t.Fatalf("活动券 promotion_id 不符: %s", mustJSON(item))
		}
		if promoE2EStr(t, item, "promotion_name") != promoNames["coupon_giveaway"] {
			t.Fatalf("活动券 promotion_name 不符: %s", mustJSON(item))
		}
		if promoE2ENum(t, item, "coupon_value") != 20 {
			t.Fatalf("活动券面值应为 20: %s", mustJSON(item))
		}
		if promoE2EBool(t, item, "used") {
			t.Fatalf("新领的券应为未使用: %s", mustJSON(item))
		}
		if got := promoE2EStr(t, item, "claimed_at"); !promoE2EUserTimeRe.MatchString(got) {
			t.Fatalf("claimed_at 格式应为 2006-01-02 15:04，实得 %q", got)
		}

		// 详情页标记已领取。
		w = env.req(http.MethodGet, "/promotion/"+itoa(giveaway), env.userSess, nil, nil)
		promo := promoE2EObj(t, promoE2EDecode(t, w), "promotion")
		if !promoE2EBool(t, promo, "coupon_claimed") {
			t.Fatalf("已领券用户详情应标记 coupon_claimed=true: %s", mustJSON(promo))
		}
		// 未登录不标记。
		w = env.req(http.MethodGet, "/promotion/"+itoa(giveaway), nil, nil, nil)
		promo = promoE2EObj(t, promoE2EDecode(t, w), "promotion")
		if promoE2EBool(t, promo, "coupon_claimed") {
			t.Fatalf("未登录不应标记 coupon_claimed: %s", mustJSON(promo))
		}
		// 未登录访问我的活动券被拦截。
		w = env.req(http.MethodGet, "/user/promotion-coupons", nil, nil, nil)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("未登录访问我的活动券应跳登录页，实得 %d", w.Code)
		}
	})

	// ---------- 领券：时间窗与启用状态 ----------
	t.Run("已结束或已禁用活动不能领券", func(t *testing.T) {
		ended := promoE2EPrefix + "赠券已结束"
		w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess,
			promoE2ESaveForm(ended, "coupon_giveaway", `{"coupon_id":`+itoa(env.tplCoupon)+`}`,
				-72*time.Hour, -time.Hour, true, 0, env.productID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("保存已结束赠券活动失败: %s", w.Body.String())
		}
		endedID := int64(promoE2ENum(t, promoE2EDecode(t, w), "id"))
		var ppID int64
		if err := env.d.QueryRowContext(ctx,
			`SELECT id FROM promotion_products WHERE promotion_id=$1`, endedID).Scan(&ppID); err != nil {
			t.Fatalf("取活动商品 ID 失败: %v", err)
		}
		w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(endedID)+"/claim", env.userSess,
			map[string]any{"promotion_product_id": ppID}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("已结束活动领券应返回 400，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); got != "当前不在活动领取时间内" {
			t.Fatalf("已结束活动 msg 应为「当前不在活动领取时间内」，实得 %q", got)
		}

		disabled := promoE2EPrefix + "赠券已禁用"
		w = env.req(http.MethodPost, "/admin/promotions/save", env.adminSess,
			promoE2ESaveForm(disabled, "coupon_giveaway", `{"coupon_id":`+itoa(env.tplCoupon)+`}`,
				-time.Hour, 72*time.Hour, false, 0, env.productID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("保存禁用赠券活动失败: %s", w.Body.String())
		}
		disabledID := int64(promoE2ENum(t, promoE2EDecode(t, w), "id"))
		if err := env.d.QueryRowContext(ctx,
			`SELECT id FROM promotion_products WHERE promotion_id=$1`, disabledID).Scan(&ppID); err != nil {
			t.Fatalf("取活动商品 ID 失败: %v", err)
		}
		w = env.reqJSON(http.MethodPost, "/promotion/"+itoa(disabledID)+"/claim", env.userSess,
			map[string]any{"promotion_product_id": ppID}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("已禁用活动领券应返回 400，实得 %d，响应: %s", w.Code, w.Body.String())
		}
		if got := promoE2EStr(t, promoE2EDecode(t, w), "msg"); got != "活动不存在或未启用" {
			t.Fatalf("已禁用活动 msg 应为「活动不存在或未启用」，实得 %q", got)
		}
	})

	// ---------- 限量抢购：真实占额后售罄展示 ----------
	t.Run("抢购名额占满后前台展示售罄", func(t *testing.T) {
		var ppID int64
		if err := env.d.QueryRowContext(ctx,
			`SELECT id FROM promotion_products WHERE promotion_id=$1`, ids["flash_sale"]).Scan(&ppID); err != nil {
			t.Fatalf("取抢购活动商品 ID 失败: %v", err)
		}
		// 走真实占额路径（事务内 FOR UPDATE + sold+1），占满 2 个名额。
		for i := 0; i < 2; i++ {
			tx, err := env.d.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("开启事务失败: %v", err)
			}
			if err := env.promos.TryReserveQuota(ctx, tx, ppID); err != nil {
				_ = tx.Rollback()
				t.Fatalf("第 %d 次占额应成功: %v", i+1, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("提交事务失败: %v", err)
			}
		}
		// 第 3 次必须售罄。
		tx, err := env.d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("开启事务失败: %v", err)
		}
		defer tx.Rollback()
		if err := env.promos.TryReserveQuota(ctx, tx, ppID); err == nil {
			t.Fatalf("名额耗尽后必须拒绝占额")
		}

		w := env.req(http.MethodGet, "/promotion/"+itoa(ids["flash_sale"]), nil, nil, nil)
		cards := promoE2EArr(t, promoE2EDecode(t, w), "products")
		if len(cards) != 1 {
			t.Fatalf("抢购活动应有 1 张卡片，实得 %d", len(cards))
		}
		card := cards[0].(map[string]any)
		if promoE2ENum(t, card, "quota_total") != 2 || promoE2ENum(t, card, "quota_sold") != 2 {
			t.Fatalf("占额后 quota 应为 total=2 sold=2: %s", mustJSON(card))
		}
		if promoE2ENum(t, card, "quota_left") != 0 {
			t.Fatalf("占满后剩余应为 0: %s", mustJSON(card))
		}
		if !promoE2EBool(t, card, "sold_out") {
			t.Fatalf("占满后 sold_out 应为 true: %s", mustJSON(card))
		}
	})

	// ---------- 统计看板 ----------
	t.Run("统计看板累计浏览与领券", func(t *testing.T) {
		giveaway := ids["coupon_giveaway"]
		w := env.req(http.MethodGet, "/promotion/"+itoa(giveaway), nil, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("访问活动详情失败: %d", w.Code)
		}
		w = env.req(http.MethodGet, "/admin/promotions/"+itoa(giveaway)+"/stats", env.adminSess, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("统计接口应返回 200，实得 %d", w.Code)
		}
		stats := promoE2EObj(t, promoE2EDecode(t, w), "stats")
		if promoE2ENum(t, stats, "views") < 2 {
			t.Fatalf("浏览数应至少 2，实得 %v", stats["views"])
		}
		if promoE2ENum(t, stats, "claimed") != 2 {
			t.Fatalf("领券数应为 2（两个用户各一张），实得 %v", stats["claimed"])
		}
		if promoE2ENum(t, stats, "orders") != 0 {
			t.Fatalf("未下单时订单数应为 0，实得 %v", stats["orders"])
		}
		if promoE2ENum(t, stats, "paid_amount") != 0 {
			t.Fatalf("未支付时成交金额应为 0，实得 %v", stats["paid_amount"])
		}
	})

	// ---------- 启停与删除 ----------
	t.Run("停用后前台不可见删除后详情404", func(t *testing.T) {
		target := ids["new_user"]
		w := env.req(http.MethodPost, "/admin/promotions/"+itoa(target)+"/toggle", env.adminSess,
			url.Values{"enabled": {"0"}}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("停用应返回 200，实得 %d", w.Code)
		}
		w = env.req(http.MethodGet, "/promotions", nil, nil, nil)
		for _, it := range promoE2EArr(t, promoE2EDecode(t, w), "list") {
			if m, ok := it.(map[string]any); ok && m["name"] == promoNames["new_user"] {
				t.Fatalf("停用后不应出现在前台列表")
			}
		}
		// 重新启用
		w = env.req(http.MethodPost, "/admin/promotions/"+itoa(target)+"/toggle", env.adminSess,
			url.Values{"enabled": {"1"}}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("启用应返回 200，实得 %d", w.Code)
		}
		w = env.req(http.MethodGet, "/promotions", nil, nil, nil)
		env.findByName(t, promoE2EArr(t, promoE2EDecode(t, w), "list"), promoNames["new_user"])

		// 删除
		w = env.req(http.MethodPost, "/admin/promotions/"+itoa(target)+"/delete", env.adminSess, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("删除应返回 200，实得 %d", w.Code)
		}
		w = env.req(http.MethodGet, "/admin/promotions/"+itoa(target), env.adminSess, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("删除后详情应返回 404，实得 %d", w.Code)
		}
		w = env.req(http.MethodGet, "/promotion/"+itoa(target), nil, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("删除后前台详情应返回 404，实得 %d", w.Code)
		}
		// 删除必须级联清理绑定与统计，不能留下孤儿行。
		for _, tbl := range []string{"promotion_products", "promotion_stats", "promotion_quota", "promotion_coupon_claims"} {
			var n int
			if err := env.d.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE promotion_id=$1`, target).Scan(&n); err != nil {
				// promotion_quota 没有 promotion_id 列，用 promotion_product_id 反查。
				if err2 := env.d.QueryRowContext(ctx,
					`SELECT count(*) FROM promotion_quota WHERE promotion_product_id IN (
						SELECT id FROM promotion_products WHERE promotion_id=$1)`, target).Scan(&n); err2 != nil {
					t.Fatalf("检查 %s 残留失败: %v / %v", tbl, err, err2)
				}
			}
			if n != 0 {
				t.Fatalf("删除活动后 %s 应无残留，实得 %d", tbl, n)
			}
		}
	})
}

// TestPromotionServiceGuards 活动服务层护栏：类型白名单、计价口径、禁用/结束活动不可下单。
func TestPromotionServiceGuards(t *testing.T) {
	env := newPromoE2EEnv(t)
	ctx := context.Background()

	t.Run("类型白名单", func(t *testing.T) {
		for _, typ := range promoE2ESupportedSet {
			if err := env.promoSvc.ValidatePromotionType(typ); err != nil {
				t.Fatalf("%s 应受支持: %v", typ, err)
			}
		}
		for _, typ := range []string{"", "bogo", "group_buy", "Discount", "unknown"} {
			if err := env.promoSvc.ValidatePromotionType(typ); err == nil {
				t.Fatalf("%q 必须被拒绝（未实现类型不得静默放行）", typ)
			}
		}
	})

	t.Run("计价口径", func(t *testing.T) {
		cases := []struct {
			name    string
			typ     string
			sell    float64
			promo   *service.ActivePromotion
			wantAmt float64
			wantDis float64
		}{
			{"折扣生效", "discount", 100, &service.ActivePromotion{Type: "discount", Price: 60}, 60, 40},
			{"折扣高于原价不降价", "discount", 50, &service.ActivePromotion{Type: "discount", Price: 60}, 50, 0},
			{"抢购同折扣", "flash_sale", 100, &service.ActivePromotion{Type: "flash_sale", Price: 30}, 30, 70},
			{"新客同折扣", "new_user", 100, &service.ActivePromotion{Type: "new_user", Price: 45}, 45, 55},
			{"满减达门槛", "full_reduction", 300, &service.ActivePromotion{Type: "full_reduction", Threshold: 200, Reduce: 50}, 250, 50},
			{"满减未达门槛", "full_reduction", 100, &service.ActivePromotion{Type: "full_reduction", Threshold: 200, Reduce: 50}, 100, 0},
			{"满减不超过0", "full_reduction", 60, &service.ActivePromotion{Type: "full_reduction", Threshold: 50, Reduce: 80}, 0, 60},
			{"赠券不改价", "coupon_giveaway", 100, &service.ActivePromotion{Type: "coupon_giveaway", CouponID: 7}, 100, 0},
			{"无活动原价", "", 100, nil, 100, 0},
		}
		for _, c := range cases {
			amt, dis := env.promoSvc.ApplyPromotion(c.sell, c.promo)
			if amt != c.wantAmt || dis != c.wantDis {
				t.Fatalf("%s: ApplyPromotion(%v) = (%v,%v)，期望 (%v,%v)", c.name, c.sell, amt, dis, c.wantAmt, c.wantDis)
			}
		}
	})

	t.Run("禁用与结束活动不参与解析", func(t *testing.T) {
		// 建一个进行中的折扣活动，随后停用/改结束，验证解析结果不再命中。
		form := promoE2ESaveForm(promoE2EPrefix+"守卫活动", "discount", `{"price":1}`,
			-time.Hour, 72*time.Hour, true, 0, env.productID)
		w := env.req(http.MethodPost, "/admin/promotions/save", env.adminSess, form, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("保存守卫活动失败: %s", w.Body.String())
		}
		id := int64(promoE2ENum(t, promoE2EDecode(t, w), "id"))

		ap, err := env.promoSvc.ActivePromotionFor(ctx, env.productID, env.pricesetID, "monthly")
		if err != nil {
			t.Fatalf("解析生效活动失败: %v", err)
		}
		if ap == nil {
			t.Fatalf("进行中且启用的折扣活动应被解析出来")
		}
		if ap.PromotionID != id {
			t.Fatalf("解析到的活动应为 %d，实得 %d", id, ap.PromotionID)
		}
		if ap.Price != 1 {
			t.Fatalf("解析到的活动价应为 1，实得 %v", ap.Price)
		}

		// 指定绑定必须同商品同周期才采纳。
		if _, err := env.promoSvc.ActivePromotionForRequested(ctx, env.productID, env.pricesetID, "monthly", id, ap.PromotionProductID); err != nil {
			t.Fatalf("正确的绑定应被采纳: %v", err)
		}
		if _, err := env.promoSvc.ActivePromotionForRequested(ctx, env.noPricePID, env.pricesetID, "monthly", id, ap.PromotionProductID); err == nil {
			t.Fatalf("跨商品绑定必须被拒绝")
		}
		if _, err := env.promoSvc.ActivePromotionForRequested(ctx, env.productID, env.pricesetID, "monthly", 0, ap.PromotionProductID); err == nil {
			t.Fatalf("promotion_id 非正数必须被拒绝")
		}
		if _, err := env.promoSvc.ActivePromotionForRequested(ctx, env.productID, env.pricesetID, "monthly", id, 0); err == nil {
			t.Fatalf("promotion_product_id 非正数必须被拒绝")
		}

		// 停用后不再命中。
		w = env.req(http.MethodPost, "/admin/promotions/"+itoa(id)+"/toggle", env.adminSess,
			url.Values{"enabled": {"0"}}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("停用失败: %d", w.Code)
		}
		if ap, err := env.promoSvc.ActivePromotionFor(ctx, env.productID, env.pricesetID, "monthly"); err != nil || ap != nil {
			t.Fatalf("停用后不得命中活动: ap=%v err=%v", ap, err)
		}

		// 重新启用但改到已结束，同样不得命中。
		w = env.req(http.MethodPost, "/admin/promotions/"+itoa(id)+"/save", env.adminSess,
			promoE2ESaveForm(promoE2EPrefix+"守卫活动", "discount", `{"price":1}`,
				-72*time.Hour, -time.Hour, true, 0, env.productID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("改结束时间失败: %s", w.Body.String())
		}
		if ap, err := env.promoSvc.ActivePromotionFor(ctx, env.productID, env.pricesetID, "monthly"); err != nil || ap != nil {
			t.Fatalf("已结束活动不得命中: ap=%v err=%v", ap, err)
		}
	})
}
