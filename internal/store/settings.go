package store

import "context"

// All returns every runtime setting override. It satisfies config.Overrides.
func (s *Store) All() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

func (s *Store) SetSetting(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, k string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, k)
	return err
}
