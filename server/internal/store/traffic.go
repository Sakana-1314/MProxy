package store

// TrafficDelta is one in-memory hourly batch waiting to be flushed.
type TrafficDelta struct {
	Requests int64
	Bytes    int64
}

// TrafficBucket is one persisted hourly traffic bucket.
type TrafficBucket struct {
	Bucket   int64 `json:"bucket"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

// TrafficTotals aggregates traffic for a time window.
type TrafficTotals struct {
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

// FlushTraffic atomically adds hourly traffic and user byte counters.
func (s *Store) FlushTraffic(hourly map[int64]TrafficDelta, userBytes map[int64]int64) error {
	if len(hourly) == 0 && len(userBytes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if len(hourly) > 0 {
		stmt, err := tx.Prepare(
			`INSERT INTO traffic_stats (bucket, requests, bytes) VALUES (?, ?, ?)
			 ON CONFLICT(bucket) DO UPDATE SET
				requests = requests + excluded.requests,
				bytes = bytes + excluded.bytes`)
		if err != nil {
			return err
		}
		for bucket, delta := range hourly {
			if delta.Requests == 0 && delta.Bytes == 0 {
				continue
			}
			if _, err := stmt.Exec(bucket, delta.Requests, delta.Bytes); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}
	if len(userBytes) > 0 {
		stmt, err := tx.Prepare(
			`UPDATE proxy_users SET total_bytes = total_bytes + ? WHERE id = ?`)
		if err != nil {
			return err
		}
		for id, bytes := range userBytes {
			if id <= 0 || bytes <= 0 {
				continue
			}
			if _, err := stmt.Exec(bytes, id); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}
	return tx.Commit()
}

// TrafficTotalsSince sums requests and bytes from hourly buckets.
func (s *Store) TrafficTotalsSince(since int64) (TrafficTotals, error) {
	var totals TrafficTotals
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(bytes), 0)
		 FROM traffic_stats WHERE bucket >= ?`, since,
	).Scan(&totals.Requests, &totals.Bytes)
	return totals, err
}

// TrafficBucketsSince returns persisted hourly buckets at or after since.
func (s *Store) TrafficBucketsSince(since int64) ([]TrafficBucket, error) {
	rows, err := s.db.Query(
		`SELECT bucket, requests, bytes FROM traffic_stats
		 WHERE bucket >= ? ORDER BY bucket`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrafficBucket, 0, 48)
	for rows.Next() {
		var item TrafficBucket
		if err := rows.Scan(&item.Bucket, &item.Requests, &item.Bytes); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
