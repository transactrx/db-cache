package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// Test models that match our database schema
// Note: Field names must match the database column names for the cache to work
type APIKey struct {
	ID        int       `db:"id"`
	Key       string    `db:"key"`
	Name      string    `db:"name"`
	IsActive  bool      `db:"is_active"`
	CreatedAt time.Time `db:"created_at"`
}

type User struct {
	ID        int       `db:"id"`
	Username  string    `db:"username"`
	Email     string    `db:"email"`
	Role      string    `db:"role"`
	CreatedAt time.Time `db:"created_at"`
}

// Test configuration
const (
	DB_HOST     = "localhost"
	DB_PORT     = "5433"
	DB_USER     = "testuser"
	DB_PASSWORD = "testpass"
	DB_NAME     = "testdb"
)

func getTestDBConnection() (*pgxpool.Pool, error) {
	// Construct connection string for test database
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		DB_USER, DB_PASSWORD, DB_HOST, DB_PORT, DB_NAME)

	// Create connection pool with reasonable settings for testing
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	// Set pool configuration for testing
	config.MaxConns = 5
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	// Create the pool
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

func TestMain(m *testing.M) {
	// Check if we should skip integration tests
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		fmt.Println("Skipping integration tests (SKIP_INTEGRATION_TESTS=true)")
		os.Exit(0)
	}

	// Run tests
	code := m.Run()
	os.Exit(code)
}

func TestPostgresCacheIntegration(t *testing.T) {
	// Skip if integration tests are disabled
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		t.Skip("Integration tests disabled")
	}

	// Get database connection
	db, err := getTestDBConnection()
	require.NoError(t, err, "Failed to connect to test database")
	defer db.Close()

	// Create logger for cache
	logger := log.New(os.Stdout, "[INTEGRATION_TEST] ", log.LstdFlags|log.Lshortfile)

	t.Run("APIKey Cache Operations", func(t *testing.T) {
		// Define SQL query to load API keys
		sqlQuery := `
			SELECT id, key, name, is_active, created_at 
			FROM api_keys 
			WHERE is_active = true 
			ORDER BY created_at DESC
		`

		// Create cache for API keys
		cache, err := dbcache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{"api_keys"}, // monitored tables
			"key",                // key field
			2*time.Second,        // check interval
			db,                   // read-only DB
			db,                   // read-write DB
		)
		require.NoError(t, err, "Failed to create API key cache")

		// Test GetAll - should return all active API keys
		allKeys := cache.GetAll()
		assert.NotEmpty(t, allKeys, "GetAll should return active API keys")
		t.Logf("Retrieved %d active API keys", len(allKeys))

		// Verify we got the expected active keys
		expectedKeys := []string{"api_key_1", "api_key_2", "api_key_4"}
		for _, key := range allKeys {
			assert.Contains(t, expectedKeys, key.Key, "Should only return active keys")
			assert.True(t, key.IsActive, "All returned keys should be active")
		}

		// Test Get with specific key
		key1Data := cache.Get("api_key_1")
		assert.NotEmpty(t, key1Data, "Get should return data for existing key")
		assert.Len(t, key1Data, 1, "Should return exactly one record for unique key")
		assert.Equal(t, "api_key_1", key1Data[0].Key, "Should return correct key")

		// Test Get with non-existent key
		nonExistentData := cache.Get("non_existent_key")
		assert.Empty(t, nonExistentData, "Get should return empty slice for non-existent key")

		// Test ForceRefresh
		err = cache.ForceRefresh()
		assert.NoError(t, err, "ForceRefresh should succeed")

		// Verify data is still there after refresh
		refreshedKeys := cache.GetAll()
		assert.Equal(t, len(allKeys), len(refreshedKeys), "Data should be preserved after refresh")
	})

	t.Run("User Cache Operations", func(t *testing.T) {
		// Define SQL query to load users
		sqlQuery := `
			SELECT id, username, email, role, created_at 
			FROM users 
			ORDER BY username ASC
		`

		// Create cache for users
		cache, err := dbcache.CreateCache[User](
			logger,
			sqlQuery,
			[]string{"users"}, // monitored tables
			"username",        // key field
			2*time.Second,     // check interval
			db,                // read-only DB
			db,                // read-write DB
		)
		require.NoError(t, err, "Failed to create user cache")

		// Test GetAll - should return all users
		allUsers := cache.GetAll()
		assert.NotEmpty(t, allUsers, "GetAll should return users")
		t.Logf("Retrieved %d users", len(allUsers))

		// Verify we got the expected users
		expectedUsernames := []string{"alice", "bob", "charlie", "diana"}
		for _, user := range allUsers {
			assert.Contains(t, expectedUsernames, user.Username, "Should return expected users")
		}

		// Test Get with specific username
		aliceData := cache.Get("alice")
		assert.NotEmpty(t, aliceData, "Get should return data for existing user")
		assert.Len(t, aliceData, 1, "Should return exactly one record for unique username")
		assert.Equal(t, "alice", aliceData[0].Username, "Should return correct username")
		assert.Equal(t, "admin", aliceData[0].Role, "Should return correct role")

		// Test Get with non-existent user
		nonExistentData := cache.Get("non_existent_user")
		assert.Empty(t, nonExistentData, "Get should return empty slice for non-existent user")
	})

	t.Run("Cache Auto-Refresh Behavior", func(t *testing.T) {
		// Create a cache with a short refresh interval
		sqlQuery := `SELECT id, key, name, is_active, created_at FROM api_keys WHERE is_active = true`

		cache, err := dbcache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{"api_keys"},
			"key",
			1*time.Second, // Very short interval for testing
			db,
			db,
		)
		require.NoError(t, err, "Failed to create cache for auto-refresh test")

		// Get initial data
		initialData := cache.GetAll()
		initialCount := len(initialData)
		t.Logf("Initial cache contains %d records", initialCount)

		// Insert a new record directly into the database
		insertSQL := `INSERT INTO api_keys (key, name, is_active) VALUES ($1, $2, $3)`
		_, err = db.Exec(context.Background(), insertSQL, "test_auto_refresh_key", "Test Auto Refresh", true)
		require.NoError(t, err, "Failed to insert test record")

		// Wait for cache to potentially refresh (with some buffer)
		time.Sleep(3 * time.Second)

		// Check if cache picked up the new record
		refreshedData := cache.GetAll()
		refreshedCount := len(refreshedData)
		t.Logf("After insert, cache contains %d records", refreshedCount)

		// The cache should have picked up the new record
		assert.Greater(t, refreshedCount, initialCount, "Cache should have picked up new record")

		// Verify the new record is in the cache
		newRecordData := cache.Get("test_auto_refresh_key")
		assert.NotEmpty(t, newRecordData, "New record should be accessible via cache")
		assert.Equal(t, "test_auto_refresh_key", newRecordData[0].Key, "Should return correct new record")

		// Clean up test record
		_, err = db.Exec(context.Background(), `DELETE FROM api_keys WHERE key = $1`, "test_auto_refresh_key")
		require.NoError(t, err, "Failed to clean up test record")
	})

	t.Run("Error Handling", func(t *testing.T) {
		// Test with invalid SQL
		invalidSQL := `SELECT invalid_column FROM non_existent_table`

		cache, err := dbcache.CreateCache[APIKey](
			logger,
			invalidSQL,
			[]string{"api_keys"},
			"key",
			2*time.Second,
			db,
			db,
		)
		assert.Error(t, err, "Should fail with invalid SQL")
		assert.Nil(t, cache, "Cache should be nil on error")

		// Test with invalid key field
		validSQL := `SELECT id, key, name, is_active, created_at FROM api_keys WHERE is_active = true`

		cache, err = dbcache.CreateCache[APIKey](
			logger,
			validSQL,
			[]string{"api_keys"},
			"invalid_field", // invalid key field
			2*time.Second,
			db,
			db,
		)
		assert.Error(t, err, "Should fail with invalid key field")
		assert.Nil(t, cache, "Cache should be nil on error")
	})
}

// Helper function to run a single test (useful for debugging)
func runSingleTest(testName string) {
	fmt.Printf("Running test: %s\n", testName)

	// This would be used for manual testing/debugging
	// In practice, use: go test -run TestPostgresCacheIntegration/TestName
}
