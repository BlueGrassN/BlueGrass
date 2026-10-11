package dbpool

import (
	"testing"
	"time"
)

func TestPoolHasBoundedCapacityWithoutOpeningConnections(t *testing.T) {
	db, err := Open(Config{Host: "127.0.0.1", Port: 3306, Database: "fixtures", User: "fixture", MaxOpen: 3, MaxIdle: 2}, true, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Stats().MaxOpenConnections != 3 {
		t.Fatal("connection pool must have an explicit cap")
	}
	if db.Stats().OpenConnections != 0 {
		t.Fatal("constructing a pool must not perform network I/O")
	}
}

func TestInvalidPoolBudgetOrTLSIsRejected(t *testing.T) {
	for _, c := range []Config{
		{Host: "localhost", Port: 3306, Database: "test", User: "u", MaxOpen: 0},
		{Host: "localhost", Port: 3306, Database: "test", User: "u", MaxOpen: 2, MaxIdle: 3},
		{Host: "localhost", Port: 3306, Database: "test", User: "u", MaxOpen: 2, TLS: "skip-verify"},
	} {
		if db, err := Open(c, true, time.UTC); err == nil {
			db.Close()
			t.Fatal("unsafe configuration accepted")
		}
	}
}
