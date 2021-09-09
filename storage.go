package certmagicsql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/caddyserver/certmagic"
)

var (
	_ certmagic.Storage = (*postgresStorage)(nil)
)

// DB represents the required database API. You can use a *database/sql.DB.
type DB interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

// NewStorage creates a new storage instance. The `db` you pass in will likely be a
// *database/sql.DB. This method will create the required metadata tables if necessary
// (certmagic_data and certmagic_locks).
func NewStorage(db DB, options Options) (certmagic.Storage, error) {
	if options.QueryTimeout == 0 {
		options.QueryTimeout = time.Second * 3
	}
	if options.LockTimeout == 0 {
		options.LockTimeout = time.Minute
	}
	if options.Database != Postgres {
		return nil, fmt.Errorf("unsupported database")
	}
	s := &postgresStorage{
		db:           db,
		queryTimeout: options.QueryTimeout,
		lockTimeout:  options.LockTimeout,
	}
	return s, s.ensureTableSetup()
}

type postgresStorage struct {
	db           DB
	queryTimeout time.Duration
	lockTimeout  time.Duration
}

// Database RDBs this library supports, currently supports Postgres only.
type Database int

const (
	Postgres Database = iota
)

type Options struct {
	QueryTimeout time.Duration `json:"query_timeout,omitempty"`
	LockTimeout  time.Duration `json:"lock_timeout,omitempty"`
	Database     Database      `json:"database,omitempty"`
}

func (s *postgresStorage) ensureTableSetup() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `create table if not exists certmagic_data (
key text primary key,
value bytea,
modified timestamptz default current_timestamp
)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `create table if not exists certmagic_locks (
key text primary key,
expires timestamptz default current_timestamp
)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Lock the key and implement certmagic.Storage.Lock.
func (s *postgresStorage) Lock(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := s.isLocked(tx, key); err != nil {
		return err
	}

	expires := time.Now().Add(s.lockTimeout)
	if _, err := tx.ExecContext(ctx, `insert into certmagic_locks (key, expires) values ($1, $2) on conflict (key) do update set expires = $2`, key, expires); err != nil {
		return fmt.Errorf("failed to lock key: %s: %w", key, err)
	}

	return tx.Commit()
}

// Unlock the key and implement certmagic.Storage.Unlock.
func (s *postgresStorage) Unlock(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `delete from certmagic_locks where key = $1`, key)
	return err
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// isLocked returns nil if the key is not locked.
func (s *postgresStorage) isLocked(queryer queryer, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	row := queryer.QueryRowContext(ctx, `select exists(select 1 from certmagic_locks where key = $1 and expires > current_timestamp)`, key)
	var locked bool
	if err := row.Scan(&locked); err != nil {
		return err
	}
	if locked {
		return fmt.Errorf("key is locked: %s", key)
	}
	return nil
}

// Store puts value at key.
func (s *postgresStorage) Store(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "insert into certmagic_data (key, value) values ($1, $2) on conflict (key) do update set value = $2, modified = current_timestamp", key, value)
	return err
}

// Load retrieves the value at key.
func (s *postgresStorage) Load(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	var value []byte
	err := s.db.QueryRowContext(ctx, "select value from certmagic_data where key = $1", key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, certmagic.ErrNotExist(fmt.Errorf("key not found: %s", key))
	}
	return value, err
}

// Delete deletes key. An error should be
// returned only if the key still exists
// when the method returns.
func (s *postgresStorage) Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "delete from certmagic_data where key = $1", key)
	return err
}

// Exists returns true if the key exists
// and there was no error checking.
func (s *postgresStorage) Exists(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	row := s.db.QueryRowContext(ctx, "select exists(select 1 from certmagic_data where key = $1)", key)
	var exists bool
	err := row.Scan(&exists)
	return err == nil && exists
}

// List returns all keys that match prefix.
// If recursive is true, non-terminal keys
// will be enumerated (i.e. "directories"
// should be walked); otherwise, only keys
// prefixed exactly by prefix will be listed.
func (s *postgresStorage) List(prefix string, recursive bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	if recursive {
		return nil, fmt.Errorf("recursive not supported")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`select key from certmagic_data where key like '%s%%'`, prefix))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// Stat returns information about key.
func (s *postgresStorage) Stat(key string) (certmagic.KeyInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()
	var modified time.Time
	var size int64
	row := s.db.QueryRowContext(ctx, "select length(value), modified from certmagic_data where key = $1", key)
	err := row.Scan(&size, &modified)
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	return certmagic.KeyInfo{
		Key:        key,
		Modified:   modified,
		Size:       size,
		IsTerminal: true,
	}, nil
}
