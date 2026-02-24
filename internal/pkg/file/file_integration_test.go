package file_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/pkg/file"
	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func runIntegrationGetFile(t *testing.T) {
	ctx := context.Background()

	// Create dummy files for testing
	dummyContent1 := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	dummyPath1 := "/tmp/dummy1.txt"
	err := os.WriteFile(dummyPath1, []byte(dummyContent1), 0644)
	assert.NoError(t, err)
	defer os.Remove(dummyPath1)

	dummyContent2 := "Test\nData"
	dummyPath2 := "/tmp/dummy2.txt"
	err = os.WriteFile(dummyPath2, []byte(dummyContent2), 0600)
	assert.NoError(t, err)
	defer os.Remove(dummyPath2)

	// Test pagination, limit, and show content
	tests := []struct {
		TestName        string
		Path            string
		ShowContent     bool
		Offset          int
		Limit           int
		ExpectedContent string
		ExpectedLines   int
	}{
		{
			TestName:        "No Content",
			Path:            dummyPath1,
			ShowContent:     false,
			ExpectedContent: "",
			ExpectedLines:   0,
		},
		{
			TestName:        "Full Content",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          0,
			Limit:           10,
			ExpectedContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			ExpectedLines:   5,
		},
		{
			TestName:        "Pagination Offset 2",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          2,
			Limit:           10,
			ExpectedContent: "Line 3\nLine 4\nLine 5",
			ExpectedLines:   5,
		},
		{
			TestName:        "Pagination Limit 2",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          0,
			Limit:           2,
			ExpectedContent: "Line 1\nLine 2",
			ExpectedLines:   5,
		},
		{
			TestName:        "Pagination Offset 1 Limit 2",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          1,
			Limit:           2,
			ExpectedContent: "Line 2\nLine 3",
			ExpectedLines:   5,
		},
		{
			TestName:        "Pagination Limit Exceeds Lines",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          3,
			Limit:           10,
			ExpectedContent: "Line 4\nLine 5",
			ExpectedLines:   5,
		},
		{
			TestName:        "Offset Exceeds Lines",
			Path:            dummyPath1,
			ShowContent:     true,
			Offset:          10,
			Limit:           2,
			ExpectedContent: "",
			ExpectedLines:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.TestName, func(t *testing.T) {
			params := &file.GetFileParams{
				Path:        tt.Path,
				ShowContent: tt.ShowContent,
				Offset:      tt.Offset,
				Limit:       tt.Limit,
			}

			res, _, err := file.GetFile(ctx, nil, params)
			assert.NoError(t, err)
			assert.NotNil(t, res)

			textContent, ok := res.Content[0].(*mcp.TextContent)
			assert.True(t, ok)

			var getFileRes file.GetFileResult
			err = json.Unmarshal([]byte(textContent.Text), &getFileRes)
			assert.NoError(t, err)

			assert.NotNil(t, getFileRes.Metadata)
			assert.Equal(t, tt.ExpectedContent, getFileRes.Content)
			assert.Equal(t, tt.ExpectedLines, getFileRes.TotalLines)

			if tt.ShowContent {
				expectedLimit := tt.Limit
				if expectedLimit <= 0 {
					expectedLimit = 1000 // default
				}
				assert.Equal(t, tt.Offset, getFileRes.Offset)
				assert.Equal(t, expectedLimit, getFileRes.Limit)
			}
		})
	}

	// Test metadata on two different files
	t.Run("Metadata File 1", func(t *testing.T) {
		params := &file.GetFileParams{Path: dummyPath1}
		res, _, err := file.GetFile(ctx, nil, params)
		assert.NoError(t, err)

		var getFileRes file.GetFileResult
		err = json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &getFileRes)
		assert.NoError(t, err)
		assert.Equal(t, "dummy1.txt", getFileRes.Metadata.Name)
		assert.Equal(t, "-rw-r--r--", getFileRes.Metadata.Mode)
		assert.False(t, getFileRes.Metadata.IsDir)
	})

	t.Run("Metadata File 2", func(t *testing.T) {
		params := &file.GetFileParams{Path: dummyPath2}
		res, _, err := file.GetFile(ctx, nil, params)
		assert.NoError(t, err)

		var getFileRes file.GetFileResult
		err = json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &getFileRes)
		assert.NoError(t, err)
		assert.Equal(t, "dummy2.txt", getFileRes.Metadata.Name)
		assert.Equal(t, "-rw-------", getFileRes.Metadata.Mode)
		assert.False(t, getFileRes.Metadata.IsDir)
	})

	// Test getting a directory
	t.Run("Directory", func(t *testing.T) {
		paramsDir := &file.GetFileParams{Path: "/tmp"}
		resDir, _, err := file.GetFile(ctx, nil, paramsDir)
		assert.NoError(t, err)

		var getFileResDir file.GetFileResult
		err = json.Unmarshal([]byte(resDir.Content[0].(*mcp.TextContent).Text), &getFileResDir)
		assert.NoError(t, err)

		assert.NotNil(t, getFileResDir.Metadata)
		assert.True(t, getFileResDir.Metadata.IsDir)
		assert.True(t, len(getFileResDir.Entries) > 0)
	})
}

func TestIntegrationGetFile(t *testing.T) {
	if os.Getenv("IN_CONTAINER") == "1" {
		runIntegrationGetFile(t)
		return
	}

	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testBinPath := "file.test"
	err := testframework.BuildGoTestBinary(t, ".", testBinPath)
	if err != nil {
		t.Fatalf("Failed to build test binary: %v", err)
	}
	defer os.Remove(testBinPath)

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      "./" + testBinPath,
			ContainerFilePath: "/tmp/file.test",
			FileMode:          0755,
		},
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	// Run the test inside the container
	code, outReader, err := container.Exec(ctx, []string{"env", "IN_CONTAINER=1", "/tmp/file.test", "-test.v", "-test.run", "TestIntegrationGetFile"})

	var outStr string
	if outReader != nil {
		if b, readErr := io.ReadAll(outReader); readErr == nil {
			outStr = string(b)
		}
	}

	if err != nil {
		t.Fatalf("Exec failed: %v\nOutput: %s", err, outStr)
	}

	if code != 0 {
		t.Fatalf("Test failed inside container (exit code %d).\nOutput:\n%s", code, outStr)
	} else {
		t.Logf("Test succeeded inside container.\nOutput:\n%s", outStr)
	}
}
