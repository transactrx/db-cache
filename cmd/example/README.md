# DB Cache Examples

This directory contains examples showing how to use the unified `dbcache.CreateCache` API with both PostgreSQL and Snowflake.

## Key Feature: Unified API

The same `dbcache.CreateCache` function works with both PostgreSQL and Snowflake! The library automatically detects which database you're using based on the connection type.

## PostgreSQL Example

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// Create PostgreSQL connection pools
readPool, _ := pgxpool.New(context.Background(), "postgres://...")
rwPool, _ := pgxpool.New(context.Background(), "postgres://...")

// Create cache - library detects PostgreSQL from *pgxpool.Pool type
cache, err := dbcache.CreateCache[MyStruct](
    logger,
    "SELECT id, name FROM users",
    []string{"users"},     // monitored tables
    "ID",                  // key field
    time.Second * 30,      // refresh interval
    readPool,              // read connection
    rwPool,                // read-write connection (for triggers)
)
```

## Snowflake Example

```go
import (
    "database/sql"
    dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// Create Snowflake connection
snowflakeDB, _ := sql.Open("snowflake", "user:password@account/database/schema")

// Create cache - library detects Snowflake from *sql.DB type
cache, err := dbcache.CreateCache[MyStruct](
    logger,
    `SELECT ID AS "id", NAME AS "name" FROM MY_DATABASE.MY_SCHEMA.USERS`,
    []string{"USERS"},     // monitored tables
    "ID",                  // key field
    time.Second * 60,      // refresh interval
    snowflakeDB,           // Snowflake connection
    "MY_DATABASE.MY_SCHEMA", // For Snowflake: specify database.schema
)
```

## Key Differences

### Connection Type
- **PostgreSQL**: Pass `*pgxpool.Pool` as the DB parameter
- **Snowflake**: Pass `*sql.DB` as the DB parameter

### Second Parameter (DB_RW)
- **PostgreSQL**: Pass a second `*pgxpool.Pool` for trigger creation (can be same as read pool)
- **Snowflake**: Pass a string in `"DATABASE.SCHEMA"` format to specify where TABLE_LOG is located

### SQL Naming
- **PostgreSQL**: Uses lowercase names by default (`select id, name from users`)
- **Snowflake**: Uses uppercase names by default, with quoted aliases for struct mapping (`SELECT ID AS "id", NAME AS "name" FROM USERS`)

## Cache Usage (Identical for Both!)

Once created, the cache API is identical for both databases:

```go
// Get by key
result := cache.Get("someKey")

// Get all cached items
allItems := cache.GetAll()

// Force refresh
err := cache.ForceRefresh()
```

## Running the Example

```bash
# PostgreSQL example
DB_BACKEND=postgres go run main.go

# Snowflake example
DB_BACKEND=snowflake go run main.go
```

## Prerequisites

### PostgreSQL
1. Running PostgreSQL instance
2. Create the cache infrastructure:
   ```go
   import dbcache "github.com/transactrx/db-cache/pkg/db-cache"
   err := dbcache.CreateDbTriggersAndTables(rwPool)
   ```

### Snowflake
1. Snowflake account with appropriate credentials
2. Create TABLE_LOG manually:
   ```sql
   CREATE TABLE IF NOT EXISTS MY_SCHEMA.TABLE_LOG (
       ID INTEGER AUTOINCREMENT,
       TABLE_NAME VARCHAR(255) NOT NULL,
       OPERATION_TIME TIMESTAMP DEFAULT CURRENT_TIMESTAMP(),
       OPERATION_TYPE VARCHAR(10) DEFAULT 'UPDATE'
   );
   ```

## See Also

- [PostgreSQL Integration Tests](../../integration-tests/postgres/)
- [Snowflake Integration Tests](../../integration-tests/snowflake/)
- [Main README](../../README.md)

