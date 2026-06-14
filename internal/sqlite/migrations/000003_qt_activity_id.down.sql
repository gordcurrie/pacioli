-- Reverse 000003: drop qt_activity_id column by rebuilding the transactions table.

CREATE TABLE transactions_old (
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

INSERT INTO transactions_old
    (id, account_id, security_id, type, trade_date, settled_date,
     quantity, price_native, commission_native, fx_rate, price_cad, commission_cad,
     source, notes, linked_transaction_id, created_at)
SELECT id, account_id, security_id, type, trade_date, settled_date,
       quantity, price_native, commission_native, fx_rate, price_cad, commission_cad,
       source, notes, linked_transaction_id, created_at
FROM transactions;

DROP TABLE transactions;

ALTER TABLE transactions_old RENAME TO transactions;

CREATE INDEX idx_transactions_account_id  ON transactions(account_id);
CREATE INDEX idx_transactions_security_id ON transactions(security_id);
CREATE INDEX idx_transactions_trade_date  ON transactions(trade_date);
