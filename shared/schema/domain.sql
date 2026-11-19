CREATE TABLE categories (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  name TEXT NOT NULL,
  color TEXT NOT NULL CHECK (color IN ('GRAY', 'RED', 'YELLOW', 'BLUE', 'GREEN')),
  monthly_limit INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);

CREATE TABLE tags (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  name TEXT NOT NULL,
  start_at TEXT NOT NULL,
  end_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);

CREATE TABLE transactions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  category_id TEXT,
  tag_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('INCOME', 'OUTCOME')),
  amount INTEGER NOT NULL,
  description TEXT,
  occurred_at TEXT NOT NULL,
  recurrence_id TEXT,
  occurrence_date TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);

CREATE UNIQUE INDEX transactions_occurrence_unique
  ON transactions (recurrence_id, occurrence_date)
  WHERE recurrence_id IS NOT NULL;
CREATE INDEX transactions_occurred_at ON transactions (occurred_at) WHERE deleted_at IS NULL;
CREATE INDEX transactions_category ON transactions (category_id) WHERE deleted_at IS NULL;

CREATE TABLE recurrences (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  category_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('INCOME', 'OUTCOME')),
  amount INTEGER NOT NULL,
  description TEXT,
  rule TEXT NOT NULL,
  start_at TEXT NOT NULL,
  end_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);
