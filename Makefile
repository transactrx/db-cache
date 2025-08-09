# Database Cache Testing Makefile

.PHONY: test test-setup test-teardown test-all clean

# Test database setup
test-setup:
	@echo "Starting test database..."
	docker-compose -f docker-compose.test.yml up -d
	@echo "Waiting for database to be ready..."
	sleep 10

# Run tests
test:
	@echo "Running tests..."
	cd pkg/db-cache && go test -v

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	cd pkg/db-cache && go test -v -cover -coverprofile=coverage.out
	cd pkg/db-cache && go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: pkg/db-cache/coverage.html"

# Teardown test database
test-teardown:
	@echo "Stopping test database..."
	docker-compose -f docker-compose.test.yml down -v

# Run complete test suite
test-all: test-setup test test-teardown

# Run complete test suite with coverage
test-all-coverage: test-setup test-coverage test-teardown

# Clean up test artifacts
clean:
	rm -f pkg/db-cache/coverage.out
	rm -f pkg/db-cache/coverage.html
	docker-compose -f docker-compose.test.yml down -v --remove-orphans

# Development test loop (keeps database running)
test-dev: test-setup
	@echo "Test database is running. Use 'make test' to run tests, 'make test-teardown' when done."