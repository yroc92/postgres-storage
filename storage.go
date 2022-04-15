package certmagicsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/caddyserver/certmagic"
	_ "github.com/lib/pq"
)

var (
	_ certmagic.Storage = (*PostgresStorage)(nil)
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
func NewStorage(storage PostgresStorage) (certmagic.Storage, error) {
	if storage.QueryTimeout == 0 {
		storage.QueryTimeout = time.Second * 3
	}
	if storage.LockTimeout == 0 {
		storage.LockTimeout = time.Minute
	}
	if len(storage.ConnectionString) == 0 {
		return nil, errors.New("please provide a valid Postgres connection URL")
	}
	database, err := sql.Open("postgres", storage.ConnectionString)
	if err != nil {
		return nil, err
	}
	s := &PostgresStorage{
		Database:         database,
		QueryTimeout:     storage.QueryTimeout,
		LockTimeout:      storage.LockTimeout,
		ConnectionString: storage.ConnectionString,
	}
	return s, s.ensureTableSetup()
}

type PostgresStorage struct {
	QueryTimeout     time.Duration `json:"query_timeout,omitempty"`
	LockTimeout      time.Duration `json:"lock_timeout,omitempty"`
	Database         *sql.DB       `json:"-"`
	ConnectionString string        `json:"connection_string,omitempty"`
}

// Database RDBs this library supports, currently supports Postgres only.
type Database int

const (
	Postgres Database = iota
)

func (s *PostgresStorage) ensureTableSetup() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.QueryTimeout)
	defer cancel()
	tx, err := s.Database.BeginTx(ctx, nil)
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
func (s *PostgresStorage) Lock(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()

	tx, err := s.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := s.isLocked(tx, key); err != nil {
		return err
	}

	expires := time.Now().Add(s.LockTimeout)
	if _, err := tx.ExecContext(ctx, `insert into certmagic_locks (key, expires) values ($1, $2) on conflict (key) do update set expires = $2`, key, expires); err != nil {
		return fmt.Errorf("failed to lock key: %s: %w", key, err)
	}

	return tx.Commit()
}

// Unlock the key and implement certmagic.Storage.Unlock.
func (s *PostgresStorage) Unlock(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, `delete from certmagic_locks where key = $1`, key)
	return err
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// isLocked returns nil if the key is not locked.
func (s *PostgresStorage) isLocked(queryer queryer, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.QueryTimeout)
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
func (s *PostgresStorage) Store(ctx context.Context, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, "insert into certmagic_data (key, value) values ($1, $2) on conflict (key) do update set value = $2, modified = current_timestamp", key, value)
	return err
}

// Load retrieves the value at key.
func (s *PostgresStorage) Load(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	var value []byte
	err := s.Database.QueryRowContext(ctx, "select value from certmagic_data where key = $1", key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, fs.ErrNotExist
	}
	return value, err
}

// Delete deletes key. An error should be
// returned only if the key still exists
// when the method returns.
func (s *PostgresStorage) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, "delete from certmagic_data where key = $1", key)
	return err
}

// Exists returns true if the key exists
// and there was no error checking.
func (s *PostgresStorage) Exists(ctx context.Context, key string) bool {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	row := s.Database.QueryRowContext(ctx, "select exists(select 1 from certmagic_data where key = $1)", key)
	var exists bool
	err := row.Scan(&exists)
	return err == nil && exists
}

// List returns all keys that match prefix.
// If recursive is true, non-terminal keys
// will be enumerated (i.e. "directories"
// should be walked); otherwise, only keys
// prefixed exactly by prefix will be listed.
func (s *PostgresStorage) List(ctx context.Context, prefix string, recursive bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	if recursive {
		return nil, fmt.Errorf("recursive not supported")
	}
	rows, err := s.Database.QueryContext(ctx, fmt.Sprintf(`select key from certmagic_data where key like '%s%%'`, prefix))
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
func (s *PostgresStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	var modified time.Time
	var size int64
	row := s.Database.QueryRowContext(ctx, "select length(value), modified from certmagic_data where key = $1", key)
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
