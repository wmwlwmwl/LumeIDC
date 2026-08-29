package httpserver

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"

	"lumeidc/internal/config"
	"lumeidc/internal/cron"
	"lumeidc/internal/crypto"
	"lumeidc/internal/db"
	"lumeidc/internal/gateway"
	"lumeidc/internal/handler"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/server/easypanel"
	"lumeidc/internal/server/zjmf"
	"lumeidc/internal/service"

	robfigcron "github.com/robfig/cron/v3"
)

// App 持有所有需要在优雅关闭时释放的资源。
type App struct {
	Server *http.Server
	DB     *sql.DB
	Cron   *robfigcron.Cron
}

// Shutdown 优雅关闭：停止 cron、关闭 HTTP、关闭 DB。
func (a *App) Shutdown(ctx context.Context) {
	if a.Cron != nil {
		a.Cron.Stop()
	}
	if a.Server != nil {
		a.Server.Shutdown(ctx)
	}
	if a.DB != nil {
		a.DB.Close()
	}
}

func Build(cfg *config.Config) (*App, error) {
	database, err := db.Open(cfg.DBDSN)
	if err != nil {
		return nil, err
	}
	// 启动时应用未执行的迁移，保证升级平滑
	if err := db.Migrate(context.Background(), database, db.Migrations()); err != nil {
		return nil, err
	}
	store, err := middleware.NewStore(cfg)
	if err != nil {
		return nil, err
	}
	handler.SetAdminStore(store)
	handler.SetPageStore(store)
	// 供应商注册表：集中分发。新上游在此注册（详见 docs/provider.md）。
	providers := server.NewRegistry()
	providers.Register(zjmf.Provider{})
	providers.Register(easypanel.Provider{})
	// 实例密码加密器（services.password_crypt），密钥与 session 同源
	cryptor, cerr := crypto.New(cfg.SecretKey)
	if cerr != nil {
		log.Fatalf("初始化密码加密器失败: %v", cerr)
	}
	users := &repo.Users{DB: database}
	products := &repo.Products{DB: database}
	serversRepo := &repo.Servers{DB: database}
	auth := &handler.Auth{Users: users, Sessions: store, Lockout: &repo.LoginAttempts{DB: database}}
	gateways := map[string]gateway.Gateway{
		"epay": gateway.Epay{},
		"mock": gateway.Mock{},
	}
	balanceRepo := &repo.Balance{DB: database}
	handler.SetBalanceRepo(balanceRepo)
	settingsRepo := &repo.Settings{DB: database}
	notifier := &service.Notifier{DB: database, Settings: settingsRepo}
	auth.Notifier = notifier
	paymentSvc := &service.Payment{
		DB:           database,
		Servers:      serversRepo,
		Products:     products,
		Provisions:   &repo.ProvisionRepo{DB: database},
		Jobs:         &repo.FulfillmentJobs{DB: database},
		Balance:      balanceRepo,
		Providers:    providers,
		PeriodGrants: &repo.PeriodGrants{DB: database},
		Notifier:     notifier,
		Crypt:        cryptor,
	}
	pay := &handler.Pay{
		Orders:   &service.Orders{DB: database, Products: products, Coupons: &repo.Coupons{DB: database}},
		Payment:  paymentSvc,
		Products: products,
		Gateways: gateways,
		BaseURL:  cfg.BaseURL,
		GwRepo:   &repo.Gateways{DB: database},
		Balance:  balanceRepo,
	}
	adminHandler := &handler.Admin{Admins: &repo.Admins{DB: database}, DB: database, Lockout: &repo.LoginAttempts{DB: database}, Announcements: &repo.Announcements{DB: database}}
	pages := &handler.Pages{
		Products:    products,
		Svc:         &service.ServicesRepo{DB: database},
		Orders:      &service.Orders{DB: database, Products: products, Coupons: &repo.Coupons{DB: database}},
		UsersRepo:   users,
		ServersRepo: serversRepo,
		Console:     &service.Console{DB: database, Servers: serversRepo, Products: products, Providers: providers, Crypt: cryptor},
		Balance:     balanceRepo,
		Notifier:    notifier,
		Announcements: &repo.Announcements{DB: database},
	}

	mux := http.NewServeMux()
	auth.Register(mux)
	pages.Register(mux)
	pay.Register(mux)
	adminHandler.Register(mux)
	gwHandler := &handler.AdminGateway{GwRepo: &repo.Gateways{DB: database}}
	gwHandler.Register(mux)
	srvHandler := &handler.AdminServers{Servers: serversRepo, Providers: providers}
	srvHandler.Register(mux)
	lifecycleSvc := &service.Lifecycle{DB: database, Servers: serversRepo, Products: products, Providers: providers}
	mng := &handler.AdminManage{
		Products: products, Users: users, Servers: serversRepo,
		Balance:   balanceRepo,
		Svc:       &service.ServicesRepo{DB: database},
		Lifecycle: lifecycleSvc,
		Payment:   paymentSvc,
		Providers: providers,
	}
	mux.HandleFunc("GET /admin/users/{id}/edit", mng.UserEdit)
	mux.HandleFunc("POST /admin/users/{id}/save", mng.UserSave)
	mux.HandleFunc("GET /admin/types", mng.TypesList)
	mux.HandleFunc("POST /admin/types/save", mng.TypeSave)
	mux.HandleFunc("POST /admin/types/{id}/delete", mng.TypeDelete)
	mux.HandleFunc("GET /admin/products", mng.ProductsList)
	mux.HandleFunc("GET /admin/products/new", mng.ProductForm)
	mux.HandleFunc("POST /admin/products/save", mng.ProductSave)
	mux.HandleFunc("GET /admin/products/{id}/edit", mng.ProductForm)
	mux.HandleFunc("POST /admin/products/{id}/save", mng.ProductSave)
	mux.HandleFunc("POST /admin/products/{id}/delete", mng.ProductDelete)
	mux.HandleFunc("GET /admin/users", mng.UsersList)
	mux.HandleFunc("GET /admin/services", mng.ServicesList)
	mux.HandleFunc("GET /admin/services/status", mng.ServicesStatusJSON)
	mux.HandleFunc("POST /admin/services/{id}/action", mng.ServiceAction)
	mux.HandleFunc("POST /admin/orders/{id}/refund", mng.OrderRefund)
	mux.HandleFunc("GET /admin/servers/{id}/catalog", mng.CatalogPage)
	mux.HandleFunc("POST /admin/servers/{id}/import", mng.ImportProducts)
	mux.HandleFunc("POST /admin/products/{id}/pull-config", mng.PullConfig)
	mux.HandleFunc("GET /admin/products/upstream-options", mng.UpstreamOptions)
	mux.HandleFunc("GET /admin/products/upstream-config", mng.UpstreamConfig)

	// 健康检查端点（不经过 CSRF）
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := database.PingContext(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"not ready","error":"database unreachable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ready"}`))
	})

	h := store.Middleware(middleware.CSRF(mux))
	addr := cfg.Listen
	if a := os.Getenv("LISTEN"); a != "" {
		addr = a // 环境变量覆盖配置，便于本地多实例测试
	}
	fulfillment := &service.Fulfillment{Jobs: &repo.FulfillmentJobs{DB: database}, Payment: paymentSvc}
	cronJobs := &cron.Jobs{DB: database, Fulfillment: fulfillment, Notifier: notifier, Lifecycle: &service.Lifecycle{
		DB:        database,
		Servers:   serversRepo,
		Products:  products,
		Providers: providers,
	}}
	cronRef := cronJobs.Start()
	return &App{
		Server: &http.Server{Addr: addr, Handler: h},
		DB:     database,
		Cron:   cronRef,
	}, nil
}
