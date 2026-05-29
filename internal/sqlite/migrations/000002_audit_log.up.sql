ALTER TABLE accounts ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE securities ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    action      TEXT NOT NULL CHECK(action IN ('create', 'delete')),
    entity_type TEXT NOT NULL CHECK(entity_type IN ('account', 'security', 'transaction')),
    entity_id   INTEGER NOT NULL,
    source      TEXT NOT NULL DEFAULT 'manual',
    snapshot    TEXT,
    import_id   TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_audit_log_entity     ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_user_id    ON audit_log(user_id);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at);
