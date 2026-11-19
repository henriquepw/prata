DROP VIEW IF EXISTS v_transactions_active;
DROP VIEW IF EXISTS v_balance_realized;
DROP VIEW IF EXISTS v_monthly_summary;
DROP VIEW IF EXISTS v_category_usage;

CREATE VIEW v_transactions_active AS
SELECT t.id,
       t.owner_id,
       t.kind,
       t.amount,
       t.description,
       t.occurred_at,
       t.recurrence_id,
       t.occurrence_date,
       COALESCE(c.id, '') AS category_id,
       COALESCE(c.name, 'Sem categoria') AS category_name,
       COALESCE(c.color, 'GRAY') AS category_color,
       COALESCE(c.monthly_limit, 0) AS category_monthly_limit
FROM transactions t
LEFT JOIN categories c ON c.id = t.category_id AND c.deleted_at IS NULL
WHERE t.deleted_at IS NULL;

CREATE VIEW v_balance_realized AS
SELECT COALESCE(SUM(CASE kind WHEN 'INCOME' THEN amount ELSE -amount END), 0) AS balance
FROM v_transactions_active;

CREATE VIEW v_monthly_summary AS
SELECT kind, category_id, category_name, category_color, occurred_at, amount
FROM v_transactions_active;

CREATE VIEW v_category_usage AS
SELECT category_id, category_name, category_color, category_monthly_limit, occurred_at,
       CASE kind WHEN 'OUTCOME' THEN amount ELSE 0 END AS amount
FROM v_transactions_active;
