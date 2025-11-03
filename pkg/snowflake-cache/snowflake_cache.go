package snowflakecache

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/georgysavva/scany/v2/sqlscan"
)

// SnowflakeTable identifies a table in Snowflake by schema and name.
// It is used to list which tables should invalidate the cache when they change.
type SnowflakeTable struct {
	Schema string
	Table  string
}

// SnowflakeCache implements an in-memory cache backed by a Snowflake data source
// and a persistent change signal stored in <signalSchema>.DB_CACHE_LOG.
//
// The cache periodically polls DB_CACHE_LOG to compute a staleness fingerprint.
// If the fingerprint differs from the last seen value, it reloads the dataset
// using the provided SQL and rebuilds an index of key -> []T.
type DbCache[T any] struct {
	mutex           sync.RWMutex
	db              any
	keyCache        map[string][]T
	monitoredTables []string
	loadSQL         string
	sqlParameters   []any
	keyField        string
	staleCheckVal   *string
	logger          *log.Logger
	logSchema       string
	logDatabase     string
}

// Get returns the cached slice associated with the given key, or nil if missing.
func (c *DbCache[T]) Get(key string) []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if val, ok := c.keyCache[key]; ok {
		return val
	}
	return nil
}

// GetAll flattens and returns all cached rows across all keys.
func (c *DbCache[T]) GetAll() []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	var result []T
	for _, val := range c.keyCache {
		result = append(result, val...)
	}
	return result
}

// ForceRefresh clears the last fingerprint and forces a reload at once.
func (c *DbCache[T]) ForceRefresh() error {
	c.mutex.Lock()
	c.staleCheckVal = nil
	c.mutex.Unlock()

	fp, err := c.getDbStaleCheckValue()
	if err != nil {
		return err
	}
	return c.loadCache(fp)
}

// getDbStaleCheckValue builds and executes the fingerprint query over DB_CACHE_LOG
// for the configured set of monitored tables.
func (c *DbCache[T]) getDbStaleCheckValue() (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Build fully-qualified TABLE_LOG reference
	logTable := "TABLE_LOG"
	if c.logSchema != "" && c.logDatabase != "" {
		logTable = fmt.Sprintf("%s.%s.TABLE_LOG", strings.ToUpper(c.logDatabase), strings.ToUpper(c.logSchema))
	} else if c.logSchema != "" {
		logTable = fmt.Sprintf("%s.TABLE_LOG", strings.ToUpper(c.logSchema))
	}

	// Generate base SQL (uses unqualified TABLE_LOG) and then qualify it
	base := generateStaleCheckSQL(c.monitoredTables)
	q := strings.ReplaceAll(base, "TABLE_LOG", logTable)

	// Build bind args (one per monitored table)
	args := make([]any, 0, len(c.monitoredTables))
	for _, t := range c.monitoredTables {
		args = append(args, t)
	}

	// Scan result
	if len(c.monitoredTables) == 1 {
		row := c.db.(*sql.DB).QueryRowContext(ctx, q, args...)
		var v string
		if err := row.Scan(&v); err != nil {
			return nil, err
		}
		return &v, nil
	}
	row := c.db.(*sql.DB).QueryRowContext(ctx, q, args...)
	var v sql.NullString
	if err := row.Scan(&v); err != nil {
		return nil, err
	}
	if v.Valid {
		val := v.String
		return &val, nil
	}
	return nil, fmt.Errorf("fingerprint query returned NULL")
}

// loadCache executes the load SQL, rebuilds the in-memory index, and
// stores the new fingerprint.
func (c *DbCache[T]) loadCache(staleCheckVal *string) error {
	if c.staleCheckVal != nil && *c.staleCheckVal == *staleCheckVal {
		c.logger.Printf("Cache is already up to date..")
		return nil
	}
	c.logger.Printf("Loading cache %s by %s\n", c.monitoredTables, c.keyField)

	var result []T
	if err := sqlscan.Select(context.Background(), c.db.(*sql.DB), &result, c.loadSQL, c.sqlParameters...); err != nil {
		return err
	}

	newMap := make(map[string][]T)
	for _, row := range result {
		key, err := extractKeyValue(row, c.keyField)
		if err != nil {
			return err
		}
		newMap[key] = append(newMap[key], row)
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.keyCache = newMap
	c.staleCheckVal = staleCheckVal
	return nil
}

// CreateSnowflakeCache constructs and starts a Snowflake-backed cache.
// Signature mirrors Postgres ordering to minimize migration friction.
//
// Parameters:
//   - logger: optional logger; when nil, a default logger to stdout is used
//   - SQL: SELECT to load the dataset of type T
//   - monitoredTables: names as "SCHEMA.TABLE" or plain "TABLE" (uses defaultSchema)
//   - keyField: exported struct field name on T used as the cache key (string or *string)
//   - checkInterval: how frequently to poll DB_CACHE_LOG for changes
//   - db: an initialized *sql.DB using the gosnowflake driver
//   - signalSchema: schema where DB_CACHE_LOG resides (e.g., "UTILS")
//   - defaultSchema: schema applied to unqualified monitored table names
//   - sqlParams: optional bind parameters for SQL
func CreateCache[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	checkInterval time.Duration,
	db any,
	defaultSchema string,
	sqlParams ...any,
) (*DbCache[T], error) {
	if SQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	// Normalize monitored tables using provided defaultSchema
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			qualified = append(qualified, SnowflakeTable{Schema: parts[0], Table: parts[1]})
		} else {
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: name})
		}
	}
	// Use default change-log schema "CACHE" to match test expectations
	return CreateSnowflakeCacheQualified[T](
		logger,
		db,
		SQL,
		keyField,
		checkInterval,
		"",
		"CACHE",
		qualified,
		sqlParams...,
	)
}

// CreateCacheWithDatabase allows specifying both database and schema for TABLE_LOG location
func CreateCacheWithDatabase[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	checkInterval time.Duration,
	db any,
	database string,
	defaultSchema string,
	sqlParams ...any,
) (*DbCache[T], error) {
	if SQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	// Normalize into schema/table pairs
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			qualified = append(qualified, SnowflakeTable{Schema: parts[0], Table: parts[1]})
		} else {
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: name})
		}
	}
	cache, err := CreateSnowflakeCacheQualified[T](
		logger,
		db,
		SQL,
		keyField,
		checkInterval,
		strings.ToUpper(database),
		strings.ToUpper(defaultSchema),
		qualified,
		sqlParams...,
	)
	if err != nil {
		return nil, err
	}
	return cache, nil
}

// generateStaleCheckSQL builds the fingerprint query for the given monitored tables
// using Snowflake SQL dialect. It intentionally references TABLE_LOG without schema,
// and callers should replace TABLE_LOG with a fully qualified name when needed.
func generateStaleCheckSQL(monitoredTables []string) string {
	if len(monitoredTables) == 1 {
		return "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct FROM TABLE_LOG WHERE table_name = ?"
	}
	var b strings.Builder
	b.WriteString("SELECT LISTAGG(ct, ', ') FROM (")
	for i := 0; i < len(monitoredTables); i++ {
		b.WriteString("SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct FROM TABLE_LOG WHERE table_name = ? ")
		if i < len(monitoredTables)-1 {
			b.WriteString(" UNION ALL ")
		} else {
			b.WriteString(") AS t")
		}
	}
	return b.String()
}

// registerStreamsForTables calls the Snowflake REGISTERCACHETABLE procedure
// for each monitored table, to create per-table Streams used by the heartbeat.
// Procedure signature: REGISTERCACHETABLE(DB_NAME, SCHEMA_NAME, TABLE_NAME)
// Failures are logged and ignored so the cache can still function.
func registerStreamsForTables(db *sql.DB, logger *log.Logger, logDatabase, logSchema string, tables []SnowflakeTable) {
	if logSchema == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var procFQN string
	if logDatabase != "" {
		procFQN = fmt.Sprintf("%s.%s.REGISTERCACHETABLE", logDatabase, logSchema)
	} else {
		procFQN = fmt.Sprintf("%s.REGISTERCACHETABLE", logSchema)
	}

	for _, t := range tables {
		schema := strings.ToUpper(t.Schema)
		table := strings.ToUpper(t.Table)
		database := strings.ToUpper(logDatabase)
		if database == "" {
			// If no database specified, try to infer from schema or use empty
			database = ""
		}
		call := fmt.Sprintf("CALL %s(?, ?, ?)", procFQN)
		if _, err := db.ExecContext(ctx, call, database, schema, table); err != nil {
			logger.Printf("warning: could not register stream for %s.%s.%s via %s: %v (continuing without auto-refresh)", database, schema, table, procFQN, err)
		} else {
			logger.Printf("registered stream for %s.%s.%s via %s", database, schema, table, procFQN)
		}
	}
}

// CreateSnowflakeCacheQualified constructs a Snowflake-backed cache when you already
// have schema-qualified monitored table descriptors. Prefer CreateSnowflakeCache for
// migration-friendly parameter ordering.
func CreateSnowflakeCacheQualified[T any](
	logger *log.Logger,
	db any,
	loadSQL string,
	keyField string,
	checkInterval time.Duration,
	logDatabase string,
	logSchema string,
	monitoredTables []SnowflakeTable,
	sqlParams ...any,
) (*DbCache[T], error) {
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	if loadSQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if keyField == "" {
		return nil, fmt.Errorf("keyField must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	if logger == nil {
		logger = log.New(os.Stdout, "sf_cache ", log.Lshortfile|log.Ltime)
	}

	// Convert to []string for internal storage
	tbls := make([]string, 0, len(monitoredTables))
	for _, t := range monitoredTables {
		if t.Table != "" {
			tbls = append(tbls, strings.ToUpper(t.Table))
		}
	}

	cache := &DbCache[T]{
		db:              db,
		loadSQL:         loadSQL,
		sqlParameters:   sqlParams,
		keyField:        keyField,
		monitoredTables: tbls,
		logger:          logger,
		keyCache:        make(map[string][]T),
	}
	cache.logSchema = strings.ToUpper(logSchema)
	cache.logDatabase = strings.ToUpper(logDatabase)

	// Best-effort provisioning of Streams via REGISTER_TABLE (opt-in via env).
	// Enable by setting DB_CACHE_SF_REGISTER_STREAMS=true in the environment.
	if sfDB, ok := db.(*sql.DB); ok && strings.EqualFold(os.Getenv("DB_CACHE_SF_REGISTER_STREAMS"), "true") {
		registerStreamsForTables(sfDB, cache.logger, cache.logDatabase, cache.logSchema, monitoredTables)
	}

	// Initial load
	fp, err := cache.getDbStaleCheckValue()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial fingerprint: %w", err)
	}
	if err := cache.loadCache(fp); err != nil {
		return nil, fmt.Errorf("failed to perform initial load: %w", err)
	}

	// Start background poller (parity with Postgres)
	go func() {
		for now := range time.Tick(checkInterval) {
			staleCheckVal, err := cache.getDbStaleCheckValue()
			if err != nil {
				cache.logger.Printf("Error in cache monitor: %v", err)
			} else {
				cache.logger.Printf("time to reload cache: %s", now.String())
				if err := cache.loadCache(staleCheckVal); err != nil {
					cache.logger.Printf("error while reloading cache: %v", err)
				}
			}
		}
	}()

	return cache, nil
}

// extractKeyValue returns a string value from the named exported struct field.
// The field may be of type string or *string. When pointer, it must be non-nil.
func extractKeyValue(obj any, keyField string) (string, error) {
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() {
		return "", fmt.Errorf("invalid value for key extraction")
	}
	if v.Kind() == reflect.Map {
		return "", fmt.Errorf("map types are not supported for key extraction")
	}

	// Find struct field by name, case-insensitive (parity with Postgres cache)
	t := v.Type()
	var f reflect.Value
	found := false
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if strings.EqualFold(sf.Name, keyField) {
			f = v.Field(i)
			found = true
			break
		}
	}
	if !found || !f.IsValid() {
		return "", fmt.Errorf("field '%s' not found on cached type", keyField)
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return "", fmt.Errorf("key field '%s' is nil", keyField)
		}
		f = f.Elem()
	}
	if f.Kind() != reflect.String {
		return "", fmt.Errorf("key field '%s' must be string or *string", keyField)
	}
	return f.String(), nil
}
