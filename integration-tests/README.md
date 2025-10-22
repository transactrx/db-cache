# Integration Tests for DB Cache Library

This directory contains comprehensive integration tests for the db-cache library, supporting both PostgreSQL and Snowflake databases.

## Structure

```
integration-tests/
├── postgres/           # PostgreSQL integration tests
│   ├── docker-compose.yml
│   ├── init/           # Database initialization scripts
│   ├── integration_test.go
│   ├── go.mod
│   ├── README.md
│   └── run_tests.sh
├── snowflake/          # Snowflake integration tests
│   ├── docker-compose.yml (placeholder)
│   ├── init/           # Database initialization scripts
│   ├── integration_test.go
│   ├── go.mod
│   ├── README.md
│   └── run_tests.sh
├── run_all_tests.sh    # Run both PostgreSQL and Snowflake tests
└── README.md           # This file
```

## Quick Start

### Run All Tests
```bash
cd integration-tests
./run_all_tests.sh
```

### Run Individual Database Tests
```bash
# PostgreSQL only
cd postgres
./run_tests.sh

# Snowflake only
cd snowflake
./run_tests.sh
```

## PostgreSQL Tests

The PostgreSQL tests use a Docker container for local testing:

- **Container**: PostgreSQL 15 Alpine
- **Port**: 5433 (to avoid conflicts)
- **Database**: testdb
- **User**: testuser
- **Password**: testpass

### Features Tested
- ✅ Basic cache operations (`Get`, `GetAll`, `ForceRefresh`)
- ✅ Auto-refresh behavior with database changes
- ✅ Multiple data types and key fields
- ✅ Error handling with invalid SQL and fields
- ✅ Cache invalidation triggers

### Running PostgreSQL Tests
```bash
cd postgres
./run_tests.sh
```

## Snowflake Tests

The Snowflake tests connect to a real Snowflake instance:

- **Requires**: Snowflake account and credentials
- **Environment Variables**: SNOWFLAKE_ACCOUNT, SNOWFLAKE_USER, SNOWFLAKE_PASSWORD
- **Optional**: SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, SNOWFLAKE_WAREHOUSE

### Features Tested
- ✅ Basic cache operations (`Get`, `GetAll`, `ForceRefresh`)
- ✅ Auto-refresh behavior with database changes
- ✅ Multiple data types and key fields
- ✅ Error handling with invalid SQL and fields
- ✅ Cache invalidation with manual logging

### Running Snowflake Tests
```bash
cd snowflake
export SNOWFLAKE_ACCOUNT=your-account
export SNOWFLAKE_USER=your-username
export SNOWFLAKE_PASSWORD=your-password
./run_tests.sh
```

### Skipping Snowflake Tests
```bash
SKIP_SNOWFLAKE_TESTS=true ./run_all_tests.sh
```

## Test Coverage

Both PostgreSQL and Snowflake tests cover:

### 1. Basic Cache Operations
- **GetAll()**: Retrieves all cached records
- **Get(key)**: Retrieves records by specific key
- **ForceRefresh()**: Manually refreshes the cache

### 2. Auto-Refresh Behavior
- Tests that cache automatically picks up database changes
- Verifies cache invalidation mechanisms work correctly
- Tests with different refresh intervals

### 3. Multiple Data Types
- **APIKey**: Tests with string key field
- **User**: Tests with different key field and data structure
- Tests with different SQL queries and filters

### 4. Error Handling
- Tests with invalid SQL queries
- Tests with invalid key field names
- Tests with missing data

## Database Schemas

### PostgreSQL Schema
- `api_keys` table with triggers for monitoring
- `users` table with triggers for monitoring
- `cache.table_log` for change tracking
- Trigger functions for automatic logging

### Snowflake Schema
- `API_KEYS` table with manual change logging
- `USERS` table with manual change logging
- `CACHE.TABLE_LOG` for change tracking
- Stored procedures for change logging

## Prerequisites

### PostgreSQL Tests
- Docker and Docker Compose
- Go 1.24 or later
- Go modules enabled

### Snowflake Tests
- Go 1.24 or later
- Go modules enabled
- Snowflake account with appropriate permissions
- Snowflake Go driver (`github.com/snowflakedb/gosnowflake`)

## Environment Variables

### PostgreSQL Tests
No environment variables required (uses Docker).

### Snowflake Tests
**Required:**
- `SNOWFLAKE_ACCOUNT`: Your Snowflake account identifier
- `SNOWFLAKE_USER`: Your Snowflake username
- `SNOWFLAKE_PASSWORD`: Your Snowflake password

**Optional:**
- `SNOWFLAKE_DATABASE`: Database name (defaults to `TESTDB`)
- `SNOWFLAKE_SCHEMA`: Schema name (defaults to `PUBLIC`)
- `SNOWFLAKE_WAREHOUSE`: Warehouse name (defaults to `COMPUTE_WH`)

**Test Control:**
- `SKIP_SNOWFLAKE_TESTS`: Set to `true` to skip Snowflake tests
- `SKIP_INTEGRATION_TESTS`: Set to `true` to skip all integration tests

## Troubleshooting

### PostgreSQL Tests
```bash
# Check container status
docker-compose ps

# View container logs
docker-compose logs postgres

# Test database connection
docker-compose exec postgres psql -U testuser -d testdb
```

### Snowflake Tests
```bash
# Test connection manually
go run -c 'package main; import _ "github.com/snowflakedb/gosnowflake"; func main() {}'

# Check environment variables
env | grep SNOWFLAKE
```

### Common Issues
1. **Port conflicts**: PostgreSQL uses port 5433 to avoid conflicts
2. **Permission issues**: Ensure Snowflake user has required permissions
3. **Network issues**: Check firewall settings for Snowflake connections
4. **Schema issues**: Verify database and schema exist in Snowflake

## Cost Considerations

### PostgreSQL Tests
- **Free**: Uses local Docker container
- **No external costs**: All testing is local

### Snowflake Tests
- **Costs**: Uses real Snowflake instance
- **Compute costs**: For running queries
- **Storage costs**: For test data
- **Warehouse costs**: For compute resources

**To minimize costs:**
- Use a small warehouse for testing
- Clean up test data after tests
- Consider using a dedicated test account

## Adding New Tests

### For PostgreSQL
1. Add test functions to `postgres/integration_test.go`
2. Follow naming convention: `TestPostgresCacheIntegration/TestName`
3. Use `require.NoError()` for setup and `assert.*` for validations
4. Clean up any test data you create

### For Snowflake
1. Add test functions to `snowflake/integration_test.go`
2. Follow naming convention: `TestSnowflakeCacheIntegration/TestName`
3. Use `require.NoError()` for setup and `assert.*` for validations
4. Clean up any test data you create

### For Both
1. Ensure tests work with both database backends
2. Add appropriate documentation
3. Update this README with new test scenarios
4. Consider adding new test data types or scenarios

## CI/CD Integration

### GitHub Actions Example
```yaml
name: Integration Tests
on: [push, pull_request]

jobs:
  postgres-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v3
        with:
          go-version: '1.24'
      - name: Run PostgreSQL tests
        run: |
          cd integration-tests/postgres
          ./run_tests.sh

  snowflake-tests:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v3
        with:
          go-version: '1.24'
      - name: Run Snowflake tests
        env:
          SNOWFLAKE_ACCOUNT: ${{ secrets.SNOWFLAKE_ACCOUNT }}
          SNOWFLAKE_USER: ${{ secrets.SNOWFLAKE_USER }}
          SNOWFLAKE_PASSWORD: ${{ secrets.SNOWFLAKE_PASSWORD }}
        run: |
          cd integration-tests/snowflake
          ./run_tests.sh
```

## Performance Considerations

- Tests use 2-second cache refresh intervals for responsiveness
- Database connections are properly managed and closed
- Tests include cleanup to prevent data accumulation
- Consider using `t.Parallel()` for independent tests if needed

## Security Notes

- Never commit database credentials to version control
- Use environment variables or secure credential management
- Consider using key pair authentication for Snowflake in production
- Rotate credentials regularly
- Use dedicated test accounts with minimal permissions

