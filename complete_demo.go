package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"
)

func main() {
	ctx := context.Background()

	// Start the systemd-mcp server process
	cmd := exec.Command("./systemd-mcp", "--noauth")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal("Failed to create stdin pipe:", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal("Failed to create stdout pipe:", err)
	}

	if err := cmd.Start(); err != nil {
		log.Fatal("Failed to start server:", err)
	}
	defer cmd.Process.Kill()

	// Create a client that can communicate with the server
	client := &mcpClient{
		stdin:  stdin,
		stdout: stdout,
	}

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Test: Initialize the client
	if err := client.initialize(ctx); err != nil {
		log.Fatal("Failed to initialize:", err)
	}

	fmt.Println("=== Complete Log Filtering Demonstration ===\n")

	// Demonstrate all parameters working together
	fmt.Println("🔍 All filtering features combined:")
	fmt.Println("   ✅ Pagination: offset=0, count=5")
	fmt.Println("   ✅ Time filtering: from today")
	fmt.Println("   ✅ Regex pattern: logs containing 'session' or 'cron'")
	fmt.Println()

	today := time.Now().Format("2006-01-02")
	fromTime := today + "T00:00:00+05:30"

	result, err := client.callTool(ctx, "list_log", map[string]interface{}{
		"count":   5,
		"offset":  0,
		"from":    fromTime,
		"pattern": "(session|cron)",
	})
	if err != nil {
		log.Fatal("Failed to call list_log with all filters:", err)
	}

	printFilteredResults(result)

	// Show parameter flexibility
	fmt.Println("\n📊 Parameter Usage Summary:")
	fmt.Println("   • count: Limit number of results (default: 100)")
	fmt.Println("   • offset: Skip newest entries for pagination (default: 0)")
	fmt.Println("   • from: Start time filter (RFC3339 format)")
	fmt.Println("   • to: End time filter (RFC3339 format)")
	fmt.Println("   • pattern: Regex filter for message content (case-insensitive)")
	fmt.Println("   • unit: Filter by specific systemd unit")
	fmt.Println("   • allboots: Include logs from all boots (default: false)")

	fmt.Println("\n💡 Example Usage Patterns:")

	examples := []struct {
		name   string
		params map[string]interface{}
		desc   string
	}{
		{
			"Recent errors",
			map[string]interface{}{
				"pattern": "(error|fail|critical)",
				"count":   10,
			},
			"Find recent error messages",
		},
		{
			"Service restarts",
			map[string]interface{}{
				"pattern": "(start|restart|stop)",
				"from":    today + "T00:00:00+05:30",
				"count":   20,
			},
			"Track service state changes today",
		},
		{
			"User activity",
			map[string]interface{}{
				"pattern": "(login|logout|session)",
				"count":   15,
			},
			"Monitor user authentication events",
		},
		{
			"System boots",
			map[string]interface{}{
				"pattern":  "boot",
				"allboots": true,
				"count":    5,
			},
			"View boot-related messages across all boots",
		},
	}

	for i, example := range examples {
		fmt.Printf("   %d. %s: %s\n", i+1, example.name, example.desc)
		fmt.Printf("      Parameters: %v\n", formatParams(example.params))
	}
}

func printFilteredResults(result interface{}) {
	if content, ok := result.(map[string]interface{}); ok {
		if contentArray, ok := content["content"].([]interface{}); ok {
			if textContent, ok := contentArray[0].(map[string]interface{}); ok {
				if text, ok := textContent["text"].(string); ok {
					var logResult map[string]interface{}
					if err := json.Unmarshal([]byte(text), &logResult); err == nil {
						if messages, ok := logResult["messages"].([]interface{}); ok {
							fmt.Printf("📋 Found %d matching log entries:\n\n", len(messages))

							if len(messages) == 0 {
								fmt.Println("   No logs matched the specified criteria")
								return
							}

							for i, msg := range messages {
								if msgMap, ok := msg.(map[string]interface{}); ok {
									if timestamp, ok := msgMap["time"].(string); ok {
										if message, ok := msgMap["message"].(string); ok {
											fmt.Printf("   %d. %s\n", i+1, timestamp[:19])
											fmt.Printf("      📝 %s\n\n", message)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func formatParams(params map[string]interface{}) string {
	result := "{"
	first := true
	for k, v := range params {
		if !first {
			result += ", "
		}
		result += fmt.Sprintf("\"%s\": %v", k, v)
		first = false
	}
	result += "}"
	return result
}

type mcpClient struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	id     int
}

func (c *mcpClient) nextID() int {
	c.id++
	return c.id
}

func (c *mcpClient) sendRequest(req interface{}) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *mcpClient) readResponse() (map[string]interface{}, error) {
	scanner := bufio.NewScanner(c.stdout)
	if scanner.Scan() {
		var response map[string]interface{}
		err := json.Unmarshal(scanner.Bytes(), &response)
		return response, err
	}
	return nil, scanner.Err()
}

func (c *mcpClient) initialize(ctx context.Context) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      c.nextID(),
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "1.0",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "complete-demo-client",
				"version": "1.0.0",
			},
		},
	}

	if err := c.sendRequest(req); err != nil {
		return err
	}

	_, err := c.readResponse()
	return err
}

func (c *mcpClient) callTool(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      c.nextID(),
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	}

	if err := c.sendRequest(req); err != nil {
		return nil, err
	}

	resp, err := c.readResponse()
	if err != nil {
		return nil, err
	}

	if result, ok := resp["result"]; ok {
		return result, nil
	}

	return nil, fmt.Errorf("no result in response")
}
