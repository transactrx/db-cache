package dbcache

import (
	"fmt"
	"testing"
)

// test generateStaleCheckSQL function
func TestGenerateStaleCheckSQL(t *testing.T) {
	sql := generateStaleCheckSQL([]string{"api_keys"})
	fmt.Println(sql)
}
