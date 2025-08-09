package dbcache

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ApiKey struct {
	ID        *int       `db:"id"`
	ApiKey    *string    `db:"api_key"`
	UserID    *string    `db:"user_id"`
	IsActive  *bool      `db:"is_active"`
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

type Product struct {
	ID        *int       `db:"id"`
	Name      *string    `db:"name"`
	Category  *string    `db:"category"`
	Price     *float64   `db:"price"`
	InStock   *bool      `db:"in_stock"`
	CreatedAt *time.Time `db:"created_at"`
}

var testDB *pgxpool.Pool

func setupTestDB() error {
	config, err := pgxpool.ParseConfig("postgres://testuser:testpass@localhost:5433/db_cache_test?sslmode=disable")
	if err != nil {
		return err
	}

	testDB, err = pgxpool.New(context.Background(), config.ConnString())
	if err != nil {
		return err
	}

	return testDB.Ping(context.Background())
}

func teardownTestDB() {
	if testDB != nil {
		testDB.Close()
	}
}

func TestMain(m *testing.M) {
	// Setup
	if err := setupTestDB(); err != nil {
		log.Printf("Failed to setup test database: %v", err)
		log.Printf("Make sure to run: docker-compose -f docker-compose.test.yml up -d")
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Teardown
	teardownTestDB()

	os.Exit(code)
}

func TestGenerateStaleCheckSQL(t *testing.T) {
	tests := []struct {
		name   string
		tables []string
		want   string
	}{
		{
			name:   "single table",
			tables: []string{"api_keys"},
			want:   "select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from table_log where table_name='api_keys' ",
		},
		{
			name:   "multiple tables",
			tables: []string{"api_keys", "products"},
			want:   "select string_agg(ct, ', ') from (select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from table_log where table_name='api_keys'  union all select count(*) || cast(case when max(operation_time) is null then '1980-01-01' else max(operation_time) end as varchar) as ct from table_log where table_name='products' ) as t",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateStaleCheckSQL(tt.tables)
			if got != tt.want {
				t.Errorf("generateStaleCheckSQL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateCache_ApiKeys(t *testing.T) {
	logger := log.New(os.Stdout, "test ", log.Lshortfile|log.Ltime)

	sql := "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = true"
	monitoredTables := []string{"api_keys"}
	keyField := "UserID"
	cacheCheckInterval := 1 * time.Second

	cache, err := CreateCache[ApiKey](logger, sql, monitoredTables, keyField, cacheCheckInterval, testDB, testDB)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Test Get method
	user1Keys := cache.Get("user1")
	if len(user1Keys) != 2 {
		t.Errorf("Expected 2 keys for user1, got %d", len(user1Keys))
	}

	user2Keys := cache.Get("user2")
	if len(user2Keys) != 1 { // Only active keys
		t.Errorf("Expected 1 active key for user2, got %d", len(user2Keys))
	}

	// Test GetAll method
	allKeys := cache.GetAll()
	if len(allKeys) != 4 { // Only active keys
		t.Errorf("Expected 4 active keys total, got %d", len(allKeys))
	}

	// Test non-existent user
	nonExistentKeys := cache.Get("user999")
	if nonExistentKeys != nil {
		t.Errorf("Expected nil for non-existent user, got %v", nonExistentKeys)
	}
}

func TestCreateCache_Products(t *testing.T) {
	logger := log.New(os.Stdout, "test ", log.Lshortfile|log.Ltime)

	sql := "SELECT id, name, category, price, in_stock, created_at FROM products"
	monitoredTables := []string{"products"}
	keyField := "Category"
	cacheCheckInterval := 1 * time.Second

	cache, err := CreateCache[Product](logger, sql, monitoredTables, keyField, cacheCheckInterval, testDB, testDB)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Test Get method
	electronics := cache.Get("electronics")
	if len(electronics) != 2 {
		t.Errorf("Expected 2 electronics products, got %d", len(electronics))
	}

	furniture := cache.Get("furniture")
	if len(furniture) != 2 {
		t.Errorf("Expected 2 furniture products, got %d", len(furniture))
	}

	office := cache.Get("office")
	if len(office) != 1 {
		t.Errorf("Expected 1 office product, got %d", len(office))
	}

	// Test GetAll method
	allProducts := cache.GetAll()
	if len(allProducts) != 5 {
		t.Errorf("Expected 5 products total, got %d", len(allProducts))
	}
}

func TestCacheRefresh(t *testing.T) {
	logger := log.New(os.Stdout, "test ", log.Lshortfile|log.Ltime)

	sql := "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = true"
	monitoredTables := []string{"api_keys"}
	keyField := "UserID"
	cacheCheckInterval := 1 * time.Second

	cache, err := CreateCache[ApiKey](logger, sql, monitoredTables, keyField, cacheCheckInterval, testDB, testDB)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Initial state
	initialKeys := cache.GetAll()
	initialCount := len(initialKeys)

	// Insert a new active key
	_, err = testDB.Exec(context.Background(),
		"INSERT INTO api_keys (api_key, user_id, is_active) VALUES ('test_new_key', 'user1', true)")
	if err != nil {
		t.Fatalf("Failed to insert new key: %v", err)
	}

	// Force refresh
	err = cache.ForceRefresh()
	if err != nil {
		t.Fatalf("Failed to force refresh: %v", err)
	}

	// Check if cache was updated
	updatedKeys := cache.GetAll()
	if len(updatedKeys) != initialCount+1 {
		t.Errorf("Expected %d keys after insert, got %d", initialCount+1, len(updatedKeys))
	}

	// Clean up
	_, err = testDB.Exec(context.Background(),
		"DELETE FROM api_keys WHERE api_key = 'test_new_key'")
	if err != nil {
		t.Fatalf("Failed to clean up test data: %v", err)
	}
}

func TestCreateDbTriggersAndTables(t *testing.T) {
	// Test the initialization function
	err := CreateDbTriggersAndTables(testDB)
	if err != nil {
		t.Errorf("CreateDbTriggersAndTables failed: %v", err)
	}

	// Verify table_log table exists
	var count int
	err = testDB.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'table_log'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check table_log existence: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected table_log to exist")
	}

	// Verify log_changes function exists
	err = testDB.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM information_schema.routines WHERE routine_name = 'log_changes'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check log_changes function existence: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected log_changes function to exist")
	}

	// Verify create_table_monitor_trigger function exists
	err = testDB.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM information_schema.routines WHERE routine_name = 'create_table_monitor_trigger'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check create_table_monitor_trigger function existence: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected create_table_monitor_trigger function to exist")
	}
}

func TestCreateCache_WriterDbFailure(t *testing.T) {
	logger := log.New(os.Stdout, "test_failure ", log.Lshortfile|log.Ltime)

	// Create an invalid database connection for the writer (simulating failure)
	invalidConfig, err := pgxpool.ParseConfig("postgres://baduser:badpass@localhost:9999/nonexistent?sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to create invalid config: %v", err)
	}
	
	invalidDB, err := pgxpool.New(context.Background(), invalidConfig.ConnString())
	if err != nil {
		t.Fatalf("Failed to create invalid db pool: %v", err)
	}
	defer invalidDB.Close()

	sql := "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = true"
	monitoredTables := []string{"api_keys"}
	keyField := "UserID"
	cacheCheckInterval := 1 * time.Second

	// This should succeed despite writer DB failure - cache should be created with warnings
	cache, err := CreateCache[ApiKey](logger, sql, monitoredTables, keyField, cacheCheckInterval, testDB, invalidDB)
	if err != nil {
		t.Fatalf("Cache creation should succeed even with writer DB failure, got error: %v", err)
	}

	// Verify cache still works for reading data
	user1Keys := cache.Get("user1")
	if len(user1Keys) != 2 {
		t.Errorf("Expected 2 keys for user1, got %d", len(user1Keys))
	}

	// Verify GetAll still works
	allKeys := cache.GetAll()
	if len(allKeys) != 4 { // Only active keys
		t.Errorf("Expected 4 active keys total, got %d", len(allKeys))
	}

	t.Logf("Cache successfully created and functional despite writer DB failure")
}

func TestCreateCache_BothDbsAvailable(t *testing.T) {
	logger := log.New(os.Stdout, "test_success ", log.Lshortfile|log.Ltime)

	sql := "SELECT id, api_key, user_id, is_active, created_at, updated_at FROM api_keys WHERE is_active = true"
	monitoredTables := []string{"api_keys"}
	keyField := "UserID"
	cacheCheckInterval := 1 * time.Second

	// This should succeed with both reader and writer available
	cache, err := CreateCache[ApiKey](logger, sql, monitoredTables, keyField, cacheCheckInterval, testDB, testDB)
	if err != nil {
		t.Fatalf("Cache creation should succeed with both DBs available, got error: %v", err)
	}

	// Verify cache works normally
	user1Keys := cache.Get("user1")
	if len(user1Keys) != 2 {
		t.Errorf("Expected 2 keys for user1, got %d", len(user1Keys))
	}

	// Verify GetAll works
	allKeys := cache.GetAll()
	if len(allKeys) != 4 { // Only active keys
		t.Errorf("Expected 4 active keys total, got %d", len(allKeys))
	}

	t.Logf("Cache successfully created with both reader and writer DBs available")
}
