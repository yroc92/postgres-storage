# certmagic-sqlstorage

[![GoDoc](https://godoc.org/github.com/yroc92/certmagic-sqlstorage?status.svg)](https://godoc.org/github.com/yroc92/certmagic-sqlstorage)

SQL storage for CertMagic/Caddy TLS data.

Currently supports PostgreSQL but it'd be pretty easy to support other RDBs like
SQLite and MySQL. Please make a pull-request if you add support for them and I'll
gladly merge.

# Example

At the top level of your Caddy JSON config:
```json
	"storage": {
		"module": "postgres",
		"connection_string": "postgres://user:password@localhost:5432/postgres"
	},
```

# LICENSE

MIT
