# Integration Tests for DB Cache Library

This directory contains integration tests for the db-cache library using a real PostgreSQL database running in Docker.

## Prerequisites

- Docker and Docker Compose
- Go 1.21 or later
- Go modules enabled

## Running the Tests

### 1. Start the PostgreSQL Container

```bash
cd integration-tests
docker-compose up -d
```

This will:
- Start a PostgreSQL 15 container on port 5433
- Initialize the database with test schema and data
- Create monitoring triggers for cache invalidation

### 2. Wait for Database to be Ready

The container includes a health check, but you can also verify manually:

```bash
# Check if container is running
docker-compose ps

# Check database connectivity
docker-compose exec postgres pg_isready -U testuser -d testdb
```

### 3. Run the Integration Tests

```bash
# Install dependencies
go mod tidy

# Run all integration tests
go test -v

# Run specific test
go test -v -run TestPostgresCacheIntegration

# Run with detailed output
go test -v -run TestPostgresCacheIntegration -args -test.v
```

### 4. Skip Integration Tests (Optional)

If you want to skip integration tests in CI or when Docker is not available:

```bash
SKIP_INTEGRATION_TESTS=true go test
```

## Test Structure

The integration tests cover:

### 1. Basic Cache Operations
- `GetAll()` - Retrieves all cached records
- `Get(key)` - Retrieves records by key
- `ForceRefresh()` - Manually refreshes the cache

### 2. Auto-Refresh Behavior
- Tests that the cache automatically picks up database changes
- Verifies cache invalidation triggers work correctly

### 3. Error Handling
- Tests behavior with invalid SQL queries
- Tests behavior with invalid key fields

### 4. Multiple Cache Types
- Tests with different data models (APIKey, User)
- Tests with different key fields and SQL queries

## Database Schema

The test database includes:

### Tables
- `api_keys` - Test table for API key caching
- `users` - Test table for user caching
- `cache.table_log` - Monitoring table for cache invalidation

### Functions
- `log_table_change()` - Trigger function for monitoring
- `create_table_monitor_trigger()` - Helper to create monitoring triggers

### Sample Data
- 4 API keys (3 active, 1 inactive)
- 4 users with different roles

## Troubleshooting

### Container Won't Start
```bash
# Check logs
docker-compose logs postgres

# Restart container
docker-compose restart postgres
```

### Database Connection Issues
```bash
# Test connection manually
psql -h localhost -p 5433 -U testuser -d testdb

# Check if port is available
netstat -an | grep 5433
```

### Tests Failing
```bash
# Run with verbose output
go test -v -run TestPostgresCacheIntegration

# Check database state
docker-compose exec postgres psql -U testuser -d testdb -c "SELECT * FROM api_keys;"
```

## Cleanup

```bash
# Stop and remove containers
docker-compose down

# Remove volumes (WARNING: This deletes all data)
docker-compose down -v
```

## Adding New Tests

1. Add new test functions to `integration_test.go`
2. Follow the naming convention: `TestPostgresCacheIntegration/TestName`
3. Use `require.NoError()` for setup and `assert.*` for validations
4. Clean up any test data you create
5. Add documentation for new test scenarios

## Performance Considerations

- Tests use a 2-second cache refresh interval for responsiveness
- Database connection pool is limited to 5 connections
- Tests include cleanup to prevent data accumulation
- Consider using `t.Parallel()` for independent tests if needed
