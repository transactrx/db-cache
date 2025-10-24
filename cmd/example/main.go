package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	dbcache "github.com/transactrx/db-cache/pkg/db-cache"
)

// ApiKey2 type is defined in apiKeyModel.go

// Example usage for PostgreSQL
func postgresExample() {
	// Initialize read and read-write database pools
	readPool, err := pgxpool.New(context.Background(), "postgres://user:password@readOnlyHost:5432/prod")
	if err != nil {
		log.Panicf("Could not create read database pool: %v", err)
	}
	defer readPool.Close()

	rwPool, err := pgxpool.New(context.Background(), "postgres://user:password@readWriteHost:5432/prod")
	if err != nil {
		log.Panicf("Could not create read-write database pool: %v", err)
	}
	defer rwPool.Close()

	// Create cache using the unified API
	// For PostgreSQL, pass *pgxpool.Pool for both DB and DB_RW
	keyCache, err := dbcache.CreateCache[ApiKey2](
		nil, // logger (nil uses default)
		"SELECT key, description, configuration, name, max_daily_rate, volumes, client_id as clientid FROM api_keys",
		[]string{"api_keys"}, // monitored tables
		"Key",                // key field name
		time.Second*43,       // check interval
		readPool,             // read database connection
		rwPool,               // read-write database connection (for triggers)
	)
	if err != nil {
		panic(err)
	}

	// Use the cache
	result := keyCache.Get("someid")
	if result != nil {
		log.Printf("Found in cache: %v", result)
	} else {
		log.Printf("Value not found in cache!")
	}

	// Get all values
	allKeys := keyCache.GetAll()
	log.Printf("Total keys in cache: %d", len(allKeys))

	// Force refresh if needed
	if err := keyCache.ForceRefresh(); err != nil {
		log.Printf("Error refreshing cache: %v", err)
	}
}

// Example usage for Snowflake
func snowflakeExample() {
	// For Snowflake, you need a standard *sql.DB connection
	// Configure your Snowflake connection string
	snowflakeDB, err := sql.Open("snowflake", "user:password@account/database/schema")
	if err != nil {
		log.Panicf("Could not connect to Snowflake: %v", err)
	}
	defer snowflakeDB.Close()

	// For Snowflake: pass "DATABASE.SCHEMA" format as the DB_RW parameter
	// This tells the library where to find the TABLE_LOG for cache invalidation
	cache, err := dbcache.CreateCache[ApiKey2](
		nil, // logger (nil uses default)
		`SELECT 
			KEY AS "key", 
			DESCRIPTION AS "description", 
			CONFIGURATION AS "configuration", 
			NAME AS "name", 
			MAX_DAILY_RATE AS "max_daily_rate", 
			VOLUMES AS "volumes", 
			CLIENT_ID AS "clientid" 
		FROM MY_DATABASE.MY_SCHEMA.API_KEYS`,
		[]string{"API_KEYS"},    // monitored tables (will be prefixed with schema)
		"Key",                   // key field name
		time.Second*60,          // check interval
		snowflakeDB,             // Snowflake *sql.DB connection
		"MY_DATABASE.MY_SCHEMA", // For Snowflake: "DATABASE.SCHEMA" format
	)
	if err != nil {
		panic(err)
	}

	// Use the cache (same API as PostgreSQL!)
	result := cache.Get("someid")
	if result != nil {
		log.Printf("Found in cache: %v", result)
	} else {
		log.Printf("Value not found in cache!")
	}

	allKeys := cache.GetAll()
	log.Printf("Total keys in cache: %d", len(allKeys))
}

func main() {
	// Check environment variable to determine which example to run
	backend := os.Getenv("DB_BACKEND")

	switch backend {
	case "snowflake":
		log.Println("Running Snowflake example...")
		snowflakeExample()
	case "postgres":
		log.Println("Running PostgreSQL example...")
		postgresExample()
	default:
		log.Println("Usage: Set DB_BACKEND environment variable to 'postgres' or 'snowflake'")
		log.Println("Example: DB_BACKEND=postgres go run main.go")
		log.Println("\nBoth examples use the same dbcache.CreateCache API!")
		log.Println("The library automatically detects whether you're using PostgreSQL or Snowflake")
		log.Println("based on the type of the database connection you pass.")
	}
}
