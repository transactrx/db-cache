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
type SnowflakeCache[T any] struct {
	mutex           sync.RWMutex
	db              *sql.DB
	keyCache        map[string][]T
	monitoredTables []SnowflakeTable
	loadSQL         string
	sqlParameters   []any
	keyField        string
	signalSchema    string
	staleCheckVal   *string
	logger          *log.Logger
	checkInterval   time.Duration
	ticker          *time.Ticker
	stopCh          chan struct{}
}

// Get returns the cached slice associated with the given key, or nil if missing.
func (c *SnowflakeCache[T]) Get(key string) []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if val, ok := c.keyCache[key]; ok {
		return val
	}
	return nil
}

// GetAll flattens and returns all cached rows across all keys.
func (c *SnowflakeCache[T]) GetAll() []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	var result []T
	for _, val := range c.keyCache {
		result = append(result, val...)
	}
	return result
}

// ForceRefresh clears the last fingerprint and forces a reload at once.
func (c *SnowflakeCache[T]) ForceRefresh() error {
	c.mutex.Lock()
	c.staleCheckVal = nil
	c.mutex.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fp, err := c.getDbStaleCheckValue(ctx)
	if err != nil {
		return err
	}
	return c.loadCache(ctx, fp)
}

// getStaleFingerprint builds and executes the fingerprint query over DB_CACHE_LOG
// for the configured set of monitored tables.
func (c *SnowflakeCache[T]) getDbStaleCheckValue(ctx context.Context) (string, error) {
	if len(c.monitoredTables) == 1 {
		q := fmt.Sprintf(
			"SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct FROM %s.DB_CACHE_LOG WHERE schema_name = ? AND table_name = ?",
			c.signalSchema,
		)
		row := c.db.QueryRowContext(ctx, q, c.monitoredTables[0].Schema, c.monitoredTables[0].Table)
		var v string
		if err := row.Scan(&v); err != nil {
			return "", err
		}
		return v, nil
	}

	// Multiple tables: aggregate per (schema_name, table_name) and listagg the tokens
	var b strings.Builder
	b.WriteString("SELECT LISTAGG(ct, ', ') FROM ( ")
	b.WriteString("SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(operation_time), TO_TIMESTAMP_LTZ('1980-01-01'))) AS ct ")
	b.WriteString("FROM ")
	b.WriteString(c.signalSchema)
	b.WriteString(".DB_CACHE_LOG WHERE (schema_name, table_name) IN (")

	args := make([]any, 0, len(c.monitoredTables)*2)
	for i, t := range c.monitoredTables {
		b.WriteString("(?, ?)")
		args = append(args, t.Schema, t.Table)
		if i < len(c.monitoredTables)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString(") GROUP BY schema_name, table_name) AS t")

	q := b.String()
	row := c.db.QueryRowContext(ctx, q, args...)
	var v sql.NullString
	if err := row.Scan(&v); err != nil {
		return "", err
	}
	if v.Valid {
		return v.String, nil
	}
	return "", fmt.Errorf("fingerprint query returned NULL")
}

// Close stops the background poller and releases resources owned by the cache.
func (c *SnowflakeCache[T]) Close() {
	if c.ticker != nil {
		c.ticker.Stop()
	}
	select {
	case <-c.stopCh:
		// already closed
	default:
		close(c.stopCh)
	}
}

// pollLoop periodically checks for staleness and reloads the cache when needed.
func (c *SnowflakeCache[T]) pollLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		case now := <-c.ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			fp, err := c.getDbStaleCheckValue(ctx)
			if err != nil {
				c.logger.Printf("error getting fingerprint: %v", err)
				cancel()
				continue
			}
			// Only reload if fingerprint changed
			c.mutex.RLock()
			prev := c.staleCheckVal
			c.mutex.RUnlock()
			if prev != nil && *prev == fp {
				// up-to-date
				cancel()
				continue
			}
			c.logger.Printf("reloading cache at %s", now.Format(time.RFC3339))
			if err := c.loadCache(ctx, fp); err != nil {
				c.logger.Printf("error reloading cache: %v", err)
			}
			cancel()
		}
	}
}

// loadCache executes the load SQL, rebuilds the in-memory index, and
// stores the new fingerprint.
func (c *SnowflakeCache[T]) loadCache(ctx context.Context, newFingerprint string) error {
	var result []T
	if err := sqlscan.Select(ctx, c.db, &result, c.loadSQL, c.sqlParameters...); err != nil {
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
	c.staleCheckVal = &newFingerprint
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
func CreateSnowflakeCache[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	checkInterval time.Duration,
	db *sql.DB,
	signalSchema string,
	defaultSchema string,
	sqlParams ...any,
) (*SnowflakeCache[T], error) {
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
	return CreateSnowflakeCacheQualified[T](
		logger,
		db,
		signalSchema,
		SQL,
		keyField,
		checkInterval,
		qualified,
		sqlParams...,
	)
}

// CreateSnowflakeCacheQualified constructs a Snowflake-backed cache when you already
// have schema-qualified monitored table descriptors. Prefer CreateSnowflakeCache for
// migration-friendly parameter ordering.
func CreateSnowflakeCacheQualified[T any](
	logger *log.Logger,
	db *sql.DB,
	signalSchema string,
	loadSQL string,
	keyField string,
	checkInterval time.Duration,
	monitoredTables []SnowflakeTable,
	sqlParams ...any,
) (*SnowflakeCache[T], error) {
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	if signalSchema == "" {
		return nil, fmt.Errorf("signalSchema must not be empty (schema that contains DB_CACHE_LOG)")
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

	cache := &SnowflakeCache[T]{
		db:              db,
		loadSQL:         loadSQL,
		sqlParameters:   sqlParams,
		keyField:        keyField,
		monitoredTables: monitoredTables,
		signalSchema:    signalSchema,
		logger:          logger,
		checkInterval:   checkInterval,
		keyCache:        make(map[string][]T),
		stopCh:          make(chan struct{}),
	}

	// Initial load
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	fp, err := cache.getDbStaleCheckValue(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get initial fingerprint: %w", err)
	}
	if err := cache.loadCache(ctx, fp); err != nil {
		return nil, fmt.Errorf("failed to perform initial load: %w", err)
	}

	// Start background poller
	cache.ticker = time.NewTicker(cache.checkInterval)
	go cache.pollLoop()

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
	f := v.FieldByName(keyField)
	if !f.IsValid() {
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
