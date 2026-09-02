package repo

import "database/sql"

// Constructors keep the database handle private to each repository. The
// composition root is the only package that needs to provide *sql.DB.
func NewAdmins(db *sql.DB) *Admins                   { return &Admins{db: db} }
func NewAdminLog(db *sql.DB) *AdminLog               { return &AdminLog{db: db} }
func NewAnnouncements(db *sql.DB) *Announcements     { return &Announcements{db: db} }
func NewAuthChallenges(db *sql.DB) *AuthChallenges   { return &AuthChallenges{db: db} }
func NewBalance(db *sql.DB) *Balance                 { return &Balance{db: db} }
func NewCoupons(db *sql.DB) *Coupons                 { return &Coupons{db: db} }
func NewFulfillmentJobs(db *sql.DB) *FulfillmentJobs { return &FulfillmentJobs{db: db} }
func NewGateways(db *sql.DB) *Gateways               { return &Gateways{db: db} }
func NewIdentityStore(db *sql.DB) *IdentityStore     { return &IdentityStore{db: db} }
func NewIntegrations(db *sql.DB) *Integrations       { return &Integrations{db: db} }
func NewLoginAttempts(db *sql.DB) *LoginAttempts     { return &LoginAttempts{db: db} }
func NewPeriodGrants(db *sql.DB) *PeriodGrants       { return &PeriodGrants{db: db} }
func NewProducts(db *sql.DB) *Products               { return &Products{db: db} }
func NewProvisionRepo(db *sql.DB) *ProvisionRepo     { return &ProvisionRepo{db: db} }
func NewRefunds(db *sql.DB) *Refunds                 { return &Refunds{db: db} }
func NewServers(db *sql.DB) *Servers                 { return &Servers{db: db} }
func NewSettings(db *sql.DB) *Settings               { return &Settings{db: db} }
func NewStats(db *sql.DB) *Stats                     { return &Stats{db: db} }
func NewUsers(db *sql.DB) *Users                     { return &Users{db: db} }
func NewInvoices(db *sql.DB) *Invoices               { return &Invoices{db: db} }
