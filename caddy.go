package certmagicsql

import (
	"github.com/caddyserver/caddy/v2"
)

func init() {
	caddy.RegisterModule(Options{})
}

func (Options) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "caddy.storage.postgres",
		New: func() caddy.Module { return new(Options) },
	}
}
