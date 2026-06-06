CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT    NOT NULL UNIQUE,
    password_hash TEXT,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    totp_secret   TEXT,
    totp_enabled  INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE accounts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    type           TEXT    NOT NULL CHECK(type IN ('margin','cash','tfsa','rrsp','resp','lrsp','srsp')),
    broker         TEXT    NOT NULL,
    currency       TEXT    NOT NULL DEFAULT 'CAD',
    account_number TEXT,
    is_registered  INTEGER NOT NULL DEFAULT 0,
    source         TEXT    NOT NULL DEFAULT 'manual',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE securities (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker   TEXT NOT NULL,
    exchange TEXT NOT NULL,
    name     TEXT NOT NULL,
    type     TEXT NOT NULL CHECK(type IN ('equity','etf','mutual_fund','option')),
    currency TEXT NOT NULL,
    source   TEXT NOT NULL DEFAULT 'manual',
    UNIQUE(ticker, exchange)
);

CREATE TABLE fx_rates (
    date          TEXT NOT NULL,
    from_currency TEXT NOT NULL,
    to_currency   TEXT NOT NULL,
    rate          TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'boc',
    PRIMARY KEY(date, from_currency, to_currency)
);

CREATE TABLE transactions (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id            INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    security_id           INTEGER NOT NULL REFERENCES securities(id) ON DELETE RESTRICT,
    type                  TEXT    NOT NULL CHECK(type IN (
                              'buy','sell','dividend','roc_adjustment',
                              'fx_conversion','transfer_in','transfer_out','journal'
                          )),
    trade_date            TEXT    NOT NULL,
    settled_date          TEXT    NOT NULL,
    quantity              TEXT    NOT NULL,
    price_native          TEXT    NOT NULL,
    commission_native     TEXT    NOT NULL DEFAULT '0',
    fx_rate               TEXT,
    price_cad             TEXT    NOT NULL,
    commission_cad        TEXT    NOT NULL DEFAULT '0',
    source                TEXT    NOT NULL DEFAULT 'manual' CHECK(source IN ('manual','questrade','canaccord_csv')),
    notes                 TEXT,
    linked_transaction_id INTEGER REFERENCES transactions(id),
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE distributions (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    security_id                 INTEGER NOT NULL REFERENCES securities(id) ON DELETE RESTRICT,
    tax_year                    INTEGER NOT NULL,
    roc_per_unit                TEXT    NOT NULL DEFAULT '0',
    total_distribution_per_unit TEXT,
    record_date                 TEXT,
    source                      TEXT,
    notes                       TEXT,
    UNIQUE(security_id, tax_year)
);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action      TEXT    NOT NULL CHECK(action IN ('create', 'update', 'delete')),
    entity_type TEXT    NOT NULL CHECK(entity_type IN ('account', 'security', 'transaction', 'user')),
    entity_id   INTEGER NOT NULL,
    source      TEXT    NOT NULL DEFAULT 'manual',
    snapshot    TEXT,
    import_id   TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE questrade_tokens (
    user_id       INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token  TEXT    NOT NULL,
    refresh_token TEXT    NOT NULL,
    api_server    TEXT    NOT NULL,
    expires_at    TEXT    NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    TEXT    NOT NULL UNIQUE,
    totp_verified INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL
);

CREATE TABLE recovery_codes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT    NOT NULL,
    used_at   DATETIME
);

CREATE INDEX idx_transactions_account_id  ON transactions(account_id);
CREATE INDEX idx_transactions_security_id ON transactions(security_id);
CREATE INDEX idx_transactions_trade_date  ON transactions(trade_date);
CREATE INDEX idx_accounts_user_id         ON accounts(user_id);
CREATE INDEX idx_audit_log_entity         ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_user_id        ON audit_log(user_id);
CREATE INDEX idx_audit_log_created_at     ON audit_log(created_at);
