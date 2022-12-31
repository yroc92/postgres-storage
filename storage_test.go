package certmagicsql

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	_ "github.com/lib/pq"
)

func setup(t *testing.T) *PostgresStorage {
	return setupWithOptions(t, Options{})
}

func setupWithOptions(t *testing.T, options Options) *PostgresStorage {
	connStr := os.Getenv("CONN_STR")
	if connStr == "" {
		t.Skipf("must set CONN_STR")
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := NewStorage(db, options)
	if err != nil {
		t.Fatal(err)
	}
	return storage
}

func dropTable() {
	connStr := os.Getenv("CONN_STR")
	if connStr == "" {
		log.Println("must set CONN_STR")
		return
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec("drop table if exists certmagic_data")
	if err != nil {
		log.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	dropTable()
	os.Exit(m.Run())
}

func TestExists(t *testing.T) {
	storage := setup(t)
	var err error
	key := "testkey"
	defer storage.Delete(key)
	exists := storage.Exists(key)
	if exists {
		t.Fatalf("key should not exist")
	}
	err = storage.Store(key, []byte("testvalue"))
	if err != nil {
		t.Fatal(err)
	}
	exists = storage.Exists(key)
	if !exists {
		t.Fatalf("key should exist")
	}
}

func TestStoreUpdatesModified(t *testing.T) {
	storage := setup(t)
	var err error
	key := "testkey"
	defer storage.Delete(key)

	err = storage.Store(key, []byte("0"))
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := storage.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	err = storage.Store(key, []byte("00"))
	if err != nil {
		t.Fatal(err)
	}
	infoAfter, err := storage.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if !infoBefore.Modified.Before(infoAfter.Modified) {
		t.Fatalf("modified not updated")
	}
	if int64(2) != infoAfter.Size {
		t.Fatalf("size not updated")
	}
}

func TestStoreExistsLoadDelete(t *testing.T) {
	storage := setup(t)
	var err error
	key := "testkey"
	val := []byte("testval")
	defer storage.Delete(key)

	if storage.Exists(key) {
		t.Fatalf("key should not exist")
	}

	err = storage.Store(key, val)
	if err != nil {
		t.Fatal(err)
	}

	if !storage.Exists(key) {
		t.Fatalf("key should exist")
	}

	load, err := storage.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(val, load) {
		t.Fatalf("got: %s", load)
	}

	err = storage.Delete(key)
	if err != nil {
		t.Fatal(err)
	}

	load, err = storage.Load(key)
	if load != nil {
		t.Fatalf("load should be nil")
	}
	if _, ok := err.(certmagic.ErrNotExist); !ok {
		t.Fatalf("err not certmagic.ErrNotExist")
	}
}

func TestStat(t *testing.T) {
	storage := setup(t)
	var err error
	key := "testkey"
	val := []byte("testval")
	defer storage.Delete(key)
	if err = storage.Store(key, val); err != nil {
		t.Fatal(err)
	}
	stat, err := storage.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Modified.IsZero() {
		t.Fatalf("modified should not be zero")
	}
	if !reflect.DeepEqual(stat, certmagic.KeyInfo{
		Key:        key,
		Modified:   stat.Modified,
		Size:       int64(len(val)),
		IsTerminal: true,
	}) {
		t.Fatalf("got: %v", stat)
	}
}

func TestList(t *testing.T) {
	storage := setup(t)
	var err error
	keys := []string{
		"testnohit1",
		"testnohit2",
		"testhit1",
		"testhit2",
		"testhit3",
	}
	for _, key := range keys {
		if err = storage.Store(key, []byte("hit")); err != nil {
			t.Fatal(err)
		}

	}
	defer func() {
		for _, key := range keys {
			if err = storage.Delete(key); err != nil {
				t.Fatal(err)
			}
		}
	}()
	list, err := storage.List("testhit", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("got: %d", len(list))
	}
	sort.Strings(list)
	if !reflect.DeepEqual([]string{"testhit1", "testhit2", "testhit3"}, list) {
		t.Fatalf("got: %v", list)
	}
}

func TestLockLocks(t *testing.T) {
	storage := setup(t)
	ctx := context.Background()
	key := "testkey"
	defer storage.Unlock(key)
	if err := storage.Lock(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := storage.isLocked(storage.Database, key); err == nil {
		t.Fatalf("key should be locked")
	}
	if err := storage.Unlock(key); err != nil {
		t.Fatal(err)
	}
	if err := storage.isLocked(storage.Database, key); err != nil {
		t.Fatal(err)
	}
}

func TestLockExpires(t *testing.T) {
	storage := setupWithOptions(t, Options{LockTimeout: 100 * time.Millisecond})
	ctx := context.Background()
	key := "testkey"
	defer storage.Unlock(key)
	if err := storage.Lock(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := storage.isLocked(storage.Database, key); err == nil {
		t.Fatalf("key should be locked")
	}
	time.Sleep(200 * time.Millisecond)
	if err := storage.isLocked(storage.Database, key); err != nil {
		t.Fatal(err)
	}
}
