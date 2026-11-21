CREATE UNIQUE INDEX categories_one_default ON categories (account_id) WHERE is_default = 1 AND deleted_at IS NULL;
