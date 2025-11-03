package snowflake_test

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"

	sf "github.com/snowflakedb/gosnowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// Test models that match our Snowflake database schema
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

// Test configuration - these should be set via environment variables
var (
	SNOWFLAKE_ACCOUNT   = os.Getenv("SNOWFLAKE_ACCOUNT")
	SNOWFLAKE_USER      = os.Getenv("SNOWFLAKE_USER")
	SNOWFLAKE_PASSWORD  = os.Getenv("SNOWFLAKE_PASSWORD")
	SNOWFLAKE_DATABASE  = os.Getenv("SNOWFLAKE_DATABASE")
	SNOWFLAKE_SCHEMA    = os.Getenv("SNOWFLAKE_SCHEMA")
	SNOWFLAKE_WAREHOUSE = os.Getenv("SNOWFLAKE_WAREHOUSE")
)

func getTestSnowflakeConnection() (*sql.DB, error) {
	// Defaults
	if SNOWFLAKE_DATABASE == "" {
		SNOWFLAKE_DATABASE = "CPE_DEV"
	}
	if SNOWFLAKE_SCHEMA == "" {
		SNOWFLAKE_SCHEMA = "CACHE_DEV"
	}

	cfg := &sf.Config{
		Account: SNOWFLAKE_ACCOUNT,
		User:    SNOWFLAKE_USER,
		// Intentionally omit Database and Schema here to avoid ping errors on missing privileges
		Warehouse: SNOWFLAKE_WAREHOUSE,
		Role:      os.Getenv("SNOWFLAKE_ROLE"),
	}

	if host := os.Getenv("SNOWFLAKE_HOST"); host != "" {
		cfg.Host = host
		cfg.InsecureMode = strings.Contains(host, "localhost") || strings.Contains(host, ":")
	}

	// Prefer key pair auth when a private key is provided
	fmt.Printf("SNOWFLAKE_PRIVATE_KEY len=%d, SNOWFLAKE_PRIVATE_KEY_PATH=%s\n", len(os.Getenv("SNOWFLAKE_PRIVATE_KEY")), os.Getenv("SNOWFLAKE_PRIVATE_KEY_PATH"))
	if pk, err := loadPrivateKeyFromEnv(); err == nil && pk != nil {
		cfg.Authenticator = sf.AuthTypeJwt
		cfg.PrivateKey = pk
	} else {
		// Fallback to password if present
		if SNOWFLAKE_PASSWORD == "" {
			return nil, fmt.Errorf("no private key or password provided for Snowflake auth")
		}
		cfg.Password = SNOWFLAKE_PASSWORD
	}

	dsn, err := sf.DSN(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build DSN: %w", err)
	}
	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Snowflake connection: %w", err)
	}
	// Do not ping here; some roles fail ping before USE DATABASE/SCHEMA. We'll set context later.
	return db, nil
}

func loadPrivateKeyFromEnv() (*rsa.PrivateKey, error) {
	// Option 1: SNOWFLAKE_PRIVATE_KEY as PEM string (supports \n-escaped newlines)
	if pemStr := os.Getenv("SNOWFLAKE_PRIVATE_KEY"); pemStr != "" {
		pemStr = strings.ReplaceAll(pemStr, "\\n", "\n")
		// First try as direct PEM
		if pk, err := parsePKCS8PEM([]byte(pemStr)); err == nil {
			return pk, nil
		}
		// If not PEM, try base64-decoding then parse PEM/DER
		if decoded, err := base64.StdEncoding.DecodeString(pemStr); err == nil {
			// Try as PEM-encoded text (after base64 decode)
			if pk, err2 := parsePKCS8PEM(decoded); err2 == nil {
				return pk, nil
			}
			// Try as raw PKCS8 DER after base64 decode
			if anyKey, err3 := x509.ParsePKCS8PrivateKey(decoded); err3 == nil {
				if k, ok := anyKey.(*rsa.PrivateKey); ok {
					return k, nil
				}
			}
			// Reference repo approach: base64 decode THEN pem decode THEN parse
			block, _ := pem.Decode(decoded)
			if block != nil && block.Type == "PRIVATE KEY" {
				if anyKey, err4 := x509.ParsePKCS8PrivateKey(block.Bytes); err4 == nil {
					if k, ok := anyKey.(*rsa.PrivateKey); ok {
						return k, nil
					}
				}
			}
		}
		// If still failing, log and continue to file path
		log.Printf("Failed to parse private key from SNOWFLAKE_PRIVATE_KEY env var")
	}
	// Option 2: SNOWFLAKE_PRIVATE_KEY_PATH pointing to a PEM file
	if path := os.Getenv("SNOWFLAKE_PRIVATE_KEY_PATH"); path != "" {
		log.Printf("Loading Snowflake private key from path: %s", path)
		b, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Failed to read private key file: %v", err)
			return nil, err
		}
		pk, err := parsePKCS8PEM(b)
		if err != nil {
			log.Printf("Failed to parse private key from path: %v", err)
			return nil, err
		}
		return pk, nil
	}
	// Option 3: default file name in current working directory
	if _, err := os.Stat("private_key.pem"); err == nil {
		log.Printf("Loading Snowflake private key from default file: private_key.pem")
		if b, err := os.ReadFile("private_key.pem"); err == nil {
			if pk, err2 := parsePKCS8PEM(b); err2 == nil {
				return pk, nil
			} else {
				log.Printf("Failed to parse private key from default file: %v", err2)
			}
		} else {
			log.Printf("Failed to read default private key file: %v", err)
		}
	}
	return nil, fmt.Errorf("no private key in env")
}

func parsePKCS8PEM(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 as fallback
		if pkcs1, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
			return pkcs1, nil
		}
		return nil, err
	}
	if k, ok := keyAny.(*rsa.PrivateKey); ok {
		return k, nil
	}
	return nil, fmt.Errorf("unsupported private key type")
}

func TestMain(m *testing.M) {
	// Load .env if present (simple parser) but DO NOT override already-set env vars
	if b, err := os.ReadFile(".env"); err == nil {
		lines := strings.Split(string(b), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// split at first '='
			if i := strings.IndexByte(line, '='); i > 0 {
				k := strings.TrimSpace(line[:i])
				v := strings.TrimSpace(line[i+1:])
				v = strings.Trim(v, "\"'")
				if _, exists := os.LookupEnv(k); !exists {
					os.Setenv(k, v)
				}
			}
		}
	}
	// refresh globals from env (honors any pre-set vars)
	SNOWFLAKE_ACCOUNT = os.Getenv("SNOWFLAKE_ACCOUNT")
	SNOWFLAKE_USER = os.Getenv("SNOWFLAKE_USER")
	SNOWFLAKE_PASSWORD = os.Getenv("SNOWFLAKE_PASSWORD")
	SNOWFLAKE_DATABASE = os.Getenv("SNOWFLAKE_DATABASE")
	SNOWFLAKE_SCHEMA = os.Getenv("SNOWFLAKE_SCHEMA")
	SNOWFLAKE_WAREHOUSE = os.Getenv("SNOWFLAKE_WAREHOUSE")

	// Check if we should skip Snowflake tests
	if os.Getenv("SKIP_SNOWFLAKE_TESTS") == "true" {
		fmt.Println("Skipping Snowflake integration tests (SKIP_SNOWFLAKE_TESTS=true)")
		os.Exit(0)
	}

	// Run tests
	code := m.Run()
	os.Exit(code)
}

func TestSnowflakeCacheIntegration(t *testing.T) {
	// Skip if Snowflake tests are disabled
	if os.Getenv("SKIP_SNOWFLAKE_TESTS") == "true" {
		t.Skip("Snowflake integration tests disabled")
	}

	// Get database connection
	db, err := getTestSnowflakeConnection()
	require.NoError(t, err, "Failed to connect to Snowflake test database")
	defer db.Close()

	// Ensure we can USE the target database and schema (clear diagnostics if not authorized)
	ensureDbAndSchemaContext(t, db)

	// Ensure schema and sample data exist for tests (idempotent)
	setupSnowflakeSchemaAndData(t, db)

	// Create logger for cache
	logger := log.New(os.Stdout, "[SNOWFLAKE_INTEGRATION_TEST] ", log.LstdFlags|log.Lshortfile)

	t.Run("APIKey Cache Operations", func(t *testing.T) {
		// Define SQL query to load API keys
		sqlQuery := `
            SELECT 
                ID AS "id",
                KEY AS "key",
                NAME AS "name",
                IS_ACTIVE AS "is_active",
                CREATED_AT AS "created_at"
            FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS 
            WHERE IS_ACTIVE = TRUE 
            ORDER BY CREATED_AT DESC
        `

		// Create cache for API keys using the unified dbcache.CreateCache interface
		// Note: Pass schema as DB_RW parameter for Snowflake - it's a string that will be used as defaultSchema
		cache, err := dbcache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{"API_KEYS"}, // monitored tables
			"KEY",                // key field
			2*time.Second,        // check interval
			db,                   // Snowflake DB connection (*sql.DB)
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA, // For Snowflake: "DATABASE.SCHEMA" format
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
            SELECT 
                ID AS "id",
                USERNAME AS "username",
                EMAIL AS "email",
                ROLE AS "role",
                CREATED_AT AS "created_at"
            FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.USERS 
            ORDER BY USERNAME ASC
        `

		// Create cache for users using the unified dbcache.CreateCache interface
		cache, err := dbcache.CreateCache[User](
			logger,
			sqlQuery,
			[]string{"USERS"}, // monitored tables
			"USERNAME",        // key field
			2*time.Second,     // check interval
			db,                // Snowflake DB connection (*sql.DB)
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA, // For Snowflake: "DATABASE.SCHEMA" format
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
		sqlQuery := `SELECT ID AS "id", KEY AS "key", NAME AS "name", IS_ACTIVE AS "is_active", CREATED_AT AS "created_at" FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS WHERE IS_ACTIVE = TRUE`

		cache, err := dbcache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{"API_KEYS"},
			"KEY",
			1*time.Second, // Very short interval for testing
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		require.NoError(t, err, "Failed to create cache for auto-refresh test")

		// Get initial data
		initialData := cache.GetAll()
		initialCount := len(initialData)
		t.Logf("Initial cache contains %d records", initialCount)

		// Insert a new record directly into the database
		insertSQL := `INSERT INTO ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS (KEY, NAME, IS_ACTIVE) VALUES (?, ?, ?)`
		_, err = db.ExecContext(context.Background(), insertSQL, "test_auto_refresh_key", "Test Auto Refresh", true)
		require.NoError(t, err, "Failed to insert test record")

		// Manually log the change (since Snowflake doesn't have triggers)
		schemaName := SNOWFLAKE_SCHEMA
		if schemaName == "" {
			schemaName = "CACHE_DEV"
		}
		logSQL := fmt.Sprintf("INSERT INTO %s.TABLE_LOG (TABLE_NAME, OPERATION_TIME, OPERATION_TYPE) VALUES (?, CURRENT_TIMESTAMP(), ?)", SNOWFLAKE_DATABASE+"."+schemaName)
		_, err = db.ExecContext(context.Background(), logSQL, "API_KEYS", "INSERT")
		require.NoError(t, err, "Failed to log table change")

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
		if assert.NotEmpty(t, newRecordData, "New record should be accessible via cache") {
			assert.Equal(t, "test_auto_refresh_key", newRecordData[0].Key, "Should return correct new record")
		}

		// Clean up test record
		_, err = db.ExecContext(context.Background(), `DELETE FROM `+SNOWFLAKE_DATABASE+`.`+SNOWFLAKE_SCHEMA+`.API_KEYS WHERE KEY = ?`, "test_auto_refresh_key")
		require.NoError(t, err, "Failed to clean up test record")
	})

	t.Run("Error Handling", func(t *testing.T) {
		// Test with invalid SQL
		invalidSQL := `SELECT INVALID_COLUMN FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.NON_EXISTENT_TABLE`

		cache, err := dbcache.CreateCache[APIKey](
			logger,
			invalidSQL,
			[]string{"API_KEYS"},
			"KEY",
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		assert.Error(t, err, "Should fail with invalid SQL")
		assert.Nil(t, cache, "Cache should be nil on error")

		// Test with invalid key field
		validSQL := `SELECT ID AS "id", KEY AS "key", NAME AS "name", IS_ACTIVE AS "is_active", CREATED_AT AS "created_at" FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS WHERE IS_ACTIVE = TRUE`

		cache, err = dbcache.CreateCache[APIKey](
			logger,
			validSQL,
			[]string{"API_KEYS"},
			"INVALID_FIELD", // invalid key field
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		assert.Error(t, err, "Should fail with invalid key field")
		assert.Nil(t, cache, "Cache should be nil on error")
	})

	t.Run("REGISTERCACHETABLE Procedure Integration", func(t *testing.T) {
		// This test creates a new table, registers it using REGISTERCACHETABLE procedure,
		// and verifies the cache works with the registered table and stream

		ctx := context.Background()
		testTableName := "TEST_PRODUCTS_" + fmt.Sprintf("%d", time.Now().Unix())

		// Step 1: Create a test table
		createTableSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.%s.%s (
				ID INTEGER AUTOINCREMENT,
				PRODUCT_CODE VARCHAR(50) UNIQUE NOT NULL,
				PRODUCT_NAME VARCHAR(255) NOT NULL,
				PRICE FLOAT NOT NULL,
				IN_STOCK BOOLEAN DEFAULT TRUE,
				CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
			)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err := db.ExecContext(ctx, createTableSQL)
		require.NoError(t, err, "Failed to create test table %s", testTableName)
		t.Logf("✓ Created test table: %s", testTableName)

		// Ensure cleanup happens
		defer func() {
			dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)
			db.ExecContext(context.Background(), dropSQL)
			t.Logf("✓ Cleaned up test table: %s", testTableName)
		}()

		// Step 2: Insert test data
		insertSQL := fmt.Sprintf(`
			INSERT INTO %s.%s.%s (PRODUCT_CODE, PRODUCT_NAME, PRICE, IN_STOCK)
			VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err = db.ExecContext(ctx, insertSQL,
			"PROD001", "Widget A", 29.99, true,
			"PROD002", "Widget B", 39.99, true,
			"PROD003", "Widget C", 49.99, false,
		)
		require.NoError(t, err, "Failed to insert test data")
		t.Logf("✓ Inserted 3 test products")

		// Step 3: Enable Go-side stream registration (do not call the procedure directly)
		prev := os.Getenv("DB_CACHE_SF_REGISTER_STREAMS")
		os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "true")
		defer os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", prev)
		t.Logf("✓ Enabled DB_CACHE_SF_REGISTER_STREAMS=true; Go cache will register via REGISTERCACHETABLE")

		// Step 4: Create cache for the new table (procedure already handled TABLE_LOG)
		type TestProduct struct {
			ID          int     `db:"id"`
			ProductCode string  `db:"product_code"`
			ProductName string  `db:"product_name"`
			Price       float64 `db:"price"`
			InStock     bool    `db:"in_stock"`
		}

		cacheSQL := fmt.Sprintf(`
			SELECT 
				ID AS "id",
				PRODUCT_CODE AS "product_code",
				PRODUCT_NAME AS "product_name",
				PRICE AS "price",
				IN_STOCK AS "in_stock"
			FROM %s.%s.%s
			WHERE IN_STOCK = TRUE
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		cache, err := dbcache.CreateCache[TestProduct](
			logger,
			cacheSQL,
			[]string{testTableName},
			"ProductCode",
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		require.NoError(t, err, "Failed to create cache for test table")
		t.Logf("✓ Created cache for %s", testTableName)

		// Step 5: Verify cache works correctly
		allProducts := cache.GetAll()
		assert.Len(t, allProducts, 2, "Should return 2 in-stock products")
		t.Logf("✓ Cache contains %d in-stock products", len(allProducts))

		// Test Get with specific product code
		prod1 := cache.Get("PROD001")
		if assert.NotEmpty(t, prod1, "Should find PROD001") {
			assert.Equal(t, "PROD001", prod1[0].ProductCode)
			assert.Equal(t, "Widget A", prod1[0].ProductName)
			assert.Equal(t, 29.99, prod1[0].Price)
			t.Logf("✓ Retrieved PROD001: %s ($%.2f)", prod1[0].ProductName, prod1[0].Price)
		}

		// Test that out-of-stock product is not in cache
		prod3 := cache.Get("PROD003")
		assert.Empty(t, prod3, "Should not find out-of-stock PROD003")

		// Step 6: Test auto-refresh by inserting new data and manually updating TABLE_LOG
		// Note: In production, your heartbeat Task would read the stream and update TABLE_LOG
		insertNewSQL := fmt.Sprintf(`
			INSERT INTO %s.%s.%s (PRODUCT_CODE, PRODUCT_NAME, PRICE, IN_STOCK)
			VALUES (?, ?, ?, ?)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err = db.ExecContext(ctx, insertNewSQL, "PROD004", "Widget D", 59.99, true)
		require.NoError(t, err, "Failed to insert new product")
		t.Logf("✓ Inserted new product PROD004")

		// Manually update TABLE_LOG to trigger cache refresh
		// (In production, your heartbeat Task consumes the stream and does this)
		logSQL := fmt.Sprintf("INSERT INTO %s.%s.TABLE_LOG (TABLE_NAME, OPERATION_TIME, OPERATION_TYPE) VALUES (?, CURRENT_TIMESTAMP(), ?)",
			SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA)
		_, err = db.ExecContext(ctx, logSQL, testTableName, "INSERT")
		require.NoError(t, err, "Failed to log change to TABLE_LOG")

		// Wait for cache to refresh
		time.Sleep(3 * time.Second)

		// Verify new product is in cache
		refreshedProducts := cache.GetAll()
		assert.Len(t, refreshedProducts, 3, "Should now have 3 in-stock products")
		t.Logf("✓ Cache refreshed: now contains %d products", len(refreshedProducts))

		prod4 := cache.Get("PROD004")
		if assert.NotEmpty(t, prod4, "Should find newly inserted PROD004") {
			assert.Equal(t, "PROD004", prod4[0].ProductCode)
			assert.Equal(t, "Widget D", prod4[0].ProductName)
			t.Logf("✓ New product PROD004 successfully cached")
		}

		t.Logf("✅ REGISTERCACHETABLE procedure integration test completed successfully!")
	})
}

// Helper function to run a single test (useful for debugging)
func runSingleTest(testName string) {
	fmt.Printf("Running Snowflake test: %s\n", testName)

	// This would be used for manual testing/debugging
	// In practice, use: go test -run TestSnowflakeCacheIntegration/TestName
}

// setupSnowflakeSchemaAndData creates required schemas/tables and loads sample data.
// It is safe to run multiple times (uses IF NOT EXISTS and upserts by unique keys).
func setupSnowflakeSchemaAndData(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schemaName := SNOWFLAKE_SCHEMA
	if schemaName == "" {
		schemaName = "CACHE_DEV"
	}

	statements := []string{
		// Change log table (qualified)
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s.TABLE_LOG (ID INTEGER AUTOINCREMENT, TABLE_NAME VARCHAR(255) NOT NULL, OPERATION_TIME TIMESTAMP DEFAULT CURRENT_TIMESTAMP(), OPERATION_TYPE VARCHAR(10) DEFAULT 'UPDATE')", SNOWFLAKE_DATABASE, schemaName),
		// API keys table
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s.API_KEYS (
            ID INTEGER AUTOINCREMENT,
            KEY VARCHAR(255) UNIQUE NOT NULL,
            NAME VARCHAR(255) NOT NULL,
            IS_ACTIVE BOOLEAN DEFAULT TRUE,
            CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
        )`, SNOWFLAKE_DATABASE, schemaName),
		// Users table
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s.USERS (
            ID INTEGER AUTOINCREMENT,
            USERNAME VARCHAR(255) UNIQUE NOT NULL,
            EMAIL VARCHAR(255) NOT NULL,
            ROLE VARCHAR(50) DEFAULT 'user',
            CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
        )`, SNOWFLAKE_DATABASE, schemaName),
	}

	for _, sqlStmt := range statements {
		_, err := db.ExecContext(ctx, sqlStmt)
		require.NoError(t, err, "Failed executing schema statement: %s", sqlStmt)
	}

	// Seed data (use merge-like behavior via delete+insert for idempotency)
	// API_KEYS data
	_, err := db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.%s.API_KEYS WHERE KEY IN ('api_key_1','api_key_2','api_key_3','api_key_4')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.API_KEYS (KEY, NAME, IS_ACTIVE) VALUES
        ('api_key_1','Test API Key 1', TRUE),
        ('api_key_2','Test API Key 2', TRUE),
        ('api_key_3','Test API Key 3', FALSE),
        ('api_key_4','Test API Key 4', TRUE)`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)

	// USERS data
	_, err = db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.%s.USERS WHERE USERNAME IN ('alice','bob','charlie','diana')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.USERS (USERNAME, EMAIL, ROLE) VALUES
        ('alice','alice@example.com','admin'),
        ('bob','bob@example.com','user'),
        ('charlie','charlie@example.com','user'),
        ('diana','diana@example.com','moderator')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)

	// Prime the change log
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.TABLE_LOG (TABLE_NAME, OPERATION_TIME, OPERATION_TYPE) VALUES
        ('API_KEYS', CURRENT_TIMESTAMP(), 'INSERT'),
        ('USERS', CURRENT_TIMESTAMP(), 'INSERT')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
}

// ensureDbAndSchemaContext executes USE statements to set current DB and schema,
// surfacing authorization issues early with clear errors.
func ensureDbAndSchemaContext(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// USE DATABASE
	if strings.TrimSpace(SNOWFLAKE_DATABASE) != "" {
		_, err := db.ExecContext(ctx, "USE DATABASE "+SNOWFLAKE_DATABASE)
		require.NoError(t, err, "Not authorized to USE DATABASE %s", SNOWFLAKE_DATABASE)
	}

	// Attempt to USE SCHEMA (database.schema form for clarity)
	if strings.TrimSpace(SNOWFLAKE_DATABASE) != "" && strings.TrimSpace(SNOWFLAKE_SCHEMA) != "" {
		_, err := db.ExecContext(ctx, fmt.Sprintf("USE SCHEMA %s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA))
		require.NoError(t, err, "Not authorized to USE SCHEMA %s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA)
	}
}
