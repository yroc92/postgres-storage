package certmagicsql

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strconv"
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
	Host             string        `json:"host,omitempty"`
	Port             string        `json:"port,omitempty"`
	User             string        `json:"user,omitempty"`
	Password         string        `json:"password,omitempty"`
	DBname           string        `json:"dbname,omitempty"`
	SSLmode          string        `json:"sslmode,omitempty"` // Valid values for sslmode are: disable, require, verify-ca, verify-full
	ConnectionString string        `json:"connection_string,omitempty"`
	DisableDDL       bool          `json:"disable_ddl,omitempty"`
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
		case "host":
			c.Host = value
		case "port":
			c.Port = value
		case "user":
			c.User = value
		case "password":
			c.Password = value
		case "dbname":
			c.DBname = value
		case "sslmode":
			c.SSLmode = value
		case "connection_string":
			c.ConnectionString = value
		case "disable_ddl":
			var err error
			c.DisableDDL, err = strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf(`parsing disable_ddl value %+v: %w`, value, err)
			}
		}
	}

	return nil
}

func (c *PostgresStorage) Provision(ctx caddy.Context) error {
	c.logger = ctx.Logger(c)

	// Load Environment
	if c.Host == "" {
		c.Host = os.Getenv("POSTGRES_HOST")
	}
	if c.Port == "" {
		c.Port = os.Getenv("POSTGRES_PORT")
	}
	if c.User == "" {
		c.User = os.Getenv("POSTGRES_USER")
	}
	if c.Password == "" {
		c.Password = os.Getenv("POSTGRES_PASSWORD")
	}
	if !c.DisableDDL {
		disableDDLString := os.Getenv("POSTGRES_DISABLE_DDL")
		if disableDDLString != "" {
			var err error
			c.DisableDDL, err = strconv.ParseBool(disableDDLString)
			if err != nil {
				return fmt.Errorf(`parsing POSTGRES_DISABLE_DDL value %+v: %w`, disableDDLString, err)
			}
		}
	}
	if c.DBname == "" {
		c.DBname = os.Getenv("POSTGRES_DBNAME")
	}
	if c.SSLmode == "" {
		c.SSLmode = os.Getenv("POSTGRES_SSLMODE")
	}
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
	var connStr string
	if len(c.ConnectionString) > 0 {
		connStr = c.ConnectionString
	} else {
		connStr_fmt := "host=%s port=%s user=%s password=%s dbname=%s sslmode=%s"
		// Set each value dynamically w/ Sprintf
		connStr = fmt.Sprintf(connStr_fmt, c.Host, c.Port, c.User, c.Password, c.DBname, c.SSLmode)
	}

	database, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	s := &PostgresStorage{
		Database:     database,
		QueryTimeout: c.QueryTimeout,
		LockTimeout:  c.LockTimeout,
		DisableDDL:   c.DisableDDL,
	}
	return s, s.ensureTableSetup()
}

func (c *PostgresStorage) CertMagicStorage() (certmagic.Storage, error) {
	return NewStorage(*c)
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
	if s.DisableDDL {
		// Only check if tables exist with expected columns; do not attempt to create them
		tx, err := s.Database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `select key, value, modified from certmagic_data limit 0`)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `select key, expires from certmagic_locks limit 0`)
		if err != nil {
			return err
		}
		return nil
	}
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
