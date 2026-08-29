-- LumeIDC initial schema (PostgreSQL)

CREATE TABLE admin_users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    discount_percent NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (discount_percent BETWEEN 0 AND 100),
    is_default BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    group_id BIGINT REFERENCES groups(id),
    status SMALLINT NOT NULL DEFAULT 1, -- 1 active, 0 disabled
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE product_types (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    sort INT NOT NULL DEFAULT 0
);

CREATE TABLE pricesets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE products (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type_id BIGINT REFERENCES product_types(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    stock INT NOT NULL DEFAULT -1, -- -1 unlimited
    hidden BOOLEAN NOT NULL DEFAULT false,
    sort INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- price per cycle in CNY; cycles: monthly/quarterly/yearly
CREATE TABLE product_prices (
    product_id BIGINT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    priceset_id BIGINT NOT NULL REFERENCES pricesets(id) ON DELETE CASCADE,
    monthly NUMERIC(12,2) NOT NULL DEFAULT 0,
    quarterly NUMERIC(12,2) NOT NULL DEFAULT 0,
    yearly NUMERIC(12,2) NOT NULL DEFAULT 0,
    PRIMARY KEY (product_id, priceset_id)
);

CREATE TABLE servers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    api_url TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE services (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    product_id BIGINT NOT NULL REFERENCES products(id),
    server_id BIGINT REFERENCES servers(id),
    order_id BIGINT,
    name TEXT NOT NULL DEFAULT '',
    hostname TEXT NOT NULL DEFAULT '',
    password_crypt TEXT NOT NULL DEFAULT '', -- AES-GCM encrypted
    status SMALLINT NOT NULL DEFAULT 0, -- 0 pending,1 active,2 suspended,3 terminated
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_services_user ON services(user_id);
CREATE INDEX idx_services_expires ON services(status, expires_at);

CREATE TABLE orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    product_id BIGINT NOT NULL REFERENCES products(id),
    priceset_id BIGINT NOT NULL REFERENCES pricesets(id),
    cycle TEXT NOT NULL CHECK (cycle IN ('monthly','quarterly','yearly')),
    amount NUMERIC(12,2) NOT NULL,
    status SMALLINT NOT NULL DEFAULT 0, -- 0 unpaid,1 paid,2 cancelled
    service_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at TIMESTAMPTZ
);
CREATE INDEX idx_orders_user ON orders(user_id);

CREATE TABLE invoices (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    no TEXT NOT NULL UNIQUE, -- human readable invoice number
    user_id BIGINT NOT NULL REFERENCES users(id),
    order_id BIGINT REFERENCES orders(id),
    amount NUMERIC(12,2) NOT NULL,
    status SMALLINT NOT NULL DEFAULT 0, -- 0 unpaid,1 paid,2 void
    gateway TEXT NOT NULL DEFAULT '',
    trade_no TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at TIMESTAMPTZ
);
CREATE INDEX idx_invoices_user ON invoices(user_id);

CREATE TABLE gateways (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code TEXT NOT NULL UNIQUE, -- mock / epay
    name TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO groups (name, discount_percent, is_default) VALUES ('默认分组', 0, true);
INSERT INTO pricesets (name)
SELECT '默认价格组'
WHERE NOT EXISTS (SELECT 1 FROM pricesets);
INSERT INTO settings (key, value) VALUES ('installed', 'false');
