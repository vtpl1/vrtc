package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// mysqlProvider implements ChannelWriter using a MySQL database.
//
// Expected table DDL:
//
//	CREATE TABLE channels (
//	    id         VARCHAR(64)   NOT NULL PRIMARY KEY,
//	    name       VARCHAR(255)  NOT NULL DEFAULT '',
//	    stream_url VARCHAR(1024) NOT NULL,
//	    username   VARCHAR(255)  NOT NULL DEFAULT '',
//	    password   VARCHAR(255)  NOT NULL DEFAULT '',
//	    site_id    INT           NOT NULL DEFAULT 0,
//	    extra      JSON
//	);
type mysqlProvider struct {
	db *sql.DB
}

// NewMySQLProvider returns a ChannelWriter backed by the given *sql.DB.
// The caller owns the DB connection and is responsible for closing it.
func NewMySQLProvider(db *sql.DB) ChannelWriter {
	return &mysqlProvider{db: db}
}

func (p *mysqlProvider) GetChannel(ctx context.Context, id string) (Channel, error) {
	row := p.db.QueryRowContext(
		ctx,
		"SELECT id, name, stream_url, username, password, site_id, extra FROM channels WHERE id = ?",
		id,
	)

	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, fmt.Errorf("%w: %s", ErrChannelNotFound, id)
	}

	if err != nil {
		return Channel{}, fmt.Errorf("channel mysql: get %q: %w", id, err)
	}

	return ch, nil
}

func (p *mysqlProvider) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := p.db.QueryContext(ctx,
		"SELECT id, name, stream_url, username, password, site_id, extra FROM channels ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("channel mysql: list: %w", err)
	}
	defer rows.Close()

	var out []Channel

	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channel mysql: scan: %w", err)
		}

		out = append(out, ch)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel mysql: rows: %w", err)
	}

	return out, nil
}

func (p *mysqlProvider) SaveChannel(ctx context.Context, ch Channel) error {
	extraJSON, err := json.Marshal(ch.Extra)
	if err != nil {
		return fmt.Errorf("channel mysql: marshal extra: %w", err)
	}

	_, err = p.db.ExecContext(ctx,
		`INSERT INTO channels (id, name, stream_url, username, password, site_id, extra)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   name=VALUES(name), stream_url=VALUES(stream_url),
		   username=VALUES(username), password=VALUES(password),
		   site_id=VALUES(site_id), extra=VALUES(extra)`,
		ch.ID, ch.Name, ch.StreamURL, ch.Username, ch.Password, ch.SiteID, string(extraJSON),
	)
	if err != nil {
		return fmt.Errorf("channel mysql: save %q: %w", ch.ID, err)
	}

	return nil
}

func (p *mysqlProvider) DeleteChannel(ctx context.Context, id string) error {
	res, err := p.db.ExecContext(ctx, "DELETE FROM channels WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("channel mysql: delete %q: %w", id, err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrChannelNotFound, id)
	}

	return nil
}

func (p *mysqlProvider) Close() error { return nil }

// scanner abstracts *sql.Row and *sql.Rows so scanChannel works for both.
type scanner interface {
	Scan(dest ...any) error
}

func scanChannel(s scanner) (Channel, error) {
	var (
		ch        Channel
		extraJSON sql.NullString
	)

	if err := s.Scan(
		&ch.ID,
		&ch.Name,
		&ch.StreamURL,
		&ch.Username,
		&ch.Password,
		&ch.SiteID,
		&extraJSON,
	); err != nil {
		return Channel{}, err
	}

	if extraJSON.Valid && extraJSON.String != "" {
		if err := json.Unmarshal([]byte(extraJSON.String), &ch.Extra); err != nil {
			return Channel{}, fmt.Errorf("parse extra JSON: %w", err)
		}
	}

	return ch, nil
}
