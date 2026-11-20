package store

import "context"

// Activity is one sync operation as the activity log shows it: the device's name, the entity, the kind, the name
// or description in the payload ("" when it has neither, as a DELETE) and the op's created_at, RFC 3339 in UTC.
type Activity struct {
	Device    string `db:"device"`
	Entity    string `db:"entity"`
	Kind      Kind   `db:"kind"`
	Label     string `db:"label"`
	CreatedAt string `db:"created_at"`
}

// Activity returns the account's 12 latest sync operations by created_at, read as a time since devices may send
// offsets, newest first, so a late sync still lands in time order; seq breaks ties.
func (s *Store) Activity(ctx context.Context, accountID string) ([]Activity, error) {
	query := `
		SELECT
			COALESCE(d.name, '') AS device,
			o.entity,
			o.kind,
			COALESCE(json_extract(o.payload, '$.name'), json_extract(o.payload, '$.description'), '') AS label,
			o.created_at
		FROM sync_operations o
		LEFT JOIN devices d ON d.id = o.device_id
		WHERE o.account_id = ?
		ORDER BY julianday(o.created_at) DESC, o.seq DESC
		LIMIT 12
	`

	activity := []Activity{}
	err := s.SelectContext(ctx, &activity, query, accountID)

	return activity, err
}
