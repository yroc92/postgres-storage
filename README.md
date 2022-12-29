# certmagic-sqlstorage

[![GoDoc](https://godoc.org/github.com/yroc92/certmagic-sqlstorage?status.svg)](https://godoc.org/github.com/yroc92/certmagic-sqlstorage)

SQL storage for CertMagic/Caddy TLS data.

Currently supports PostgreSQL but it'd be pretty easy to support other RDBs like
SQLite and MySQL. Please make a pull-request if you add support for them and I'll
gladly merge.

Now with support for Caddyfile and environment configuration.

# Example
- Valid values for sslmode are: disable, require, verify-ca, verify-full

With vanilla JSON config file:
```json
{
	  "storage": {
	    	"module": "postgres",
	    	"dbname": "certmagictest",
		"host": "localhost",
		"module": "postgres",
		"password": "postgres",
		"port": "5432",
		"sslmode": "disable",
		"user": "postgres"
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
		sslmode disable
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

Configuring with labels for usage with Swarm and docker-proxy (https://github.com/lucaslorentz/caddy-docker-proxy):
```yaml
deploy:
  labels:
    # Set Storage definitions
    caddy_0.storage: postgres
    caddy_0.storage.host: localhost
    caddy_0.storage.port: "5432"
    caddy_0.storage.user: postgres
    caddy_0.storage.password: postgres
    caddy_0.storage.dbname: certmagictest
    caddy_0.storage.sslmode: disable
```

# Build vanilla Docker image
```Dockerfile
# Version to build
ARG CADDY_VERSION="2.6.2"

FROM caddy:${CADDY_VERSION}-builder AS builder

RUN xcaddy build \
    --with github.com/gabrielmocan/postgres-storage

FROM caddy:${CADDY_VERSION}-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
```

# Build Docker image with docker-proxy support and Cloudflare resolver support
```Dockerfile
# Version to build
ARG CADDY_VERSION="2.6.2"

FROM caddy:${CADDY_VERSION}-builder AS builder

RUN xcaddy build \
    --with github.com/gabrielmocan/postgres-storage      \
    --with github.com/lucaslorentz/caddy-docker-proxy/v2 \
    --with github.com/caddy-dns/cloudflare

FROM caddy:${CADDY_VERSION}-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

CMD ["caddy", "docker-proxy"]
```

# LICENSE

MIT
