# certmagic-sqlstorage

[![GoDoc](https://godoc.org/github.com/yroc92/certmagic-sqlstorage?status.svg)](https://godoc.org/github.com/yroc92/certmagic-sqlstorage)

SQL storage for CertMagic/Caddy TLS data.

Currently supports PostgreSQL but it'd be pretty easy to support other RDBs like
SQLite and MySQL. Please make a pull-request if you add support for them and I'll
gladly merge.

# Example

```go
db, err := sql.Open("postgres", "conninfo")
if err != nil { ... }
storage, err := certmagicsql.NewStorage(
    db,
    certmagicsql.Options{},
)
if err != nil { ... }
certmagic.Default.Storage = storage
```

# LICENSE

MIT
