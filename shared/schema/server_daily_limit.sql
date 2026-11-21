ALTER TABLE users ADD COLUMN daily_limit_mode TEXT NOT NULL DEFAULT 'DYNAMIC' CHECK (daily_limit_mode IN ('FIXED', 'DYNAMIC'));
