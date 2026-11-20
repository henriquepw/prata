package store

import "context"

// CategoryUsage is a live category with its OUTCOME total in cents over a period.
type CategoryUsage struct {
	Category
	Used int64 `db:"used"`
}

// RealizedBalance is v_balance_realized scoped to the account: INCOME minus OUTCOME of its live transactions, in cents.
func (s *Store) RealizedBalance(ctx context.Context, accountID string) (int64, error) {
	query := `
		SELECT COALESCE(SUM(CASE kind WHEN 'INCOME' THEN amount ELSE -amount END), 0)
		FROM transactions
		WHERE account_id = ? AND deleted_at IS NULL
	`

	var balance int64
	err := s.GetContext(ctx, &balance, query, accountID)

	return balance, err
}

// CategoryUsages returns the account's live categories by name with their OUTCOME total over occurred_at in
// [from, to), RFC 3339 in UTC.
func (s *Store) CategoryUsages(ctx context.Context, accountID, from, to string) ([]CategoryUsage, error) {
	query := `
		SELECT c.id, c.name, c.color, c.monthly_limit, COALESCE(SUM(t.amount), 0) AS used
		FROM categories c
		LEFT JOIN transactions t ON t.category_id = c.id
			AND t.deleted_at IS NULL
			AND t.kind = 'OUTCOME'
			AND t.occurred_at >= ? AND t.occurred_at < ?
		WHERE c.account_id = ? AND c.deleted_at IS NULL
		GROUP BY c.id
		ORDER BY c.name, c.id
	`

	usages := []CategoryUsage{}
	err := s.SelectContext(ctx, &usages, query, from, to, accountID)

	return usages, err
}

// ConfirmedOccurrences returns the account's confirmed occurrence dates by recurrence id, deleted transactions
// included since they still hold the unique (recurrence_id, occurrence_date).
func (s *Store) ConfirmedOccurrences(ctx context.Context, accountID string) (map[string][]string, error) {
	query := `
		SELECT recurrence_id, occurrence_date
		FROM transactions
		WHERE account_id = ? AND recurrence_id IS NOT NULL AND occurrence_date IS NOT NULL
	`

	var rows []struct {
		RecurrenceID   string `db:"recurrence_id"`
		OccurrenceDate string `db:"occurrence_date"`
	}
	err := s.SelectContext(ctx, &rows, query, accountID)
	if err != nil {
		return nil, err
	}

	confirmed := map[string][]string{}
	for _, row := range rows {
		confirmed[row.RecurrenceID] = append(confirmed[row.RecurrenceID], row.OccurrenceDate)
	}

	return confirmed, nil
}
