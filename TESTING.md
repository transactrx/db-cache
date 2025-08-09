# Testing Guide for DB Cache

This guide explains how to set up and run tests for the db-cache library.

## Prerequisites

- Docker and docker-compose installed
- Go 1.24 or later
- Make (optional, for using Makefile commands)

## Test Setup

The testing environment uses Docker to create a PostgreSQL database with sample data. The setup includes:

- **PostgreSQL 15** running on port 5433
- **Sample database**: `db_cache_test`
- **Test user**: `testuser` / `testpass`
- **Sample tables**: `api_keys`, `products` with test data

## Running Tests

### Option 1: Using the test script (Recommended)

```bash
./test.sh
```

This script will:
1. Start the test database using Docker
2. Wait for it to be ready
3. Run all tests
4. Clean up the environment

### Option 2: Using Make commands

```bash
# Run complete test suite (setup, test, teardown)
make test-all

# Run tests with coverage report
make test-all-coverage

# For development (keeps database running)
make test-dev
make test           # Run tests multiple times
make test-teardown  # When done developing
```

### Option 3: Manual setup

```bash
# Start test database
docker-compose -f docker-compose.test.yml up -d

# Wait for database to be ready
sleep 10

# Run tests
cd pkg/db-cache
go test -v

# Clean up
docker-compose -f docker-compose.test.yml down -v
```

## Test Structure

### Test Files

- `pkg/db-cache/db_cache_test.go` - Main test file with comprehensive test cases

### Test Database Schema

The test database is automatically initialized with:

1. **Core tables**: `table_log` for cache invalidation tracking
2. **Sample tables**: 
   - `api_keys` - Sample API key data grouped by user
   - `products` - Sample product data grouped by category
3. **Functions**: Database functions required for cache monitoring
4. **Triggers**: Monitoring triggers for sample tables

### Test Cases

- **Unit Tests**:
  - `TestGenerateStaleCheckSQL` - Tests SQL generation for cache staleness checks
  - `TestCreateDbTriggersAndTables` - Tests database initialization

- **Integration Tests**:
  - `TestCreateCache_ApiKeys` - Tests cache creation and retrieval with API key data
  - `TestCreateCache_Products` - Tests cache creation with product data
  - `TestCacheRefresh` - Tests cache invalidation and refresh functionality

- **Resilience Tests**:
  - `TestCreateCache_BothDbsAvailable` - Tests normal operation with both reader and writer databases available
  - `TestCreateCache_WriterDbFailure` - Tests resilience when writer database is unavailable (simulates production master/replica failure scenarios)

## Sample Test Data

### API Keys Table
```sql
api_key         | user_id | is_active
key1_test123    | user1   | true
key2_test456    | user1   | true  
key3_test789    | user2   | true
key4_test000    | user2   | false
key5_test111    | user3   | true
```

### Products Table
```sql
name           | category    | price   | in_stock
Laptop Pro     | electronics | 1299.99 | true
Wireless Mouse | electronics | 29.99   | true
Office Chair   | furniture   | 299.99  | true
Desk Lamp      | furniture   | 89.99   | false
Coffee Mug     | office      | 12.99   | true
```

## Expected Test Results

When running tests successfully, you should see:
- API keys cache: 4 active keys across 3 users
- Products cache: 5 products across 3 categories
- Cache refresh functionality working correctly
- All database functions and triggers properly created

### Resilience Test Results

**Normal Operation** (`TestCreateCache_BothDbsAvailable`):
```
test_success 19:07:58 db_cache.go:195: successfully created monitoring trigger for table: api_keys
test_success 19:07:58 db_cache.go:93: Loading cache [api_keys] by UserID
```

**Writer Database Failure** (`TestCreateCache_WriterDbFailure`):
```
test_failure 19:07:58 db_cache.go:193: warning: could not create table monitoring trigger for api_keys: failed to connect to `host=localhost user=baduser database=nonexistent`: dial error (dial tcp 127.0.0.1:9999: connect: connection refused) (cache will still work but may not auto-refresh)
test_failure 19:07:58 db_cache.go:93: Loading cache [api_keys] by UserID
```

This demonstrates that:
- ✅ Cache creation succeeds even when writer DB fails
- ✅ Cache loading works normally from reader DB
- ✅ Appropriate warnings are logged for operational awareness
- ✅ Cache functionality remains intact (just without auto-refresh triggers)

## Continuous Integration

Tests automatically run on GitHub Actions for:
- Push to any branch
- Pull requests to any branch

The CI pipeline (`.github/workflows/go.yml`) includes:
- PostgreSQL 15 service container with test database
- Go 1.24 testing environment
- Database initialization with sample data
- Comprehensive test suite with race condition detection
- Coverage reporting and upload to Codecov
- **Build fails if any tests fail** - ensuring production quality

The workflow will:
1. Set up PostgreSQL test database
2. Initialize database schema and sample data
3. Download Go dependencies
4. Build the project
5. Run all tests with coverage (`go test -v -race -cover`)
6. Upload coverage results

This ensures that both resilience scenarios (normal operation and writer DB failure) pass before any code is merged.

## Troubleshooting

### Database Connection Issues
```bash
# Check if database is running
docker-compose -f docker-compose.test.yml ps

# Check database logs
docker-compose -f docker-compose.test.yml logs postgres-test

# Restart database
docker-compose -f docker-compose.test.yml restart postgres-test
```

### Port Conflicts
If port 5433 is already in use, modify `docker-compose.test.yml`:
```yaml
ports:
  - "5434:5432"  # Change to different port
```

Then update the connection string in `db_cache_test.go`:
```go
config, err := pgxpool.ParseConfig("postgres://testuser:testpass@localhost:5434/db_cache_test?sslmode=disable")
```

### Test Failures
- Ensure Docker is running and has sufficient resources
- Verify test database initialization completed successfully
- Check that sample data matches expected counts in test cases