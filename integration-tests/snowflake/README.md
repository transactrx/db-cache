# Snowflake Integration Tests for DB Cache Library

This directory contains integration tests for the db-cache library using a real Snowflake database connection.

## Prerequisites

- Go 1.24 or later
- Go modules enabled
- Snowflake account with appropriate permissions
- Snowflake Go driver (`github.com/snowflakedb/gosnowflake`)

## Setup

### 1. Snowflake Database Setup

Since Snowflake doesn't offer a local Docker container, you'll need to:

1. **Create a Snowflake account** (if you don't have one)
2. **Set up a test database** with the required schema
3. **Configure environment variables** for connection

### 2. Environment Variables

Set the following environment variables:

```bash
export SNOWFLAKE_ACCOUNT=your-account
export SNOWFLAKE_USER=your-username
export SNOWFLAKE_PASSWORD=your-password
export SNOWFLAKE_DATABASE=your-database
export SNOWFLAKE_SCHEMA=your-schema
export SNOWFLAKE_WAREHOUSE=your-warehouse
```

**Note**: If `SNOWFLAKE_DATABASE`, `SNOWFLAKE_SCHEMA`, or `SNOWFLAKE_WAREHOUSE` are not set, defaults will be used:
- Database: `TESTDB`
- Schema: `PUBLIC`
- Warehouse: `COMPUTE_WH`

### 3. Database Schema Setup

Run the SQL scripts in the `init/` directory to set up your Snowflake database:

1. **Create schema and tables**:
   ```sql
   -- Run init/01-create-schema.sql
   ```

2. **Insert sample data**:
   ```sql
   -- Run init/02-sample-data.sql
   ```

## Running the Tests

### 1. Install Dependencies

```bash
cd integration-tests/snowflake
go mod tidy
```

### 2. Run Tests

```bash
# Run all Snowflake integration tests
go test -v

# Run specific test
go test -v -run TestSnowflakeCacheIntegration

# Run with detailed output
go test -v -run TestSnowflakeCacheIntegration -args -test.v
```

### 3. Skip Snowflake Tests (Optional)

If you want to skip Snowflake tests in CI or when Snowflake is not available:

```bash
SKIP_SNOWFLAKE_TESTS=true go test
```

## Test Structure

The integration tests cover:

### 1. Basic Cache Operations
- `GetAll()` - Retrieves all cached records
- `Get(key)` - Retrieves records by key
- `ForceRefresh()` - Manually refreshes the cache

### 2. Auto-Refresh Behavior
- Tests that the cache automatically picks up database changes
- Verifies cache invalidation works correctly

### 3. Error Handling
- Tests behavior with invalid SQL queries
- Tests behavior with invalid key fields

### 4. Multiple Cache Types
- Tests with different data models (APIKey, User)
- Tests with different key fields and SQL queries

## Database Schema

The test database includes:

### Tables
- `API_KEYS` - Test table for API key caching
- `USERS` - Test table for user caching
- `CACHE.TABLE_LOG` - Monitoring table for cache invalidation

### Functions
- `LOG_TABLE_CHANGE()` - Stored procedure for monitoring changes

### Sample Data
- 4 API keys (3 active, 1 inactive)
- 4 users with different roles

## Troubleshooting

### Connection Issues
```bash
# Test connection manually
go run -c "package main; import _ \"github.com/snowflakedb/gosnowflake\"; func main() {}"
```

### Permission Issues
Ensure your Snowflake user has:
- `CREATE TABLE` permission
- `INSERT` permission
- `SELECT` permission
- `DELETE` permission (for cleanup)

### Schema Issues
```bash
# Check if tables exist
SELECT * FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = 'PUBLIC';
```

## Cost Considerations

**Important**: These tests connect to a real Snowflake instance and may incur costs:
- **Compute costs** for running queries
- **Storage costs** for test data
- **Warehouse costs** for compute resources

To minimize costs:
- Use a small warehouse for testing
- Clean up test data after tests
- Consider using a dedicated test account

## Adding New Tests

1. Add new test functions to `integration_test.go`
2. Follow the naming convention: `TestSnowflakeCacheIntegration/TestName`
3. Use `require.NoError()` for setup and `assert.*` for validations
4. Clean up any test data you create
5. Add documentation for new test scenarios

## Performance Considerations

- Tests use a 2-second cache refresh interval for responsiveness
- Database connection is managed by the Go driver
- Tests include cleanup to prevent data accumulation
- Consider using `t.Parallel()` for independent tests if needed

## Security Notes

- Never commit Snowflake credentials to version control
- Use environment variables or secure credential management
- Consider using Snowflake's key pair authentication for production
- Rotate credentials regularly

