#!/bin/bash

# Integration Test Runner for DB Cache Library
# This script sets up the test environment and runs the integration tests

set -e  # Exit on any error

echo "🚀 Starting DB Cache Integration Tests"
echo "======================================"

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

# Check if Docker is available
if ! command -v docker &> /dev/null; then
    print_error "Docker is not installed or not in PATH"
    exit 1
fi

if ! command -v docker-compose &> /dev/null; then
    print_error "Docker Compose is not installed or not in PATH"
    exit 1
fi

# Check if Go is available
if ! command -v go &> /dev/null; then
    print_error "Go is not installed or not in PATH"
    exit 1
fi

print_status "Prerequisites check passed ✓"

# Change to integration-tests directory
cd "$(dirname "$0")"

# Clean up any existing containers
print_status "Cleaning up existing containers..."
docker-compose down -v 2>/dev/null || true

# Start PostgreSQL container
print_status "Starting PostgreSQL container..."
docker-compose up -d

# Wait for database to be ready
print_status "Waiting for database to be ready..."
max_attempts=30
attempt=1

while [ $attempt -le $max_attempts ]; do
    if docker-compose exec -T postgres pg_isready -U testuser -d testdb &>/dev/null; then
        print_success "Database is ready!"
        break
    fi
    
    if [ $attempt -eq $max_attempts ]; then
        print_error "Database failed to start within expected time"
        print_error "Container logs:"
        docker-compose logs postgres
        exit 1
    fi
    
    print_status "Attempt $attempt/$max_attempts - waiting for database..."
    sleep 2
    ((attempt++))
done

# Install Go dependencies
print_status "Installing Go dependencies..."
go mod tidy

# Run the integration tests
print_status "Running integration tests..."
echo ""

# Run tests with verbose output
if go test -v -timeout 5m; then
    print_success "All integration tests passed! 🎉"
    echo ""
    print_status "Test Summary:"
    echo "  ✓ PostgreSQL container started successfully"
    echo "  ✓ Database schema initialized"
    echo "  ✓ Sample data loaded"
    echo "  ✓ Cache operations tested"
    echo "  ✓ Auto-refresh behavior verified"
    echo "  ✓ Error handling validated"
    echo ""
    print_success "The db-cache library is working correctly with PostgreSQL!"
else
    print_error "Integration tests failed!"
    echo ""
    print_status "Debugging information:"
    echo "  Container status:"
    docker-compose ps
    echo ""
    echo "  Database logs:"
    docker-compose logs postgres
    echo ""
    print_warning "You can manually inspect the database with:"
    echo "  docker-compose exec postgres psql -U testuser -d testdb"
    exit 1
fi

# Ask if user wants to keep containers running (skip if not interactive or in CI)
echo ""
if [ -t 0 ] && [ "$CI" != "true" ]; then
    read -p "Keep PostgreSQL container running for manual testing? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_status "Container kept running. Stop with: docker-compose down"
        print_status "Connect manually with: docker-compose exec postgres psql -U testuser -d testdb"
    else
        print_status "Stopping containers..."
        docker-compose down
        print_success "Cleanup completed"
    fi
else
    print_status "Stopping containers..."
    docker-compose down
    print_success "Cleanup completed"
fi

echo ""
print_success "Integration test run completed!"
