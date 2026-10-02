package quota

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type HistoryDimensions struct {
	InboundTag  string
	UserName    string
	InboundType string
	Protocol    string
	Target      string
}

type historyCounter struct {
	dimensions   HistoryDimensions
	uplink       atomic.Int64
	downlink     atomic.Int64
	lastUplink   int64
	lastDownlink int64
}

const maxHistorySeries = 4096

const historyOtherTarget = "(other)"

type HistoryStore struct {
	db            *sql.DB
	access        sync.Mutex
	counters      map[HistoryDimensions]*historyCounter
	lastFlush     time.Time
	retentionDays int
}

type HistoryPoint struct {
	BucketStart   int64  `json:"bucket_start"`
	InboundTag    string `json:"inbound_tag,omitempty"`
	UserName      string `json:"user_name,omitempty"`
	InboundType   string `json:"inbound_type,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Target        string `json:"target,omitempty"`
	UplinkBytes   int64  `json:"uplink_bytes"`
	DownlinkBytes int64  `json:"downlink_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	PeakBPS       int64  `json:"peak_bytes_per_second"`
	ActiveUsers   int64  `json:"active_users"`
}

type HistoryQuery struct {
	From, To                                            time.Time
	Granularity                                         string
	InboundTag, UserName, InboundType, Protocol, Target string
}

func OpenHistory(path string, retentionDays int) (*HistoryStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS quota_hourly (
 bucket_start INTEGER NOT NULL, inbound_tag TEXT NOT NULL, user_name TEXT NOT NULL,
 inbound_type TEXT NOT NULL, protocol TEXT NOT NULL, target TEXT NOT NULL,
 uplink_bytes INTEGER NOT NULL, downlink_bytes INTEGER NOT NULL, peak_bps INTEGER NOT NULL,
 PRIMARY KEY(bucket_start,inbound_tag,user_name,inbound_type,protocol,target)
);
CREATE INDEX IF NOT EXISTS quota_hourly_time ON quota_hourly(bucket_start);
CREATE INDEX IF NOT EXISTS quota_hourly_user ON quota_hourly(inbound_tag,user_name,bucket_start);
CREATE INDEX IF NOT EXISTS quota_hourly_target ON quota_hourly(target,bucket_start);`); err != nil {
		db.Close()
		return nil, err
	}
	return &HistoryStore{db: db, counters: make(map[HistoryDimensions]*historyCounter), lastFlush: time.Now(), retentionDays: retentionDays}, nil
}

func (s *HistoryStore) Counters(d HistoryDimensions) (*atomic.Int64, *atomic.Int64) {
	s.access.Lock()
	defer s.access.Unlock()
	counter := s.counters[d]
	if counter == nil && len(s.counters) >= maxHistorySeries {
		// Target cardinality is effectively unbounded on a public proxy. Keep
		// memory and hourly SQLite rows bounded, while retaining exact totals
		// for the user/inbound/protocol dimensions.
		d.Target = historyOtherTarget
		counter = s.counters[d]
	}
	if counter == nil {
		counter = &historyCounter{dimensions: d}
		s.counters[d] = counter
	}
	return &counter.uplink, &counter.downlink
}

func (s *HistoryStore) Flush(now time.Time) error {
	s.access.Lock()
	defer s.access.Unlock()
	elapsed := now.Sub(s.lastFlush).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO quota_hourly
(bucket_start,inbound_tag,user_name,inbound_type,protocol,target,uplink_bytes,downlink_bytes,peak_bps)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(bucket_start,inbound_tag,user_name,inbound_type,protocol,target)
DO UPDATE SET uplink_bytes=uplink_bytes+excluded.uplink_bytes, downlink_bytes=downlink_bytes+excluded.downlink_bytes,
peak_bps=MAX(peak_bps,excluded.peak_bps)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	type pendingCounter struct {
		counter          *historyCounter
		uplink, downlink int64
	}
	pending := make([]pendingCounter, 0, len(s.counters))
	bucket := now.UTC().Truncate(time.Hour).Unix()
	for _, c := range s.counters {
		uplink, downlink := c.uplink.Load(), c.downlink.Load()
		du, dd := uplink-c.lastUplink, downlink-c.lastDownlink
		if du == 0 && dd == 0 {
			continue
		}
		peak := int64(float64(du+dd) / elapsed)
		d := c.dimensions
		if _, err = stmt.Exec(bucket, d.InboundTag, d.UserName, d.InboundType, d.Protocol, d.Target, du, dd, peak); err != nil {
			stmt.Close()
			tx.Rollback()
			return err
		}
		pending = append(pending, pendingCounter{counter: c, uplink: uplink, downlink: downlink})
	}
	if err = stmt.Close(); err != nil {
		tx.Rollback()
		return err
	}
	if s.retentionDays > 0 {
		cutoff := now.UTC().AddDate(0, 0, -s.retentionDays).Truncate(time.Hour).Unix()
		if _, err = tx.Exec(`DELETE FROM quota_hourly WHERE bucket_start < ?`, cutoff); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, item := range pending {
		item.counter.lastUplink = item.uplink
		item.counter.lastDownlink = item.downlink
	}
	s.lastFlush = now
	return nil
}

func (s *HistoryStore) Query(ctx context.Context, q HistoryQuery) ([]HistoryPoint, error) {
	bucketExpr := "bucket_start"
	switch q.Granularity {
	case "", "hour":
	case "day":
		bucketExpr = "bucket_start - (bucket_start % 86400)"
	case "month":
		bucketExpr = "CAST(strftime('%s', datetime(bucket_start, 'unixepoch', 'start of month')) AS INTEGER)"
	default:
		return nil, fmt.Errorf("invalid granularity %q", q.Granularity)
	}
	where := []string{"bucket_start >= ?", "bucket_start < ?"}
	args := []any{q.From.Unix(), q.To.Unix()}
	filters := []struct{ name, value string }{{"inbound_tag", q.InboundTag}, {"user_name", q.UserName}, {"inbound_type", q.InboundType}, {"protocol", q.Protocol}, {"target", q.Target}}
	for _, f := range filters {
		if f.value != "" {
			where = append(where, f.name+" = ?")
			args = append(args, f.value)
		}
	}
	query := `SELECT ` + bucketExpr + `,SUM(uplink_bytes),SUM(downlink_bytes),MAX(peak_bps),COUNT(DISTINCT inbound_tag || char(0) || user_name)
FROM quota_hourly WHERE ` + strings.Join(where, " AND ") + ` GROUP BY 1 ORDER BY 1`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err = rows.Scan(&p.BucketStart, &p.UplinkBytes, &p.DownlinkBytes, &p.PeakBPS, &p.ActiveUsers); err != nil {
			return nil, err
		}
		p.UsedBytes = p.UplinkBytes + p.DownlinkBytes
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *HistoryStore) Top(ctx context.Context, q HistoryQuery, dimension string, limit int) ([]HistoryPoint, error) {
	columns := map[string]string{"user": "inbound_tag,user_name", "target": "target", "inbound": "inbound_tag", "protocol": "protocol"}
	column, ok := columns[dimension]
	if !ok {
		return nil, fmt.Errorf("invalid dimension %q", dimension)
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	where := []string{"bucket_start >= ?", "bucket_start < ?"}
	args := []any{q.From.Unix(), q.To.Unix()}
	filters := []struct{ name, value string }{{"inbound_tag", q.InboundTag}, {"user_name", q.UserName}, {"inbound_type", q.InboundType}, {"protocol", q.Protocol}, {"target", q.Target}}
	for _, filter := range filters {
		if filter.value != "" {
			where = append(where, filter.name+" = ?")
			args = append(args, filter.value)
		}
	}
	query := `SELECT ` + column + `,SUM(uplink_bytes),SUM(downlink_bytes),MAX(peak_bps) FROM quota_hourly WHERE ` + strings.Join(where, " AND ") + ` GROUP BY ` + column + ` ORDER BY SUM(uplink_bytes+downlink_bytes) DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		var err error
		switch dimension {
		case "user":
			err = rows.Scan(&p.InboundTag, &p.UserName, &p.UplinkBytes, &p.DownlinkBytes, &p.PeakBPS)
		case "target":
			err = rows.Scan(&p.Target, &p.UplinkBytes, &p.DownlinkBytes, &p.PeakBPS)
		case "inbound":
			err = rows.Scan(&p.InboundTag, &p.UplinkBytes, &p.DownlinkBytes, &p.PeakBPS)
		case "protocol":
			err = rows.Scan(&p.Protocol, &p.UplinkBytes, &p.DownlinkBytes, &p.PeakBPS)
		}
		if err != nil {
			return nil, err
		}
		p.UsedBytes = p.UplinkBytes + p.DownlinkBytes
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *HistoryStore) Close() error { return s.db.Close() }
