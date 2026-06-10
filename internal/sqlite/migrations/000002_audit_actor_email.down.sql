-- Reverse 000002: drop actor_email, restore user_id NOT NULL with ON DELETE CASCADE.
-- Rows where user_id IS NULL (actor deleted) are lost; that is expected on downgrade.

CREATE TABLE audit_log_old (
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

INSERT INTO audit_log_old (id, user_id, action, entity_type, entity_id, source, snapshot, import_id, created_at)
SELECT id, user_id, action, entity_type, entity_id, source, snapshot, import_id, created_at
FROM audit_log
WHERE user_id IS NOT NULL;

DROP TABLE audit_log;

ALTER TABLE audit_log_old RENAME TO audit_log;

CREATE INDEX idx_audit_log_entity     ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_user_id    ON audit_log(user_id);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at);
