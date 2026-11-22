CREATE TABLE categories (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (account_id <> ''),
  owner_id TEXT NOT NULL,
  name TEXT NOT NULL,
  color TEXT NOT NULL CHECK (color IN ('GRAY', 'RED', 'YELLOW', 'BLUE', 'GREEN')),
  monthly_limit INTEGER NOT NULL DEFAULT 0,
  is_daily INTEGER NOT NULL DEFAULT 0 CHECK (is_daily IN (0, 1)),
  is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);

CREATE INDEX categories_account_id ON categories (account_id);
CREATE UNIQUE INDEX categories_one_default ON categories (account_id) WHERE is_default = 1 AND deleted_at IS NULL;

CREATE TABLE tags (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (account_id <> ''),
  owner_id TEXT NOT NULL,
  name TEXT NOT NULL,
  start_at TEXT NOT NULL,
  end_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  server_seq INTEGER
);

CREATE INDEX tags_account_id ON tags (account_id);

CREATE TABLE transactions (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (account_id <> ''),
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
CREATE INDEX transactions_account_id ON transactions (account_id);

CREATE TABLE recurrences (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (account_id <> ''),
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

CREATE INDEX recurrences_account_id ON recurrences (account_id);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  secret TEXT NOT NULL,
  language TEXT NOT NULL DEFAULT 'pt-BR' CHECK (language IN ('pt-BR', 'en')),
  date_format TEXT NOT NULL DEFAULT 'DD/MM/YYYY' CHECK (date_format IN ('DD/MM/YYYY', 'MM/DD/YYYY', 'YYYY-MM-DD')),
  number_format TEXT NOT NULL DEFAULT '1.234,56' CHECK (number_format IN ('1.234,56', '1,234.56')),
  daily_limit_mode TEXT NOT NULL DEFAULT 'DYNAMIC' CHECK (daily_limit_mode IN ('FIXED', 'DYNAMIC')),
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
  currency TEXT NOT NULL DEFAULT 'BRL' CHECK (currency IN ('BRL', 'USD', 'EUR')),
  monthly_income INTEGER NOT NULL DEFAULT 0 CHECK (monthly_income >= 0),
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
  received_at TEXT NOT NULL
);

CREATE UNIQUE INDEX server_ops_idempotency ON sync_operations (device_id, id);
CREATE INDEX server_ops_pull ON sync_operations (account_id, seq);

CREATE TABLE pluggy_credentials (
  user_id TEXT PRIMARY KEY,
  credentials BLOB NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE connections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  institution TEXT NOT NULL,
  status TEXT NOT NULL,
  execution_status TEXT NOT NULL,
  last_updated_at TEXT,
  last_fetched_at TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX connections_user ON connections (user_id);

CREATE TABLE movements (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  connection_id TEXT,
  institution TEXT NOT NULL,
  bank_account TEXT NOT NULL,
  bank_account_kind TEXT NOT NULL CHECK (bank_account_kind IN ('CHECKING', 'CARD')),
  external_id TEXT NOT NULL,
  description TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('INCOME', 'OUTCOME')),
  amount INTEGER NOT NULL CHECK (amount >= 0),
  occurred_at TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'CONFIRMED', 'IGNORED')),
  created_at TEXT NOT NULL,
  UNIQUE (account_id, institution, bank_account, bank_account_kind, external_id)
);

CREATE UNIQUE INDEX movements_connection_external ON movements (connection_id, external_id) WHERE connection_id IS NOT NULL;
