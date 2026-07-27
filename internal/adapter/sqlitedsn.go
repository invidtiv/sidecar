package adapter

import (
	"net/url"
	"strconv"
	"time"
)

// ReadOnlyDSN builds a SQLite DSN that opens path strictly read-only.
//
// The "file:" prefix is load-bearing: mattn/go-sqlite3 discards the query
// string for non-URI DSNs and always passes SQLITE_OPEN_READWRITE|CREATE to
// sqlite3_open_v2, so a bare "path?mode=ro" opens the database read-write and
// creates it when missing. Only SQLite's own URI parser honors mode=ro.
//
// journal_mode is deliberately not set: the PRAGMA needs an exclusive write
// lock, which blocks for as long as the owning application holds the database.
func ReadOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_busy_timeout", strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10))
	return u.String() + "?" + q.Encode()
}

// sqliteBusyTimeout bounds how long a read waits on a lock held by the
// application that owns the database.
const sqliteBusyTimeout = 5 * time.Second
