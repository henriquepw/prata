CREATE TABLE app_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE sync_operations (
  local_seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  server_seq INTEGER,
  entity TEXT NOT NULL CHECK (entity IN ('CATEGORY', 'TAG', 'TRANSACTION', 'RECURRENCE')),
  entity_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('CREATE', 'UPDATE', 'DELETE')),
  payload TEXT NOT NULL,
  protocol_version INTEGER NOT NULL,
  device_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX sync_operations_pending ON sync_operations (local_seq) WHERE server_seq IS NULL;
