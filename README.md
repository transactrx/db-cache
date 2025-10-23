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

## Using this library with Snowflake (Unified API)

This repo provides a unified cache API for both Postgres and Snowflake via `dbcache.CreateCache`. Unlike Postgres (which uses triggers to update `table_log`), Snowflake does not support triggers in the same way. For Snowflake, you should rely on a durable change signal table (e.g., `TABLE_LOG`) that is updated by a centralized background process (Streams + Task) or by your application logic.

### Minimal example (Snowflake)

```go
package main

import (
    "context"
    "database/sql"
    "log"
    "time"

    _ "github.com/snowflakedb/gosnowflake" // register Snowflake driver
    dbcache "github.com/transactrx/db-cache/pkg/db-cache"
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

    // Load SQL for your dataset
    loadSQL := "SELECT ID AS \"id\", API_KEY AS \"api_key\", USER_ID AS \"user_id\", IS_ACTIVE AS \"is_active\", CREATED_AT AS \"created_at\", UPDATED_AT AS \"updated_at\" FROM PUBLIC.API_KEYS WHERE IS_ACTIVE = TRUE"

    // Monitored tables for invalidation (unqualified names use default schema)
    monitored := []string{"API_KEYS"}

    // Create cache using the unified API.
    // For Snowflake, pass the TABLE_LOG location as "DATABASE.SCHEMA" in the DB_RW parameter.
    cache, err := dbcache.CreateCache[ApiKey](
        nil,             // logger (nil -> default)
        loadSQL,         // SELECT to populate cache
        monitored,       // monitored tables (for invalidation)
        "UserID",        // key field on struct (string or *string)
        5*time.Second,   // poll interval
        db,              // *sql.DB (gosnowflake)
        "MY_DB.MY_SCHEMA", // TABLE_LOG location: Database.Schema
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

### Provisioning required for Snowflake

- Create a durable change signal table (e.g., `TABLE_LOG`) in a chosen schema (e.g., `MY_DB.MY_SCHEMA`).
- A centralized Task and per-table Streams (or application-side logging) should write entries into `TABLE_LOG` when monitored tables change.
- The library does not create Tasks. It relies on `TABLE_LOG` being updated by your platform. See `integration-tests/snowflake/README.md` for recommended approaches (Streams + Task, stored procedures, or application-level logging).

### Interface differences (Postgres vs Snowflake)

- Constructor (Unified):
  - `dbcache.CreateCache[T](logger, sql, monitoredTables []string, keyField string, interval, DB, DB_RW, params...) (Cache[T], error)`
- DB parameter types:
  - Postgres: `DB` is `*pgxpool.Pool`; `DB_RW` is `*pgxpool.Pool` (used to create triggers).
  - Snowflake: `DB` is `*sql.DB` (gosnowflake); `DB_RW` is a string `"DATABASE.SCHEMA"` that points to where `TABLE_LOG` resides.
- Key field type:
  - Postgres and Snowflake accept `string` or `*string` exported fields.
- Invalidation source:
  - Postgres: triggers update `table_log` (the library can create the required functions/triggers).
  - Snowflake: your centralized process updates `TABLE_LOG` (Streams + Task or application-level logging).

For Snowflake provisioning and examples, see `integration-tests/snowflake/README.md`.

### Migration: Postgres → Snowflake (minimal changes)

Your call-site remains largely the same; the unified constructor adapts based on DB type:

```go
// Postgres
cache, _ := dbcache.CreateCache[T](
    logger,
    sql,
    []string{"api_keys"},
    "UserID",
    5*time.Second,
    pgxReadPool,   // *pgxpool.Pool
    pgxWritePool,  // *pgxpool.Pool
)

// Snowflake
cache, _ := dbcache.CreateCache[T](
    logger,
    sql,
    []string{"API_KEYS"},
    "UserID",
    5*time.Second,
    snowflakeDB,              // *sql.DB (gosnowflake)
    "MY_DB.MY_SCHEMA",       // TABLE_LOG location
)
```