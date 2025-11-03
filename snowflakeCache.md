## Snowflake Cache — Local change signal (CACHE.TABLE_LOG)

### Purpose

This document describes the Snowflake design that mirrors Postgres semantics as closely as possible: a fixed local change‑signal table (like Postgres `table_log`), polled by the cache to detect staleness. The cache never writes to this table; a lightweight heartbeat process maintains it.

### How it works

- Change signal lives in a fixed table: `CACHE.TABLE_LOG` with the columns:
  - `table_name` STRING PRIMARY KEY
  - `operation_time` TIMESTAMP_LTZ DEFAULT CURRENT_TIMESTAMP
- On each INSERT/UPDATE/DELETE to a monitored table, the heartbeat process upserts a row for that table into `CACHE.TABLE_LOG`, updating `operation_time`.
- The application cache polls `CACHE.TABLE_LOG` and computes a fingerprint per table: `COUNT(*) || TO_VARCHAR(MAX(operation_time))`. If the fingerprint changes, it reloads.
- The cache API is the same as Postgres: `Get`, `GetAll`, `ForceRefresh`.

### Why a heartbeat process?

Snowflake does not use traditional table triggers for DML in the same way as Postgres. The recommended pattern is:
- Create a Stream per table to capture changes
- A scheduled Task runs a small procedure (the heartbeat) to detect new changes via Streams and upsert into `CACHE.TABLE_LOG`
- Apps only read `CACHE.TABLE_LOG`

This keeps the cache simple and symmetric with Postgres while using Snowflake‑native CDC.

### Objects to create (once per environment)

1) Schema and change signal table

```sql
CREATE SCHEMA IF NOT EXISTS CACHE;

CREATE TABLE IF NOT EXISTS CACHE.TABLE_LOG (
  table_name     STRING PRIMARY KEY,
  operation_time TIMESTAMP_LTZ DEFAULT CURRENT_TIMESTAMP
);
```

2) Registry of monitored tables (schema + name) and their Streams

```sql
CREATE TABLE IF NOT EXISTS CACHE.REGISTRY (
  schema_name  STRING NOT NULL,
  table_name   STRING NOT NULL,
  stream_name  STRING NOT NULL,
  enabled      BOOLEAN DEFAULT TRUE,
  PRIMARY KEY (schema_name, table_name)
);
```

3) Register a table (creates a Stream and adds it to the registry)

```sql
CREATE OR REPLACE PROCEDURE CACHE.REGISTERCACHETABLE(P_SCHEMA STRING, P_TABLE STRING)
RETURNS STRING
LANGUAGE SQL
EXECUTE AS OWNER
AS
$$
BEGIN
  LET V_STREAM STRING := 'DB_CACHE_' || UPPER(P_TABLE);

  EXECUTE IMMEDIATE 'CREATE STREAM IF NOT EXISTS ' || IDENTIFIER(:P_SCHEMA) || '.' || IDENTIFIER(:V_STREAM) ||
                    ' ON TABLE ' || IDENTIFIER(:P_SCHEMA) || '.' || IDENTIFIER(:P_TABLE);

  MERGE INTO CACHE.REGISTRY r
  USING (SELECT :P_SCHEMA AS schema_name, :P_TABLE AS table_name, :V_STREAM AS stream_name) s
  ON (r.schema_name = s.schema_name AND r.table_name = s.table_name)
  WHEN MATCHED THEN UPDATE SET stream_name = s.stream_name, enabled = TRUE
  WHEN NOT MATCHED THEN INSERT (schema_name, table_name, stream_name, enabled)
                       VALUES (s.schema_name, s.table_name, s.stream_name, TRUE);

  -- Ensure a baseline row exists in CACHE.TABLE_LOG
  MERGE INTO CACHE.TABLE_LOG t
  USING (SELECT :P_TABLE AS table_name) s
  ON (t.table_name = s.table_name)
  WHEN MATCHED THEN UPDATE SET operation_time = COALESCE(t.operation_time, CURRENT_TIMESTAMP())
  WHEN NOT MATCHED THEN INSERT (table_name, operation_time) VALUES (s.table_name, CURRENT_TIMESTAMP());

  RETURN 'REGISTERED ' || :P_SCHEMA || '.' || :P_TABLE || ' USING STREAM ' || :V_STREAM;
END;
$$;
```

4) Heartbeat procedure (consumes Streams and updates `CACHE.TABLE_LOG`)

```sql
CREATE OR REPLACE PROCEDURE CACHE.HEARTBEAT()
RETURNS STRING
LANGUAGE JAVASCRIPT
EXECUTE AS OWNER
AS
$$
var updated = 0;

var db = snowflake.getCurrentDatabase();
var rs = snowflake.createStatement({
  sqlText: `SELECT schema_name, table_name, stream_name
            FROM ${db}.CACHE.REGISTRY WHERE enabled`
}).execute();

while (rs.next()) {
  var schema = rs.getColumnValue(1);
  var table  = rs.getColumnValue(2);
  var stream = rs.getColumnValue(3);
  var qname  = `${schema}.${stream}`;

  var has = snowflake.createStatement({ sqlText: `SELECT SYSTEM$STREAM_HAS_DATA(?)`, binds: [qname]}).execute();
  has.next();
  var hasData = ('' + has.getColumnValue(1)).toLowerCase() === 'true';

  if (hasData) {
    // Upsert heartbeat into CACHE.TABLE_LOG
    snowflake.createStatement({
      sqlText: `MERGE INTO ${db}.CACHE.TABLE_LOG t
                USING (SELECT ? AS table_name) s
                ON (t.table_name = s.table_name)
                WHEN MATCHED THEN UPDATE SET operation_time=CURRENT_TIMESTAMP()
                WHEN NOT MATCHED THEN INSERT(table_name, operation_time) VALUES(s.table_name, CURRENT_TIMESTAMP())`,
      binds: [table]
    }).execute();

    // Advance stream offset
    snowflake.createStatement({ sqlText: `SELECT COUNT(*) FROM ` + qname }).execute();
    updated++;
  }
}

return `UPDATED ${updated} TABLE(S)`;
$$;
```

5) Task to run the heartbeat

```sql
CREATE OR REPLACE TASK CACHE.HEARTBEAT_TASK
WAREHOUSE = <WAREHOUSE>
SCHEDULE = '1 MINUTE'
AS CALL CACHE.HEARTBEAT();

ALTER TASK CACHE.HEARTBEAT_TASK RESUME;
```

### Application integration (what your code does)

The application cache reads only `CACHE.TABLE_LOG`. It computes a fingerprint exactly like Postgres and reloads if it changes.

Single table fingerprint:

```sql
SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct
FROM CACHE.TABLE_LOG
WHERE table_name = ?;
```

Multiple tables fingerprint (library composes a union‑all + listagg string of per‑table tokens):

```sql
SELECT LISTAGG(ct, ', ')
FROM (
  SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct
  FROM CACHE.TABLE_LOG WHERE table_name = ?
  UNION ALL
  SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct
  FROM CACHE.TABLE_LOG WHERE table_name = ?
  -- … one SELECT per monitored table
) AS t;
```

### Setup steps

1) Create schema `CACHE` and table `CACHE.TABLE_LOG` (DDL above)
2) For each table to monitor, call `CACHE.REGISTER_TABLE('<SCHEMA>', '<TABLE>')`
3) Start `CACHE.HEARTBEAT_TASK` (requires a running warehouse)
4) Grant the application role read access on `CACHE.TABLE_LOG`

### Notes on connection helpers

This library intentionally does not create database connections for you:
- Postgres: use `pgxpool` (see `cmd/example/main.go` for a minimal pattern)
- Snowflake: use `database/sql` with `gosnowflake` driver; README shows a minimal example

Keeping connection setup outside the library avoids coupling to specific auth/DSN strategies, TLS, and pool configs. If you want simple helpers, we can add optional `OpenPostgresPool()` / `OpenSnowflakeDB()` utilities, but they are not required to use the cache.



