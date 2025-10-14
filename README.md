The DB CACHE requires that the following SQL resources

***NOTE:*** The following SQL scripts are provided as a convenience.  They are not required to be used.  The only requirement is that the SQL resources exist in the database.
If you would like to automate the execution of these scripts as part of your particular solution, there is a `CreateDbTriggersAndTables` function which you can invoke to do so.

```sql
CREATE  TABLE IF NOT EXISTS table_log (
   table_name text PRIMARY KEY,
   operation_time timestamp default current_timestamp
);
alter table table_log owner to rds_superuser;

CREATE OR REPLACE FUNCTION log_changes()
RETURNS TRIGGER AS $$
BEGIN
   INSERT INTO table_log(table_name, operation_time)
   VALUES (TG_TABLE_NAME, current_timestamp)
   ON CONFLICT (table_name)
   DO UPDATE SET operation_time = excluded.operation_time;

   RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION create_table_monitor_trigger(table_name text)
RETURNS VOID AS $$
BEGIN
   IF NOT EXISTS (
      SELECT 1
      FROM pg_trigger
      WHERE tgname = 'monitor_changes' AND
            tgenabled = 'O' AND
            tgisinternal = 'f' AND
            tgrelid = (table_name::regclass)::oid
   ) THEN
      EXECUTE format('
         CREATE TRIGGER monitor_changes
         AFTER INSERT OR UPDATE OR DELETE ON %I
         FOR EACH ROW EXECUTE FUNCTION log_changes();
      ', table_name);
   END IF;
END;
$$ LANGUAGE plpgsql;;
```

## Using this library with Snowflake

This repo also provides a Snowflake-backed cache that uses a persistent change signal in Snowflake (see `snowflakeCache.md`). Unlike Postgres (which uses triggers to update `table_log`), Snowflake relies on Streams and a centralized Task that writes heartbeats into a durable table `DB_CACHE_LOG`. Your application only reads `DB_CACHE_LOG` and reloads when the fingerprint changes.

### Minimal example

```go
package main

import (
    "context"
    "database/sql"
    "log"
    "time"

    _ "github.com/snowflakedb/gosnowflake" // register Snowflake driver
    snowflakecache "github.com/transactrx/db-cache/pkg/snowflake-cache"
)

type ApiKey struct {
    ID        *int       `db:"id"`
    ApiKey    *string    `db:"api_key"`
    UserID    *string    `db:"user_id"`   // key field: string or *string
    IsActive  *bool      `db:"is_active"`
    CreatedAt *time.Time `db:"created_at"`
    UpdatedAt *time.Time `db:"updated_at"`
}

func main() {
    // Your DSN (can be built via gosnowflake DSN helpers)
    dsn := "user:pass@account/DB/SCHEMA?warehouse=WH&role=ROLE"

    db, err := sql.Open("snowflake", dsn)
    if err != nil { log.Fatal(err) }
    defer db.Close()
    if err := db.PingContext(context.Background()); err != nil { log.Fatal(err) }

    // The schema that contains DB_CACHE_LOG (see snowflakeCache.md provisioning)
    signalSchema := "UTILS"

    // Load SQL for your dataset
    loadSQL := "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = TRUE"

    // Tables to monitor for invalidation (schema + table)
    monitored := []snowflakecache.SnowflakeTable{{Schema: "PUBLIC", Table: "API_KEYS"}}

    cache, err := snowflakecache.CreateSnowflakeCache[ApiKey](
        nil,            // logger (nil -> default)
        db,             // *sql.DB using gosnowflake
        signalSchema,   // schema that hosts DB_CACHE_LOG
        loadSQL,        // SELECT to populate cache
        "UserID",       // key field on struct (string or *string)
        5*time.Second,  // poll interval
        monitored,      // monitored tables
    )
    if err != nil { log.Fatal(err) }
    defer cache.Close()

    // Lookups
    user1Keys := cache.Get("user1")
    allKeys := cache.GetAll()
    _ = user1Keys
    _ = allKeys

    // Manual refresh if needed
    _ = cache.ForceRefresh()
}
```

### Provisioning required for Snowflake

- A centralized Task and per-table Streams must be provisioned to write heartbeats into `DB_CACHE_LOG` (the durable change signal). See `snowflakeCache.md` for exact DDL and procedures.
- The application typically needs only `SELECT` on `DB_CACHE_LOG` and no privileges on Streams/Tasks.

### Interface differences (Postgres vs Snowflake)

- Constructor:
  - Postgres: `dbcache.CreateCache[T](logger, sql, monitoredTables []string, keyField string, interval, DB, DB_RW, params...) (*DbCache[T], error)`
  - Snowflake: `snowflakecache.CreateSnowflakeCache[T](logger, db *sql.DB, signalSchema string, loadSQL string, keyField string, interval, monitored []SnowflakeTable, params...) (*SnowflakeCache[T], error)`
- Key field type:
  - Postgres: expects an exported field (commonly `*string`).
  - Snowflake: exported `string` or `*string` is accepted.
- Invalidation source:
  - Postgres: triggers update `table_log` (the library can help create them).
  - Snowflake: a centralized Task consumes Streams and writes to `DB_CACHE_LOG` (provisioned separately).
- Lifecycle:
  - Both provide `Get`, `GetAll`, and `ForceRefresh`.
  - Snowflake cache also exposes `Close()` to stop its background poller.

For full Snowflake design and setup steps, see `snowflakeCache.md`.

### Migration: Postgres → Snowflake (minimal changes)

To minimize disruption, a compatibility constructor mirrors the Postgres parameter order and semantics:

```go
// Postgres (existing)
cache, _ := dbcache.CreateCache[T](
    logger,
    sql,
    []string{"api_keys"},
    "UserID",
    5*time.Second,
    DB, DB_RW,
)

// Snowflake (compat constructor)
cache, _ := snowflakecache.CreateSnowflakeCacheCompat[T](
    logger,
    sql,
    []string{"PUBLIC.API_KEYS"}, // or []string{"API_KEYS"} with defaultSchema below
    "UserID",
    5*time.Second,
    db,              // *sql.DB (gosnowflake)
    "UTILS",        // signalSchema (schema that hosts DB_CACHE_LOG)
    "PUBLIC",       // defaultSchema (used if tables are unqualified)
)
```

- Keep: `logger`, `sql`, `monitoredTables ([]string)`, `keyField`, `interval`.
- Change: supply `db *sql.DB`, `signalSchema` (e.g., `UTILS`), and `defaultSchema` (e.g., `PUBLIC`).
- Methods remain the same: `Get`, `GetAll`, `ForceRefresh`. Snowflake also offers `Close()` to stop the background poller.