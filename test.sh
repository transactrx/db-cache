#!/bin/bash

# DB Cache Test Runner Script
set -e

echo "🔧 Setting up test environment..."

# Start the test database
echo "Starting PostgreSQL test database..."
docker-compose -f docker-compose.test.yml up -d

# Wait for database to be ready
echo "Waiting for database to be ready..."
sleep 10

# Check if database is accessible
echo "Checking database connectivity..."
if ! docker-compose -f docker-compose.test.yml exec -T postgres-test pg_isready -U testuser -d db_cache_test > /dev/null 2>&1; then
    echo "❌ Database is not ready. Waiting a bit more..."
    sleep 10
    if ! docker-compose -f docker-compose.test.yml exec -T postgres-test pg_isready -U testuser -d db_cache_test > /dev/null 2>&1; then
        echo "❌ Database failed to start properly"
        exit 1
    fi
fi

echo "✅ Database is ready!"

# Run the tests
echo "🧪 Running tests..."
cd pkg/db-cache
if go test -v; then
    echo "✅ All tests passed!"
    TEST_EXIT_CODE=0
else
    echo "❌ Some tests failed!"
    TEST_EXIT_CODE=1
fi
cd ../..

# Cleanup
echo "🧹 Cleaning up test environment..."
docker-compose -f docker-compose.test.yml down -v

exit $TEST_EXIT_CODE