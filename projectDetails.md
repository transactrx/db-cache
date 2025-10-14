## Project Details — db-cache

### What this project is

`db-cache` is a small, generic Go library that builds and maintains an in‑memory cache from a PostgreSQL query and refreshes itself automatically when the underlying database tables change.

- **Data source**: your SQL query (any SELECT you choose)
- **Change detection**: lightweight `table_log` in Postgres, updated by triggers on your tables
- **Refresh strategy**: a periodic poll computes a “staleness fingerprint”; if it differs from the last one, the cache reloads
- **Access**: fast, in‑memory lookups via `Get(key) []T`, `GetAll() []T`, and manual `ForceRefresh()`

### High-level architecture

```
┌────────────────┐          ┌──────────────────────────┐
│  Your service  │──Get()──▶│  In‑memory cache (map)   │
│  (Go process)  │         │  key:string → []T        │
└───────▲────────┘         └───────────▲──────────────┘
        │                              │
        │                      reload if stale
        │                              │
        │                ┌─────────────┴──────────────┐
        │                │ Background poller (ticker) │
        │                │ - Compute staleness        │
        │                │ - If changed, reload       │
        │                └─────────────▲──────────────┘
        │                              │
        │                       reads fingerprint
        │                              │
        │                 ┌────────────┴────────────┐
        │                 │ Postgres: table_log     │
        │                 │ (table_name, time)      │
        │                 └────────────▲────────────┘
        │                              │
        │                 updated by DB triggers
        │                              │
        │           ┌──────────────────┴──────────────────┐
        └──────────▶│ Your monitored tables (e.g., X, Y)  │
                    │ AFTER INSERT/UPDATE/DELETE trigger  │
                    └─────────────────────────────────────┘
```

### Key concepts

- **Dataset query (load SQL)**: The SELECT used to populate the cache with rows of type `T`.
- **Monitored tables**: Tables whose changes should invalidate the cache (e.g., `[]string{"api_keys"}`).
- **Key field**: The struct field (string) in `T` used as the map key. The library groups rows by this key to build `map[string][]T`.
- **Change signal table (`table_log`)**: A single table in Postgres with last‑change timestamps per monitored table. Updated by triggers on each write.
- **Staleness fingerprint**: A compact string derived from `table_log` (per table: `count || max(operation_time)`) used to detect changes.

---

## Quick start

### Requirements

- Go 1.24+
- PostgreSQL 12+
- Dependencies: `github.com/jackc/pgx/v5`, `github.com/georgysavva/scany/v2`

### Minimal example

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// Define the row type to cache. Use db tags to map columns.
type ApiKey struct {
    ID        *int       `db:"id"`
    ApiKey    *string    `db:"api_key"`
    UserID    *string    `db:"user_id"`   // Key field must be a *string
    IsActive  *bool      `db:"is_active"`
    CreatedAt *time.Time `db:"created_at"`
    UpdatedAt *time.Time `db:"updated_at"`
}

func main() {
    reader, _ := pgxpool.New(context.Background(), "postgres://user:pass@host:5432/db?sslmode=disable")
    writer := reader // in simple setups, the same pool can be used

    // Optionally, pre-create DB artifacts (table_log, functions, trigger factory)
    // _ = dbcache.CreateDbTriggersAndTables(writer)

    // Create the cache
    cache, err := dbcache.CreateCache[ApiKey](
        nil, // logger (nil → default logger)
        "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = true",
        []string{"api_keys"}, // monitored tables
        "UserID",             // key field on struct (must be exported, *string)
        5*time.Second,         // check interval
        reader,                 // reader pool (used for queries)
        writer,                 // writer pool (used to create triggers)
    )
    if err != nil { log.Fatal(err) }

    // Lookups
    user1Keys := cache.Get("user1")
    allKeys := cache.GetAll()
    _ = user1Keys
    _ = allKeys

    // Manual refresh if needed
    _ = cache.ForceRefresh()
}
```

---

## API reference

### Constructor

```go
func CreateCache[T any](
    logger *log.Logger,
    SQL string,
    monitoredTables []string,
    keyField string,
    cacheCheckInterval time.Duration,
    DB *pgxpool.Pool,    // read pool
    DB_RW *pgxpool.Pool, // write pool (for creating triggers)
    SQLParams ...interface{},
) (*DbCache[T], error)
```

- **logger**: Optional; if `nil`, a default logger to stdout is used.
- **SQL / SQLParams**: The load query and its parameters (scanned into values of `T`).
- **monitoredTables**: The tables to watch for changes; triggers will write into `table_log` for these.
- **keyField**: Name of the exported struct field on `T` used as the cache key; must be of type `*string`.
- **cacheCheckInterval**: How often to poll the staleness fingerprint.
- **DB / DB_RW**:
  - `DB`: used for reading (loading cache, staleness checks)
  - `DB_RW`: used to create monitoring triggers; if unavailable, cache still works but may not auto‑create triggers.

### Cache instance

```go
type DbCache[T any] struct {
    // Fields are internal; use methods below
}

func (c *DbCache[T]) Get(key string) []T
func (c *DbCache[T]) GetAll() []T
func (c *DbCache[T]) ForceRefresh() error
```

- **Get**: Returns all cached rows whose key equals `key`. Returns `nil` if not present.
- **GetAll**: Returns all cached rows across all keys.
- **ForceRefresh**: Clears the fingerprint and reloads immediately if possible.

---

## Internals

### How monitored tables are specified

You pass `monitoredTables` to `CreateCache`. They are stored in the cache and used for:

- Creating monitoring triggers (via the writer pool)
- Building the staleness SQL used during polling

```go
cache, err := CreateCache[ApiKey](
    logger, sql,
    []string{"api_keys", "products"}, // <= here
    "UserID",
    5*time.Second,
    reader, writer,
)
```

### What creates/updates `table_log`

- Postgres triggers—not the cache—update `table_log`.
- The library provides DDL helpers to create what you need:

```sql
CREATE TABLE IF NOT EXISTS table_log (
    table_name     TEXT PRIMARY KEY,
    operation_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE OR REPLACE FUNCTION log_changes() RETURNS TRIGGER AS $$
BEGIN
   INSERT INTO table_log(table_name, operation_time)
   VALUES (TG_TABLE_NAME, current_timestamp)
   ON CONFLICT (table_name)
   DO UPDATE SET operation_time = excluded.operation_time;
   RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION create_table_monitor_trigger(table_name TEXT) RETURNS VOID AS $$
BEGIN
   IF NOT EXISTS (
      SELECT 1 FROM pg_trigger
      WHERE tgname='monitor_changes' AND tgenabled='O' AND tgisinternal='f'
        AND tgrelid=(table_name::regclass)::oid
   ) THEN
      EXECUTE format('CREATE TRIGGER monitor_changes\n'
                     'AFTER INSERT OR UPDATE OR DELETE ON %I\n'
                     'FOR EACH ROW EXECUTE FUNCTION log_changes();', table_name);
   END IF;
END; $$ LANGUAGE plpgsql;
```

At runtime, the cache attempts to call `select create_table_monitor_trigger('<table>')` for each monitored table using the writer pool. Failures are logged as warnings (the cache still works; you can create the triggers yourself).

### How staleness is determined

On each tick, the cache queries a “fingerprint” derived from `table_log`:

- For a single table `t`: `count(*) || cast(coalesce(max(operation_time),'1980-01-01') as varchar)`
- For multiple tables: compute the same per table and aggregate them via `string_agg`.

Examples (exact strings used in tests):

```sql
-- Single table (api_keys)
select count(*) || cast(case when max(operation_time) is null then '1980-01-01'
                             else max(operation_time) end as varchar) as ct
from table_log where table_name='api_keys'

-- Multiple tables (api_keys, products)
select string_agg(ct, ', ') from (
  select count(*) || cast(case when max(operation_time) is null then '1980-01-01'
                               else max(operation_time) end as varchar) as ct
  from table_log where table_name='api_keys'
  union all
  select count(*) || cast(case when max(operation_time) is null then '1980-01-01'
                               else max(operation_time) end as varchar) as ct
  from table_log where table_name='products'
) as t
```

The cache compares the new fingerprint to the previous one:

- **If equal** → “already up to date”, skip reload
- **If different** → run the load SQL, rebuild the in‑memory map, store the new fingerprint

### What invalidation means (and why monitor)

- **Invalidation** here means the in‑memory snapshot may be stale relative to Postgres.
- Monitoring is required because the cache lives in process memory and does not automatically “see” database writes—triggers provide a cheap, reliable signal.

### Loading data and keying

When reload is needed, the cache executes your load SQL using the reader pool and scans rows into `T` via `scany`. It then groups rows by the configured `keyField` into `map[string][]T`.

Keying rules:

- The `keyField` must be the name of an exported struct field on `T` of type `*string`.
- If your column name differs from the struct field, use `db:"column_name"` tags or alias the column in SQL.
- Map types as `T` are not recommended (reflection with maps is limited and type‑unsafe in this library).

Concurrency:

- Public reads (`Get`, `GetAll`) hold a read lock; reload takes a write lock and swaps the internal map.

Background lifecycle:

- A background goroutine polls on a fixed interval. It is created by `CreateCache` and runs for the process lifetime.

---

## Testing and local development

This repo includes a fully self‑contained test harness.

- **One‑shot**: `./test.sh` (starts Docker Postgres, runs tests, tears down)
- **Makefile**:
  - `make test-all` — start DB, run tests, teardown
  - `make test-all-coverage` — includes coverage report
  - `make test-dev` — keeps DB running; iterate with `make test`

The container exposes Postgres on `localhost:5433` and auto‑initializes:

- `table_log`, `log_changes()`, `create_table_monitor_trigger(text)`
- Sample tables: `api_keys`, `products`, with seed data

---

## FAQs (answering common questions directly)

- **Where are the underlying tables specified?**
  - In `CreateCache` via `monitoredTables []string`.

- **What criterion determines staleness?**
  - A string fingerprint from `table_log` per monitored table: `count(*) || max(operation_time)`; any change in count or last change time changes the fingerprint.

- **What does invalidation mean and why monitor for it?**
  - Invalidation means the cached snapshot may be outdated. Monitoring is needed so the in‑memory cache knows when to reload after DB writes.

- **Does the in‑memory cache update `table_log`?**
  - No. Postgres triggers do. The library can help you create those triggers; they run inside Postgres on INSERT/UPDATE/DELETE.

- **“If changed” — if what changes? How does it know?**
  - If the staleness fingerprint string changes compared to the last seen value, the cache reloads. It computes this by querying `table_log` each interval.

---

## Limitations and trade‑offs

- Keys are `string`; the key field on `T` is expected to be `*string` (exported field).
- Full reloads on change (no incremental updates).
- Background goroutine currently runs for process lifetime (no stop signal in the API).
- Triggers are `FOR EACH ROW` (simplest, but more invocations for bulk writes). You may adapt them to `FOR EACH STATEMENT` if desired.

---

## Practical tips

- Prefer consistent `db` struct tags rather than relying on column aliases.
- Keep your load SQL narrow—only select columns you need.
- Consider separate reader/writer pools when using read replicas.
- If trigger creation fails (e.g., writer unavailable), the cache still loads and works; you can create triggers later or manage them yourself.

---

## Pointers to source

- Core cache: `pkg/db-cache/db_cache.go`
- DB init helpers (DDL): `pkg/db-cache/init.go`
- Tests and examples: `pkg/db-cache/db_cache_test.go`, `cmd/example`



