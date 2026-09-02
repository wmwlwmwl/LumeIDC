package service

import (
	"context"
	"database/sql"

	"lumeidc/internal/crypto"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

func NewNotifier(db *sql.DB, settings *repo.Settings) *Notifier {
	return &Notifier{db: db, Settings: settings}
}

func NewServicesRepo(db *sql.DB) *ServicesRepo { return &ServicesRepo{db: db} }

func NewOrders(db *sql.DB, products *repo.Products, coupons *repo.Coupons, identity interface {
	IsApproved(context.Context, int64) (bool, error)
}) *Orders {
	return &Orders{db: db, Products: products, Coupons: coupons, Identity: identity}
}

func NewConsole(db *sql.DB, servers *repo.Servers, products *repo.Products, providers *server.Registry, crypt *crypto.Cryptor) *Console {
	return &Console{db: db, Servers: servers, Products: products, Providers: providers, Crypt: crypt}
}

func NewLifecycle(db *sql.DB, servers *repo.Servers, products *repo.Products, providers *server.Registry) *Lifecycle {
	return &Lifecycle{db: db, Servers: servers, Products: products, Providers: providers}
}

func NewPayment(db *sql.DB, lifecycle *Lifecycle, servers *repo.Servers, products *repo.Products, provisions *repo.ProvisionRepo, jobs *repo.FulfillmentJobs, balance *repo.Balance, providers *server.Registry, periodGrants *repo.PeriodGrants, notifier *Notifier, crypt *crypto.Cryptor) *Payment {
	return &Payment{db: db, Lifecycle: lifecycle, Servers: servers, Products: products, Provisions: provisions, Jobs: jobs, Balance: balance, Providers: providers, PeriodGrants: periodGrants, Notifier: notifier, Crypt: crypt}
}

func NewFulfillment(jobs *repo.FulfillmentJobs, payment *Payment, lifecycle *Lifecycle) *Fulfillment {
	return &Fulfillment{Jobs: jobs, Payment: payment, Lifecycle: lifecycle}
}
