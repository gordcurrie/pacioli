-- Rebuild audit_log with:
--   actor_email: denormalized actor email captured at write time so audit
--                history survives user deletion or email changes.
--   user_id:     made nullable with ON DELETE SET NULL so rows are kept
--                when the actor user is deleted.

CREATE TABLE audit_log_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    actor_email TEXT    NOT NULL DEFAULT '',
    action      TEXT    NOT NULL CHECK(action IN ('create', 'update', 'delete')),
    entity_type TEXT    NOT NULL CHECK(entity_type IN ('account', 'security', 'transaction', 'user')),
    entity_id   INTEGER NOT NULL,
    source      TEXT    NOT NULL DEFAULT 'manual',
    snapshot    TEXT,
    import_id   TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO audit_log_new (id, user_id, actor_email, action, entity_type, entity_id, source, snapshot, import_id, created_at)
SELECT al.id, al.user_id, COALESCE(u.email, ''), al.action, al.entity_type, al.entity_id, al.source, al.snapshot, al.import_id, al.created_at
FROM audit_log al
LEFT JOIN users u ON al.user_id = u.id;

DROP TABLE audit_log;

ALTER TABLE audit_log_new RENAME TO audit_log;

CREATE INDEX idx_audit_log_entity     ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_user_id    ON audit_log(user_id);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at);
