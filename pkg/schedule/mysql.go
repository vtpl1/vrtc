package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// mysqlProvider implements ScheduleProvider using a MySQL database.
//
// Expected table DDL:
//
//	CREATE TABLE recording_schedules (
//	    id              VARCHAR(64)   NOT NULL PRIMARY KEY,
//	    channel_id      VARCHAR(64)   NOT NULL,
//	    storage_path    VARCHAR(1024) NOT NULL,
//	    segment_minutes INT           NOT NULL DEFAULT 0,
//	    start_at        DATETIME      NULL,
//	    end_at          DATETIME      NULL,
//	    days_of_week    JSON
//	);
type mysqlProvider struct {
	db *sql.DB
}

// NewMySQLProvider returns a ScheduleProvider backed by the given *sql.DB.
// The caller owns the DB connection and is responsible for closing it.
func NewMySQLProvider(db *sql.DB) ScheduleProvider {
	return &mysqlProvider{db: db}
}

func (p *mysqlProvider) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, channel_id, storage_path, segment_minutes,
		       start_at, end_at, days_of_week
		FROM recording_schedules
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("schedule mysql: list: %w", err)
	}
	defer rows.Close()

	var out []Schedule

	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("schedule mysql: scan: %w", err)
		}

		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedule mysql: rows: %w", err)
	}

	return out, nil
}

func (p *mysqlProvider) Close() error { return nil }

func scanSchedule(rows *sql.Rows) (Schedule, error) {
	var (
		s          Schedule
		startAt    sql.NullTime
		endAt      sql.NullTime
		daysOfWeek sql.NullString
	)

	if err := rows.Scan(
		&s.ID, &s.ChannelID, &s.StoragePath, &s.SegmentMinutes,
		&startAt, &endAt, &daysOfWeek,
	); err != nil {
		return Schedule{}, err
	}

	if startAt.Valid {
		s.StartAt = startAt.Time.UTC()
	}

	if endAt.Valid {
		s.EndAt = endAt.Time.UTC()
	}

	if daysOfWeek.Valid && daysOfWeek.String != "" {
		var days []int
		if err := json.Unmarshal([]byte(daysOfWeek.String), &days); err != nil {
			return Schedule{}, fmt.Errorf("parse days_of_week JSON: %w", err)
		}

		s.DaysOfWeek = make([]time.Weekday, len(days))
		for i, d := range days {
			s.DaysOfWeek[i] = time.Weekday(d)
		}
	}

	return s, nil
}
