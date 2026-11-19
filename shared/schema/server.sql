CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  secret TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE account_users (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('OWNER', 'MEMBER')),
  created_at TEXT NOT NULL,
  UNIQUE (account_id, user_id)
);

CREATE TABLE devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  name TEXT,
  api_key_hash TEXT,
  cursor INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE sync_operations (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  entity TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  protocol_version INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  UNIQUE (device_id, id)
);

CREATE INDEX server_ops_pull ON sync_operations (account_id, seq);

ALTER TABLE categories ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tags ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE recurrences ADD COLUMN account_id TEXT NOT NULL DEFAULT '';

CREATE INDEX categories_account_id ON categories (account_id);
CREATE INDEX tags_account_id ON tags (account_id);
CREATE INDEX transactions_account_id ON transactions (account_id);
CREATE INDEX recurrences_account_id ON recurrences (account_id);
