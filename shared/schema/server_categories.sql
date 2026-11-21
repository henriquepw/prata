ALTER TABLE categories ADD COLUMN is_daily INTEGER NOT NULL DEFAULT 0 CHECK (is_daily IN (0, 1));
ALTER TABLE categories ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1));
