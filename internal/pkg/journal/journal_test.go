package journal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateListLogsSchema(t *testing.T) {
	schema := CreateListLogsSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "count")
	assert.Contains(t, schema.Properties, "offset")
	assert.Contains(t, schema.Properties, "unit")
}

func TestCanAccessLogs(t *testing.T) {
	// This might return true or false depending on the environment,
	// but we can check it doesn't panic.
	_ = CanAccessLogs()
}
