package proxy

import (
	stdlog "log"
	"sync"
	"time"

	"cdnproxy/internal/store"
)

// trafficStats aggregates request/traffic counters in memory and flushes them
// to SQLite periodically. Requests never write to the database directly.
type trafficStats struct {
	store    *store.Store
	logger   *stdlog.Logger
	interval time.Duration

	mu     sync.Mutex
	hourly map[int64]*trafficCounter
	users  map[int64]int64

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type trafficCounter struct {
	requests int64
	bytes    int64
}

func newTrafficStats(st *store.Store, logger *stdlog.Logger, interval time.Duration) *trafficStats {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = stdlog.Default()
	}
	stats := &trafficStats{
		store:    st,
		logger:   logger,
		interval: interval,
		hourly:   make(map[int64]*trafficCounter),
		users:    make(map[int64]int64),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go stats.run()
	return stats
}

// Record counts one completed proxy request/connection and its bytes.
func (s *trafficStats) Record(userID int64, bytes int64) {
	hour := startOfLocalHour(time.Now()).Unix()
	s.mu.Lock()
	counter := s.hourly[hour]
	if counter == nil {
		counter = &trafficCounter{}
		s.hourly[hour] = counter
	}
	counter.requests++
	counter.bytes += bytes
	if userID > 0 && bytes > 0 {
		s.users[userID] += bytes
	}
	s.mu.Unlock()
}

// PendingUserBytes returns unflushed bytes for quota checks.
func (s *trafficStats) PendingUserBytes(userID int64) int64 {
	if userID <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[userID]
}

func (s *trafficStats) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.flush()
		case <-s.stop:
			s.flush()
			return
		}
	}
}

func (s *trafficStats) flush() {
	s.mu.Lock()
	if len(s.hourly) == 0 && len(s.users) == 0 {
		s.mu.Unlock()
		return
	}
	hourly := s.hourly
	users := s.users
	s.hourly = make(map[int64]*trafficCounter)
	s.users = make(map[int64]int64)
	s.mu.Unlock()

	batch := make(map[int64]store.TrafficDelta, len(hourly))
	for bucket, counter := range hourly {
		batch[bucket] = store.TrafficDelta{Requests: counter.requests, Bytes: counter.bytes}
	}
	if err := s.store.FlushTraffic(batch, users); err != nil {
		s.logger.Printf("[proxy] 刷新流量统计失败: %v", err)
		// Put the batch back so a transient database error does not lose it.
		s.mu.Lock()
		for bucket, delta := range batch {
			counter := s.hourly[bucket]
			if counter == nil {
				counter = &trafficCounter{}
				s.hourly[bucket] = counter
			}
			counter.requests += delta.Requests
			counter.bytes += delta.Bytes
		}
		for id, bytes := range users {
			s.users[id] += bytes
		}
		s.mu.Unlock()
	}
}

// Close flushes the remaining batch and stops the background ticker.
func (s *trafficStats) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
	})
}

func startOfLocalHour(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.Local)
}
