CREATE TABLE pluggy_credentials (
  user_id TEXT PRIMARY KEY,
  credentials BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
