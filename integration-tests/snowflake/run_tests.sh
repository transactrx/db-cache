#!/bin/bash

# Snowflake Integration Test Runner for DB Cache Library
# This script sets up the test environment and runs the Snowflake integration tests

set -e  # Exit on any error

echo "❄️  Starting DB Cache Snowflake Integration Tests"
echo "=================================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if Go is available
if ! command -v go &> /dev/null; then
    print_error "Go is not installed or not in PATH"
    exit 1
fi

print_status "Prerequisites check passed ✓"

# Change to snowflake integration-tests directory
cd "$(dirname "$0")"

# Auto-load environment variables from .env if present (does not override existing env)
if [ -f ".env" ]; then
    print_status "Loading environment from $(pwd)/.env"
    set -a
    # shellcheck disable=SC1091
    . ./.env
    set +a
elif [ -f "../../.env" ]; then
    print_status "Loading environment from $(cd ../.. && pwd)/.env"
    set -a
    # shellcheck disable=SC1091
    . ../../.env
    set +a
fi

# Check if Snowflake tests should be skipped
if [ "$SKIP_SNOWFLAKE_TESTS" = "true" ]; then
    print_warning "Snowflake tests are disabled (SKIP_SNOWFLAKE_TESTS=true)"
    print_status "To enable tests, unset SKIP_SNOWFLAKE_TESTS and configure Snowflake credentials"
    exit 0
fi

# Check if required environment variables are set
if [ -z "$SNOWFLAKE_ACCOUNT" ] || [ -z "$SNOWFLAKE_USER" ]; then
    print_warning "Snowflake credentials not configured"
    print_status "To run tests, set SNOWFLAKE_ACCOUNT, SNOWFLAKE_USER, and SNOWFLAKE_PRIVATE_KEY"
    print_status "Skipping Snowflake tests..."
    exit 0
fi

# Display connection info
print_status "Snowflake connection configuration:"
echo "  Account: $SNOWFLAKE_ACCOUNT"
echo "  User: $SNOWFLAKE_USER"
echo "  Database: ${SNOWFLAKE_DATABASE:-CPE_DEV}"
echo "  Schema: ${SNOWFLAKE_SCHEMA:-CACHE_DEV}"
echo "  Role: ${SNOWFLAKE_ROLE:-BATCHJOB_RW_DEV}"
echo ""

# Install Go dependencies
print_status "Installing Go dependencies..."
go mod tidy

# Run the integration tests
print_status "Running Snowflake integration tests..."
echo ""

# Run tests with verbose output
if go test -v -timeout 10m; then
    print_success "All Snowflake integration tests passed! 🎉"
    echo ""
    print_status "Test Summary:"
    echo "  ✓ Snowflake connection established"
    echo "  ✓ Database schema verified"
    echo "  ✓ Sample data loaded"
    echo "  ✓ Cache operations tested"
    echo "  ✓ Auto-refresh behavior verified"
    echo "  ✓ Error handling validated"
    echo ""
    print_success "The db-cache library is working correctly with Snowflake!"
else
    print_error "Snowflake integration tests failed!"
    echo ""
    print_status "Common troubleshooting steps:"
    echo "  1. Verify your Snowflake credentials are correct"
    echo "  2. Check that your user has the required permissions"
    echo "  3. Ensure the test database and schema exist"
    echo "  4. Verify that the sample data has been loaded"
    echo "  5. Check your network connection to Snowflake"
    echo ""
    exit 1
fi

echo ""
print_success "Snowflake integration test run completed!"
print_warning "Remember: These tests use a real Snowflake instance and may incur costs."
