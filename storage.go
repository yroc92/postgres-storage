package certmagicsqlcaddyfile

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/certmagic"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
)

var (
	_ certmagic.Storage = (*PostgresStorage)(nil)
)

type PostgresStorage struct {
	logger *zap.Logger

	QueryTimeout     time.Duration `json:"query_timeout,omitempty"`
	LockTimeout      time.Duration `json:"lock_timeout,omitempty"`
	Database         *sql.DB       `json:"-"`
	ConnectionString string        `json:"connection_string,omitempty"`
}

func init() {
	caddy.RegisterModule(PostgresStorage{})
}

func (c *PostgresStorage) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		var value string

		key := d.Val()

		if !d.Args(&value) {
			continue
		}

		switch key {
		case "conn_string":
			c.ConnectionString = value
		}
	}

	return nil
}

func (c *PostgresStorage) Provision(ctx caddy.Context) error {
	c.logger = ctx.Logger(c)

	// Load Environment
	if c.ConnectionString == "" {
		c.ConnectionString = os.Getenv("POSTGRES_CONN_STRING")
	}
	if c.QueryTimeout == 0 {
		c.QueryTimeout = time.Second * 3
	}
	if c.LockTimeout == 0 {
		c.LockTimeout = time.Minute
	}

	return nil
}

func (PostgresStorage) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "caddy.storage.postgres",
		New: func() caddy.Module {
			return new(PostgresStorage)
		},
	}
}

func NewStorage(c PostgresStorage) (certmagic.Storage, error) {
	database, err := sql.Open("postgres", c.ConnectionString)
	if err != nil {
		return nil, err
	}
	s := &PostgresStorage{
		Database:         database,
		QueryTimeout:     c.QueryTimeout,
		LockTimeout:      c.LockTimeout,
		ConnectionString: c.ConnectionString,
	}
	return s, s.ensureTableSetup()
}

func (s *PostgresStorage) CertMagicStorage() (certmagic.Storage, error) {
	return NewStorage(*s)
}

// DB represents the required database API. You can use a *database/sql.DB.
type DB interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
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
func (s PostgresStorage) Lock(ctx context.Context, key string) error {
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
func (s PostgresStorage) Unlock(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, `delete from certmagic_locks where key = $1`, key)
	return err
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// isLocked returns nil if the key is not locked.
func (s PostgresStorage) isLocked(queryer queryer, key string) error {
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
func (s PostgresStorage) Store(ctx context.Context, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, "insert into certmagic_data (key, value) values ($1, $2) on conflict (key) do update set value = $2, modified = current_timestamp", key, value)
	return err
}

// Load retrieves the value at key.
func (s PostgresStorage) Load(ctx context.Context, key string) ([]byte, error) {
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
func (s PostgresStorage) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.QueryTimeout)
	defer cancel()
	_, err := s.Database.ExecContext(ctx, "delete from certmagic_data where key = $1", key)
	return err
}

// Exists returns true if the key exists
// and there was no error checking.
func (s PostgresStorage) Exists(ctx context.Context, key string) bool {
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
func (s PostgresStorage) List(ctx context.Context, prefix string, recursive bool) ([]string, error) {
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
func (s PostgresStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
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
