CREATE TABLE connections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  institution TEXT NOT NULL,
  status TEXT NOT NULL,
  execution_status TEXT NOT NULL,
  last_updated_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX connections_user ON connections (user_id);
