# Snowflake Integration Tests for DB Cache Library

This directory contains integration tests for the db-cache library using a real Snowflake database connection.

## Prerequisites

- Go 1.24 or later
- Go modules enabled
- Snowflake account with appropriate permissions
- Snowflake Go driver (`github.com/snowflakedb/gosnowflake`)

## Setup

### 1. Snowflake Database Setup

Since Snowflake doesn't offer a local Docker container, you'll need access to a real Snowflake instance. The tests will automatically create the necessary schema and tables.

**Required Snowflake Permissions:**
- `USAGE` on the target database
- `USAGE` on the target schema (or ability to create it)
- `CREATE TABLE` on the schema
- `INSERT`, `SELECT`, `DELETE` on tables (for test operations)

### 2. Environment Variables

Create a `.env` file in the project root of test folder or set these environment variables:

```bash
# Required
export SNOWFLAKE_ACCOUNT=your-account
export SNOWFLAKE_USER=your-username
export SNOWFLAKE_DATABASE=your-database
export SNOWFLAKE_SCHEMA=your-schema      # e.g., CACHE_DEV

# Authentication
export SNOWFLAKE_PRIVATE_KEY="xxgddteyyagagghrruwwosis"

# Optional
export SNOWFLAKE_WAREHOUSE=your-warehouse  # Default: COMPUTE_WH
export SNOWFLAKE_ROLE=your-role           # If you need to specify a role
```

**Example `.env` file:**
```bash
SNOWFLAKE_ACCOUNT=abc12345.us-east-1
SNOWFLAKE_USER=SA_BATCH_RW_DEV
SNOWFLAKE_DATABASE=CPE_DEV
SNOWFLAKE_SCHEMA=CACHE_DEV
SNOWFLAKE_WAREHOUSE=COMPUTE_WH
SNOWFLAKE_ROLE=BATCHJOB_RW_DEV
SNOWFLAKE_PRIVATE_KEY="LS0tLS1CRUdJTi..."  # Base64 encoded or PEM format
```

### 3. Schema and Data Setup

**No manual setup required!** The tests automatically create all necessary objects:

The `setupSnowflakeSchemaAndData()` function in `integration_test.go` automatically creates:

> **Note:** If you want to test automatic stream registration, you need to create the `REGISTERCACHETABLE` stored procedure in your `CACHE_DEV` schema and set `DB_CACHE_SF_REGISTER_STREAMS=true`. Otherwise, tests will manually update `TABLE_LOG`.

1. **TABLE_LOG** - For tracking table changes (cache invalidation)
   ```sql
   CREATE TABLE IF NOT EXISTS {DATABASE}.{SCHEMA}.TABLE_LOG (
       ID INTEGER AUTOINCREMENT,
       TABLE_NAME VARCHAR(255) NOT NULL,
       OPERATION_TIME TIMESTAMP DEFAULT CURRENT_TIMESTAMP(),
       OPERATION_TYPE VARCHAR(10) DEFAULT 'UPDATE'
   );
   ```

2. **API_KEYS** - Test table for API key caching
   ```sql
   CREATE TABLE IF NOT EXISTS {DATABASE}.{SCHEMA}.API_KEYS (
       ID INTEGER AUTOINCREMENT,
       KEY VARCHAR(255) UNIQUE NOT NULL,
       NAME VARCHAR(255) NOT NULL,
       IS_ACTIVE BOOLEAN DEFAULT TRUE,
       CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
   );
   ```

3. **USERS** - Test table for user caching
   ```sql
   CREATE TABLE IF NOT EXISTS {DATABASE}.{SCHEMA}.USERS (
       ID INTEGER AUTOINCREMENT,
       USERNAME VARCHAR(255) UNIQUE NOT NULL,
       EMAIL VARCHAR(255) NOT NULL,
       ROLE VARCHAR(50) DEFAULT 'user',
       CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
   );
   ```

4. **Sample Data** - Test data is inserted idempotently
   - 4 API keys (3 active, 1 inactive)
   - 4 users with different roles

All setup is **idempotent** - you can run tests multiple times without conflicts.

## Running the Tests

### Quick start (recommended)

Use the existing test runner. It will run all Snowflake integration tests and, when enabled, will validate that the Go cache code performs stream registration by calling your stored procedure under the hood.

```bash
cd integration-tests/snowflake
export DB_CACHE_SF_REGISTER_STREAMS=true   # Opt-in: Go code will call REGISTERCACHETABLE
./run_tests.sh
```

### Using the Shell Script (Full Test Suite)

```bash
cd integration-tests/snowflake
./run_tests.sh
```

The script will:
- Check for required environment variables
- Install dependencies
- Run all integration tests
- Report results

### Manual Test Execution

```bash
cd integration-tests/snowflake

# Install dependencies
go mod tidy

# Run all tests
go test -v

# Run specific test
go test -v -run TestSnowflakeCacheIntegration

# Run with count to disable caching
go test -v -count=1
```

### Run All Integration Tests (PostgreSQL + Snowflake)

```bash
cd integration-tests
./run_all_tests.sh
```

## Test Structure

The integration tests cover:

### 1. APIKey Cache Operations
- `GetAll()` - Retrieves all active API keys
- `Get(key)` - Retrieves specific API key by key field
- `ForceRefresh()` - Manually refreshes the cache

### 2. User Cache Operations
- Tests with a different data model (User vs APIKey)
- Tests with different key field (USERNAME vs KEY)
- Validates cache works with multiple types

### 3. Cache Auto-Refresh Behavior
- Inserts new data into Snowflake
- **Manually logs change in TABLE_LOG** (Snowflake doesn't support automatic triggers)
- Waits for automatic cache refresh
- Verifies cache picks up the new data

### 4. Error Handling
- Tests with invalid SQL queries (non-existent table)
- Tests with invalid key fields
- Verifies appropriate error messages

## Important: TABLE_LOG Updates in Snowflake

**The library can automatically register Snowflake Streams** when you create a cache! 🎉

### Automatic Stream Registration (Opt-in)

When you enable stream registration by setting `DB_CACHE_SF_REGISTER_STREAMS=true`, the library will:

1. **Call REGISTERCACHETABLE** procedure for each monitored table
2. **The procedure creates a STREAM** for the table (tracks INSERT/UPDATE/DELETE)
3. **Registers the stream** in your cache registry
4. **Your heartbeat Task** can then query these streams and update TABLE_LOG

This provides **PostgreSQL-style automatic cache invalidation** for Snowflake!

> **Prerequisites:** You must create the `REGISTERCACHETABLE` stored procedure in your cache schema (e.g., `CACHE_DEV`) before enabling this feature.

### Example (Go performs registration)
```bash
# Enable automatic stream registration
export DB_CACHE_SF_REGISTER_STREAMS=true
```

```go
// Create the cache - Go will call REGISTERCACHETABLE for each monitored table
cache, err := dbcache.CreateCache[MyType](
    logger,
    "SELECT ... FROM API_KEYS",
    []string{"API_KEYS"},      // REGISTERCACHETABLE called for API_KEYS
    "ID",
    time.Second * 60,
    snowflakeDB,
    "MY_DB.CACHE_DEV",         // Procedure called: MY_DB.CACHE_DEV.REGISTERCACHETABLE
)

// Now when data changes in API_KEYS:
// 1. Stream detects the change (created by REGISTERCACHETABLE)
// 2. Your heartbeat Task writes to TABLE_LOG
// 3. Cache auto-refreshes within check interval!
```

### Procedure Signature and Privileges

Your procedure must have this signature and be executable by the test role:

```sql
-- Signature (case-sensitive name)
CREATE OR REPLACE PROCEDURE CPE_DEV.CACHE_DEV."REGISTERCACHETABLE"(
  DB_NAME VARCHAR, SCHEMA_NAME VARCHAR, TABLE_NAME VARCHAR
) RETURNS VARCHAR LANGUAGE JAVASCRIPT;

-- Minimum privilege required by the test role (example role shown)
GRANT USAGE ON PROCEDURE CPE_DEV.CACHE_DEV."REGISTERCACHETABLE"(VARCHAR, VARCHAR, VARCHAR)
TO ROLE BATCHJOB_RW_DEV;
```

### Fallback Behavior

If the REGISTERCACHETABLE call initiated by the Go code fails (e.g., procedure doesn't exist, insufficient privileges), the cache will:
- **Still work** - All cache operations function normally
- **Log a warning** - You'll know stream registration failed
- **Allow manual refresh** - You can call `cache.ForceRefresh()` or manually update `TABLE_LOG`

### Requirements for Automatic Stream Registration

Your Snowflake setup needs:
- The `REGISTERCACHETABLE` stored procedure created in your cache schema
- `EXECUTE` privilege on the procedure
- Procedure has permissions to `CREATE STREAM` on monitored tables
- Appropriate role assignment

If these aren't available, cache creation will succeed but you'll get a warning. Cache will still work - you'll just need to call `ForceRefresh()` manually or manually insert into TABLE_LOG.

### Manual Approach (Still Supported)

If you prefer manual control, you can still insert into TABLE_LOG yourself:

```go
// Modify data
db.Exec("INSERT INTO API_KEYS ...")

// Manually log the change
db.Exec("INSERT INTO TABLE_LOG (TABLE_NAME) VALUES ('API_KEYS')")
```

## What the Tests Actually Do

1. **Connect to Snowflake** using credentials from environment
2. **Create schema and tables** if they don't exist (idempotent)
3. **Insert sample data** (or update if exists)
4. **Create cache instances** using `dbcache.CreateCache` API
5. **Test cache operations** (Get, GetAll, ForceRefresh)
6. **Simulate data changes** and verify auto-refresh
7. **Test error scenarios**
8. **Clean up test data** automatically

## Troubleshooting

### Connection Issues

**Error: Cannot connect to Snowflake**
- Verify `SNOWFLAKE_ACCOUNT` is correct (include region if needed)
- Check network connectivity
- Verify credentials are correct

**Error: Private key authentication failed**
- Ensure private key is in PKCS8 format
- Check key encoding (base64 or PEM with `\n` escaped as `\\n`)
- Verify key matches the public key registered in Snowflake

### Permission Issues

**Error: SQL access control error**
```bash
# Grant necessary permissions in Snowflake:
GRANT USAGE ON DATABASE {DATABASE} TO ROLE {ROLE};
GRANT USAGE ON SCHEMA {DATABASE}.{SCHEMA} TO ROLE {ROLE};
GRANT CREATE TABLE ON SCHEMA {DATABASE}.{SCHEMA} TO ROLE {ROLE};
GRANT INSERT, SELECT, DELETE ON ALL TABLES IN SCHEMA {DATABASE}.{SCHEMA} TO ROLE {ROLE};
GRANT INSERT, SELECT, DELETE ON FUTURE TABLES IN SCHEMA {DATABASE}.{SCHEMA} TO ROLE {ROLE};
GRANT ROLE {ROLE} TO USER {USER};
```

**Error: Schema does not exist**
- Either create the schema manually, or grant `CREATE SCHEMA` permission
- Tests use the schema specified in `SNOWFLAKE_SCHEMA` environment variable

### Test Failures

**Cache not refreshing**
- Check that TABLE_LOG is being updated
- Verify the refresh interval (1-2 seconds in tests)
- Look for errors in test output

**Data not found**
- Verify sample data was inserted successfully
- Check for SQL errors during setup
- Ensure correct database/schema context

## Cost Considerations

**Important**: These tests connect to a real Snowflake instance and may incur costs:
- **Compute costs** for query execution
- **Storage costs** for test data (minimal)
- **Warehouse costs** based on warehouse size

To minimize costs:
- Use X-Small warehouse for testing
- Tests clean up automatically, minimizing storage
- Consider running tests only when needed (not on every commit)
- Use a dedicated dev/test Snowflake account

## Security Notes

- ✅ `.env` files are excluded by `.gitignore`
- ✅ `private*` files are excluded by `.gitignore`
- ✅ Never commit Snowflake credentials to version control
- ✅ Use key-pair authentication for production
- ✅ Rotate credentials regularly
- ✅ Use role-based access control in Snowflake

## Adding New Tests

1. Add test functions to `integration_test.go` as sub-tests
2. Use the pattern: `t.Run("Test Name", func(t *testing.T) { ... })`
3. Use `require.NoError()` for setup assertions
4. Use `assert.*` for test validations
5. Clean up any additional test data you create
6. Update this README with new test scenarios

## Performance Considerations

- Tests use short refresh intervals (1-2 seconds) for quick validation
- Parallel tests are not used to avoid Snowflake connection limits
- Sample data is minimal to reduce load
- Connection pooling is handled by the Go Snowflake driver

## Differences from PostgreSQL Tests

| Aspect | PostgreSQL | Snowflake |
|--------|-----------|-----------|
| Connection | `*pgxpool.Pool` | `*sql.DB` |
| Setup | Docker container | Real Snowflake instance |
| Schema creation | Automatic via triggers | Manual grants + auto-setup |
| Triggers | Supported | Not supported (manual TABLE_LOG updates) |
| Cost | Free | May incur charges |
| Speed | Fast (local) | Slower (network) |

Both use the **same `dbcache.CreateCache` API**! 🎉
