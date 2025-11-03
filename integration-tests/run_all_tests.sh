#!/bin/bash

# Complete Integration Test Runner for DB Cache Library
# This script runs both PostgreSQL and Snowflake integration tests

echo "🚀 Starting Complete DB Cache Integration Tests"
echo "=============================================="

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

# Change to integration-tests directory
cd "$(dirname "$0")"

# Track overall success
overall_success=true

# Run PostgreSQL tests
echo ""
print_status "Running PostgreSQL Integration Tests..."
echo "=============================================="

cd postgres
if echo "n" | ./run_tests.sh; then
    print_success "PostgreSQL tests completed successfully ✓"
else
    print_error "PostgreSQL tests failed ✗"
    overall_success=false
fi
cd ..

# Run Snowflake tests
echo ""
print_status "Running Snowflake Integration Tests..."
echo "=============================================="

cd snowflake
if ./run_tests.sh; then
    print_success "Snowflake tests completed successfully ✓"
else
    print_error "Snowflake tests failed ✗"
    overall_success=false
fi
cd ..

# Final summary
echo ""
echo "=============================================="
if [ "$overall_success" = true ]; then
    print_success "All integration tests completed successfully! 🎉"
    echo ""
    print_status "Summary:"
    echo "  ✓ PostgreSQL integration tests passed"
    echo "  ✓ Snowflake integration tests passed"
    echo "  ✓ Both database backends are working correctly"
    echo ""
    print_success "The db-cache library is fully functional with both PostgreSQL and Snowflake!"
else
    print_error "Some integration tests failed!"
    echo ""
    print_status "Please check the individual test outputs above for details."
    echo "  - PostgreSQL tests: ./postgres/run_tests.sh"
    echo "  - Snowflake tests: ./snowflake/run_tests.sh"
    exit 1
fi

echo ""
print_status "Integration test run completed!"
