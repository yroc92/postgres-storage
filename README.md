# certmagic-sqlstorage

[![GoDoc](https://godoc.org/github.com/yroc92/certmagic-sqlstorage?status.svg)](https://godoc.org/github.com/yroc92/certmagic-sqlstorage)

Forked from: https://github.com/yroc92/postgres-storage

SQL storage for CertMagic/Caddy TLS data.

Currently supports PostgreSQL but it'd be pretty easy to support other RDBs like
SQLite and MySQL. Please make a pull-request if you add support for them and I'll
gladly merge.

Now with support for Caddyfile and environment configuration.

# Example

At the top level of your Caddy JSON config:
```json
{
	  "storage": {
	    	"module": "postgres",
	    	"connection_string": "postgres://user:password@localhost:5432/certmagictest"
	  }
	  "app": {
	    	...
	  }
}
```

With Caddyfile:
```Caddyfile
# Global Config

{
	storage postgres {
		dbname certmagictest
		host localhost
		password postgres
		port 5432
		sslmode disable # Valid values for sslmode are: disable, require, verify-ca, verify-full
		user postgres
	}
}
```

From Environment:
```text
POSTGRES_HOST
POSTGRES_PORT
POSTGRES_USER
POSTGRES_PASSWORD
POSTGRES_DBNAME
POSTGRES_SSLMODE
```

# LICENSE

MIT
