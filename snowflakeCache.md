## Snowflake Cache — Persistent Change Signal Design

### Purpose

This document describes a production‑ready, persistent, and efficient design to implement the same cache‑invalidation semantics that `db-cache` uses on PostgreSQL, but on Snowflake (which does not support traditional triggers).


### Read me first — how everything works at a glance

- **Will the program create the centralized Task?**
  - Recommended: provision the Task once per environment via infra/DBA (IaC/SQL), since it needs a Warehouse and owner privileges. The application typically does not create this shared Task by default. If you prefer, you can allow a bootstrap step to create it, but treat it as an ops responsibility.

- **Does the Task run the moment changes occur?**
  - No. Snowflake Tasks run on a schedule (interval/cron). On each run, the Task checks Streams for changes and writes a heartbeat into `DB_CACHE_LOG`. For near real‑time behavior, use a short schedule (e.g., every 1 minute) and lightweight statements. If you need sub‑minute responsiveness, move the heartbeat into the application process (see Alternatives) or run a tighter schedule subject to account limits and cost.

- **Will the program create Streams for monitored tables?**
  - Yes, safely if you choose. Call the provided registration procedure `DB_CACHE_REGISTER_TABLE(schema, table)` from a bootstrap step; it creates the Stream (idempotent) and records it in `DB_CACHE_REGISTRY`. Alternatively, pre‑provision Streams centrally and just register them.

- **What’s the difference between `DB_CACHE_REGISTRY` and `DB_CACHE_LOG`?**
  - `DB_CACHE_REGISTRY`: configuration of what to monitor (schema/table → stream name, enabled flag). Think of it as the “watchlist.”
  - `DB_CACHE_LOG`: durable change signal used by applications. For each table, it stores the last heartbeat time and an incrementing `change_seq`. Apps only read this table to detect staleness (just like Postgres `table_log`).

- **What do applications do?**
  - Apps never touch Streams. They periodically read `DB_CACHE_LOG` to compute a fingerprint; if it changed since the last poll, they reload the in‑memory cache.

- **Who updates `DB_CACHE_LOG`?**
  - The centralized Task runs the heartbeat procedure. It reads Streams, and when any have new data, it upserts a heartbeat for those tables into `DB_CACHE_LOG` and advances the Streams’ offsets.


### Goals and requirements

- Persistent, auditable invalidation signal (like Postgres `table_log`).
- Multi‑consumer safe: many services can read the signal without interfering.
- Efficient: low overhead for detection; full reloads remain application‑side.
- Minimal privileges; avoid modifying data tables or adding columns.
- Resilient to outages and retention windows (Time Travel).


### High‑level approach

We introduce a durable signal table `DB_CACHE_LOG` in Snowflake. A lightweight, centralized Snowflake Task reads Streams on monitored tables and writes a heartbeat for each table into `DB_CACHE_LOG` whenever changes occur. Application caches only read `DB_CACHE_LOG` to detect staleness—just like they read `table_log` in Postgres.

```
┌────────────────────────┐     poll      ┌─────────────────────┐
│  App cache instances   │──────────────▶│  DB_CACHE_LOG table │
│  (no write privileges) │               │  (persistent state) │
└──────────▲─────────────┘               └──────────▲──────────┘
           │                                     write on change
           │                                           │
           │                                   ┌───────┴────────┐
           │                                   │ Snowflake Task │
           │                                   │  (heartbeat)   │
           │                                   └───────▲────────┘
           │                                           │
           │                                   read/consume Streams
           │                                           │
           │                                   ┌───────┴────────┐
           └──────────────────────────────────▶│  Streams (CDC) │
                                               └───────▲────────┘
                                                       │
                                               ┌───────┴────────┐
                                               │ Monitored tbls │
                                               └────────────────┘
```

- Streams provide cheap, native change detection.
- The Task is the single consumer of Streams and persists changes into `DB_CACHE_LOG`.
- Apps never touch Streams; they only read the persistent log → multi‑consumer safe and simple.


### Objects

1) `DB_CACHE_REGISTRY` (which tables to monitor)

```sql
CREATE TABLE IF NOT EXISTS <DB>.<SCHEMA>.DB_CACHE_REGISTRY (
    schema_name           STRING   NOT NULL,
    table_name            STRING   NOT NULL,
    stream_name           STRING   NOT NULL,
    enabled               BOOLEAN  DEFAULT TRUE,
    registered_at         TIMESTAMP_LTZ DEFAULT CURRENT_TIMESTAMP(),
    PRIMARY KEY (schema_name, table_name)
);
```

2) `DB_CACHE_LOG` (persistent change signal)

```sql
CREATE TABLE IF NOT EXISTS <DB>.<SCHEMA>.DB_CACHE_LOG (
    schema_name           STRING   NOT NULL,
    table_name            STRING   NOT NULL,
    operation_time        TIMESTAMP_LTZ NOT NULL,
    change_seq            NUMBER   NOT NULL DEFAULT 0,
    PRIMARY KEY (schema_name, table_name)
);
```

3) One Stream per monitored table (shared, centralized)

```sql
-- Deterministic stream naming: DB_CACHE_<TABLE_NAME>
CREATE STREAM IF NOT EXISTS <DB>.<SCHEMA>.DB_CACHE_<TABLE_NAME>
ON TABLE <DB>.<SCHEMA>.<TABLE_NAME>;
```

4) Registration helper (optional stored procedure)

```sql
-- Register a table for monitoring: create stream, upsert registry & log row
CREATE OR REPLACE PROCEDURE <DB>.<SCHEMA>.DB_CACHE_REGISTER_TABLE(
    P_SCHEMA STRING,
    P_TABLE  STRING
)
RETURNS STRING
LANGUAGE SQL
EXECUTE AS OWNER
AS
$$
BEGIN
    LET V_STREAM STRING := 'DB_CACHE_' || UPPER(P_TABLE);

    EXECUTE IMMEDIATE 'CREATE STREAM IF NOT EXISTS ' || IDENTIFIER(:P_SCHEMA) || '.' || IDENTIFIER(:V_STREAM) ||
                      ' ON TABLE ' || IDENTIFIER(:P_SCHEMA) || '.' || IDENTIFIER(:P_TABLE);

    MERGE INTO DB_CACHE_REGISTRY r
    USING (SELECT :P_SCHEMA AS schema_name, :P_TABLE AS table_name, :V_STREAM AS stream_name) s
    ON (r.schema_name = s.schema_name AND r.table_name = s.table_name)
    WHEN MATCHED THEN UPDATE SET stream_name = s.stream_name, enabled = TRUE
    WHEN NOT MATCHED THEN INSERT (schema_name, table_name, stream_name, enabled) VALUES (s.schema_name, s.table_name, s.stream_name, TRUE);

    MERGE INTO DB_CACHE_LOG l
    USING (SELECT :P_SCHEMA AS schema_name, :P_TABLE AS table_name) s
    ON (l.schema_name = s.schema_name AND l.table_name = s.table_name)
    WHEN MATCHED THEN UPDATE SET operation_time = COALESCE(l.operation_time, CURRENT_TIMESTAMP())
    WHEN NOT MATCHED THEN INSERT (schema_name, table_name, operation_time, change_seq) VALUES (s.schema_name, s.table_name, CURRENT_TIMESTAMP(), 0);

    RETURN 'REGISTERED ' || :P_SCHEMA || '.' || :P_TABLE || ' USING STREAM ' || :V_STREAM;
END;
$$;
```

5) Heartbeat procedure (consumes Streams, updates `DB_CACHE_LOG`)

```sql
CREATE OR REPLACE PROCEDURE <DB>.<SCHEMA>.DB_CACHE_HEARTBEAT()
RETURNS STRING
LANGUAGE JAVASCRIPT
EXECUTE AS OWNER
AS
$$
var updated = 0;

// Pull tables to monitor
var rs = snowflake.createStatement({
  sqlText: `SELECT schema_name, table_name, stream_name
            FROM ${snowflake.getCurrentDatabase()}.${snowflake.getCurrentSchema()}.DB_CACHE_REGISTRY
            WHERE enabled`
}).execute();

while (rs.next()) {
  var schema = rs.getColumnValue(1);
  var table  = rs.getColumnValue(2);
  var stream = rs.getColumnValue(3);
  var qname  = `${schema}.${stream}`;

  // Check if stream has data
  var has = snowflake.createStatement({
    sqlText: `SELECT SYSTEM$STREAM_HAS_DATA(?)`,
    binds: [qname]
  }).execute();
  has.next();
  var hasData = ('' + has.getColumnValue(1)).toLowerCase() === 'true';

  // If stream is stale or invalid, recreate it (best effort)
  var show = snowflake.createStatement({
    sqlText: `SHOW STREAMS LIKE ? IN SCHEMA ` + schema,
    binds: [stream]
  }).execute();
  var stale = false;
  if (show.next()) {
    // The 8th column in SHOW STREAMS is STALE (may vary by account version); use name lookup for safety
    // Safer approach: query INFORMATION_SCHEMA.STREAMS() if available
    try {
      stale = ('' + show.getColumnValue('stale')).toLowerCase() === 'true';
    } catch (e) { stale = false; }
  }

  if (stale) {
    // Recreate stream and force a heartbeat so readers know baseline shifted
    snowflake.createStatement({
      sqlText: `CREATE OR REPLACE STREAM ` + qname + ` ON TABLE ` + schema + `.` + table
    }).execute();
    snowflake.createStatement({
      sqlText: `MERGE INTO ${snowflake.getCurrentDatabase()}.${snowflake.getCurrentSchema()}.DB_CACHE_LOG l
                USING (SELECT ? AS schema_name, ? AS table_name) s
                ON (l.schema_name = s.schema_name AND l.table_name = s.table_name)
                WHEN MATCHED THEN UPDATE SET operation_time=CURRENT_TIMESTAMP(), change_seq=l.change_seq+1
                WHEN NOT MATCHED THEN INSERT(schema_name, table_name, operation_time, change_seq) VALUES(s.schema_name, s.table_name, CURRENT_TIMESTAMP(), 1)`,
      binds: [schema, table]
    }).execute();
    updated++;
    continue;
  }

  if (hasData) {
    // Persist change signal
    snowflake.createStatement({
      sqlText: `MERGE INTO ${snowflake.getCurrentDatabase()}.${snowflake.getCurrentSchema()}.DB_CACHE_LOG l
                USING (SELECT ? AS schema_name, ? AS table_name) s
                ON (l.schema_name = s.schema_name AND l.table_name = s.table_name)
                WHEN MATCHED THEN UPDATE SET operation_time=CURRENT_TIMESTAMP(), change_seq=l.change_seq+1
                WHEN NOT MATCHED THEN INSERT(schema_name, table_name, operation_time, change_seq) VALUES(s.schema_name, s.table_name, CURRENT_TIMESTAMP(), 1)`,
      binds: [schema, table]
    }).execute();

    // Advance stream offset (consume changes)
    snowflake.createStatement({
      sqlText: `SELECT COUNT(*) FROM ` + qname
    }).execute();

    updated++;
  }
}

return `UPDATED ${updated} TABLE(S)`;
$$;
```

6) Task to run the heartbeat

```sql
CREATE OR REPLACE TASK <DB>.<SCHEMA>.DB_CACHE_HEARTBEAT_TASK
WAREHOUSE = <WAREHOUSE_NAME>
SCHEDULE = '1 MINUTE'
AS CALL <DB>.<SCHEMA>.DB_CACHE_HEARTBEAT();

-- Start the task
ALTER TASK <DB>.<SCHEMA>.DB_CACHE_HEARTBEAT_TASK RESUME;
```


### Application integration

Your application cache does not need to read Streams. It should only read from `DB_CACHE_LOG` to compute a staleness fingerprint, exactly like the Postgres version.

Single table fingerprint:

```sql
SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01')))
  AS ct
FROM <DB>.<SCHEMA>.DB_CACHE_LOG
WHERE schema_name = ? AND table_name = ?;
```

Multiple tables fingerprint:

```sql
SELECT LISTAGG(ct, ', ')
FROM (
  SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct
  FROM <DB>.<SCHEMA>.DB_CACHE_LOG
  WHERE (schema_name, table_name) IN ((?, ?), (?, ?), ...)
  GROUP BY schema_name, table_name
);
```

Algorithm in the app (unchanged):

1) Poll fingerprint at a fixed interval.
2) If fingerprint differs from last seen value → execute your load SQL and rebuild the in‑memory map.
3) Otherwise skip reload.


### Setup steps (once per environment)

1) Choose a database/schema to host the cache objects (e.g., `UTILS.PUBLIC`).
2) Create `DB_CACHE_REGISTRY` and `DB_CACHE_LOG`.
3) Deploy procedures `DB_CACHE_REGISTER_TABLE` and `DB_CACHE_HEARTBEAT`.
4) Create the Task `DB_CACHE_HEARTBEAT_TASK` and resume it (ensure a warehouse is available).
5) For each table to monitor, call:

```sql
CALL <DB>.<SCHEMA>.DB_CACHE_REGISTER_TABLE('<SCHEMA_NAME>', '<TABLE_NAME>');
```

6) Grant SELECT on `DB_CACHE_LOG` to application roles; deny write.


### Operations and resilience

- Streams rely on table Time Travel retention. Set retention to a safe value (e.g., 2–3 days) for monitored tables.
  ```sql
  ALTER TABLE <DB>.<SCHEMA>.<TABLE> SET DATA_RETENTION_TIME_IN_DAYS = 3;
  ```
- If a stream becomes stale (Task paused beyond retention), the heartbeat will recreate it and still bump `DB_CACHE_LOG` to signal a change.
- The Task is the only consumer of Streams → no offset interference between applications.
- Costs are minimal: stream checks and `COUNT(*)` on Streams are lightweight; the Task uses a small warehouse on schedule.


### Security

- Procedures and Task run `EXECUTE AS OWNER`—use a dedicated, least‑privilege role.
- Applications need only `SELECT` on `DB_CACHE_LOG` and no access to Streams.
- Grant `MONITOR` privileges as needed to debug.


### Alternatives (when Tasks are not allowed)

If you cannot run a Snowflake Task, you can move the heartbeat logic into the application:

- The app (writer role) checks `SYSTEM$STREAM_HAS_DATA(stream)` and, if true, updates `DB_CACHE_LOG` and consumes the stream via `SELECT COUNT(*) FROM stream`.
- Other readers still look only at `DB_CACHE_LOG`.

Trade‑off: the app becomes a consumer; if multiple apps do this, they must coordinate to avoid offset races. The Task‑based design is preferred.


### FAQ

- Why not use Change Tracking instead of Streams?
  - Change Tracking is not a consumer offset; it helps Snowflake optimize scans. Streams give reliable, cheap CDC semantics for invalidation, which we persist in `DB_CACHE_LOG`.

- What happens if the Task is down for a long time?
  - Streams can become stale after the retention window. The heartbeat procedure recreates them and bumps `DB_CACHE_LOG`, ensuring caches perform a full reload.

- Can multiple environments share the same objects?
  - Prefer separate schemas per environment to isolate signal tables and tasks.


### Summary

This design mirrors Postgres `table_log` semantics on Snowflake by centralizing CDC into a durable `DB_CACHE_LOG` table maintained by a heartbeat Task that consumes Streams. Applications remain simple and safe: they poll a persistent log, trigger full reloads when the fingerprint changes, and never interact with Streams directly.


