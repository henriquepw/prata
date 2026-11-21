ALTER TABLE accounts ADD COLUMN monthly_income INTEGER NOT NULL DEFAULT 0 CHECK (monthly_income >= 0);
