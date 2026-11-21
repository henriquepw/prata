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
