package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/internal/pkg/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFile_Unit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test auth with read permissions
	testAuth, err := authkeeper.NewNoAuth(true, false)
	require.NoError(t, err)

	// Create a test file
	testFilePath := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\n"
	err = os.WriteFile(testFilePath, []byte(content), 0644)
	require.NoError(t, err)

	// Create a test subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	err = os.Mkdir(subDir, 0755)
	require.NoError(t, err)

	t.Run("Read file content", func(t *testing.T) {
		params := &GetFileParams{
			Path:        testFilePath,
			ShowContent: true,
		}
		res, _, err := GetFile(context.Background(), nil, params, testAuth)
		assert.NoError(t, err)
		assert.NotNil(t, res)

		var result GetFileResult
		tc := res.Content[0].(*mcp.TextContent)
		err = json.Unmarshal([]byte(tc.Text), &result)
		assert.NoError(t, err)
		assert.Equal(t, "test.txt", result.Metadata.Name)
		// bufio.Scanner strips newlines and we join with \n, 
		// so the trailing newline of the last line is missing if it was empty.
		assert.Equal(t, strings.TrimSuffix(content, "\n"), result.Content)
		assert.Equal(t, 3, result.TotalLines)
	})

	t.Run("Read directory entries", func(t *testing.T) {
		params := &GetFileParams{
			Path: tmpDir,
		}
		res, _, err := GetFile(context.Background(), nil, params, testAuth)
		assert.NoError(t, err)

		var result GetFileResult
		tc := res.Content[0].(*mcp.TextContent)
		err = json.Unmarshal([]byte(tc.Text), &result)
		assert.NoError(t, err)
		assert.True(t, result.Metadata.IsDir)
		assert.GreaterOrEqual(t, len(result.Entries), 2) // test.txt and subdir
	})

	t.Run("File not found", func(t *testing.T) {
		params := &GetFileParams{
			Path: filepath.Join(tmpDir, "nonexistent"),
		}
		_, _, err := GetFile(context.Background(), nil, params, testAuth)
		assert.Error(t, err)
	})

	t.Run("Toon Serialization", func(t *testing.T) {
		params := &GetFileParams{
			Path:        testFilePath,
			ShowContent: true,
		}
		var enc util.OutputEncoding
		enc.UseToon()

		hf := &HostFile{
			Auth:    testAuth,
			Encoder: enc,
		}
		res, _, err := hf.GetFile(context.Background(), nil, params)
		assert.NoError(t, err)
		assert.NotNil(t, res)

		tc := res.Content[0].(*mcp.TextContent)
		
		// Toon output should start with standard Toon representation characters
		assert.True(t, strings.Contains(tc.Text, "metadata"))
	})
}
