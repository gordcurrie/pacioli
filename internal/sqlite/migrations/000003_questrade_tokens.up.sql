CREATE TABLE questrade_tokens (
    user_id       INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token  TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    api_server    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
