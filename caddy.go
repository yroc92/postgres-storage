package certmagicsql

import (
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/certmagic"
)

func init() {
	caddy.RegisterModule(PostgresStorage{})
}

func (PostgresStorage) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "caddy.storage.postgres",
		New: func() caddy.Module { return new(PostgresStorage) },
	}
}

// CertMagicStorage converts s to a certmagic.Storage instance.
func (s *PostgresStorage) CertMagicStorage() (certmagic.Storage, error) {
	return s, nil
}
