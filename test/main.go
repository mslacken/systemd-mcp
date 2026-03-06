package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

var (
	endpoint     string
	token        string
	debug        bool
	interactive  bool
	callbackHost string
	kcURL        string
	kcUser       string
	kcPass       string
	kcClient     string
)

func debugLog(format string, a ...interface{}) {
	if debug {
		fmt.Printf("[DEBUG] "+format+"\n", a...)
	}
}

func discoverKeycloakURL() (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	metaURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", u.Scheme, u.Host, u.Path)
	debugLog("Fetching metadata from %s", metaURL)

	resp, err := http.Get(metaURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata endpoint returned status %d", resp.StatusCode)
	}

	var meta struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}

	if len(meta.AuthorizationServers) == 0 {
		return "", fmt.Errorf("no authorization servers found in metadata")
	}

	serverURL := meta.AuthorizationServers[0]
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}

	debugLog("Discovered Keycloak Server URL: %s", serverURL)
	return strings.TrimRight(serverURL, "/"), nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}

func doInteractiveLogin(serverURL string) (string, error) {
	authURL := strings.TrimRight(serverURL, "/") + "/protocol/openid-connect/auth"
	tokenURL := strings.TrimRight(serverURL, "/") + "/protocol/openid-connect/token"

	listener, err := net.Listen("tcp", callbackHost+":0")
	if err != nil {
		return "", err
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://%s:%d/callback", callbackHost, port)

	conf := &oauth2.Config{
		ClientID:    kcClient,
		RedirectURL: redirectURL,
		Scopes:      []string{"openid", "systemd-audience", "mcp:read", "mcp:write"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}

	state := "random-state-string"
	url := conf.AuthCodeURL(state)

	fmt.Printf("\nOpening browser to login...\nIf it doesn't open automatically, please click this link:\n%s\n\n", url)
	openBrowser(url)

	codeCh := make(chan string)
	errCh := make(chan error)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("invalid state")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("missing code")
			return
		}
		fmt.Fprintf(w, "Login successful. You can close this window and return to the terminal.")
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case code := <-codeCh:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer srv.Shutdown(ctx)
		tok, err := conf.Exchange(ctx, code)
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	case err := <-errCh:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer srv.Shutdown(ctx)
		return "", err
	case <-time.After(3 * time.Minute):
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer srv.Shutdown(ctx)
		return "", fmt.Errorf("login timed out")
	}
}

func getTokenFromKeycloak() (string, error) {
	targetURL := kcURL
	if targetURL == "" {
		discovered, err := discoverKeycloakURL()
		if err != nil {
			debugLog("Could not discover Keycloak URL: %v", err)
			return "", nil
		}
		targetURL = discovered
	}

	if interactive {
		return doInteractiveLogin(targetURL)
	}

	tokenURL := strings.TrimRight(targetURL, "/") + "/protocol/openid-connect/token"
	debugLog("Fetching token from Keycloak at %s", tokenURL)
	form := url.Values{}
	form.Add("client_id", kcClient)
	form.Add("username", kcUser)
	form.Add("password", kcPass)
	form.Add("grant_type", "password")
	form.Add("scope", "openid systemd-audience mcp:read mcp:write")

	reqToken, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	reqToken.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	respToken, err := client.Do(reqToken)
	if err != nil {
		return "", err
	}
	defer respToken.Body.Close()

	if respToken.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respToken.Body)
		return "", fmt.Errorf("failed to get token, status: %d, body: %s", respToken.StatusCode, string(b))
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(respToken.Body).Decode(&tokenResponse); err != nil {
		return "", err
	}

	debugLog("Successfully retrieved token from Keycloak")
	return tokenResponse.AccessToken, nil
}

// headerTransport is a custom http.RoundTripper that injects headers
type headerTransport struct {
	Transport http.RoundTripper
	Header    http.Header
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	debugLog("Sending request to %s", req.URL.String())
	for k, v := range t.Header {
		req.Header[k] = v
		debugLog("Setting header %s: %s", k, v)
	}
	return t.Transport.RoundTrip(req)
}

func createClient() (*mcp.Client, *mcp.ClientSession, error) {
	debugLog("Creating MCP client for endpoint: %s", endpoint)

	if token == "" {
		fetchedToken, err := getTokenFromKeycloak()
		if err != nil {
			return nil, nil, fmt.Errorf("error fetching token: %v", err)
		}
		if fetchedToken != "" {
			token = fetchedToken
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
		debugLog("Using provided bearer token")
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "cli-test-client", Version: "1.0.0"}, nil)
	mcpSession, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: &headerTransport{
				Transport: http.DefaultTransport,
				Header:    header,
			},
			Timeout: 10 * time.Second,
		},
	}, nil)

	if err != nil {
		debugLog("Failed to connect: %v", err)
	} else {
		debugLog("Connected to MCP server")
	}

	return mcpClient, mcpSession, err
}

func getTools() []*mcp.Tool {
	debugLog("Fetching tools...")
	_, session, err := createClient()
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		fmt.Printf("Failed to list tools: %v\n", err)
		os.Exit(1)
	}

	debugLog("Successfully fetched %d tools", len(result.Tools))
	return result.Tools
}

func runTool(name string, args map[string]interface{}) {
	debugLog("Running tool: %s with args: %+v", name, args)
	_, session, err := createClient()
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})

	if err != nil {
		fmt.Printf("Error calling tool: %v\n", err)
		os.Exit(1)
	}

	debugLog("Tool execution completed, IsError: %v", result.IsError)

	if result.IsError {
		fmt.Println("Tool returned an error:")
	}

	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			fmt.Println(tc.Text)
		} else {
			b, _ := json.MarshalIndent(c, "", "  ")
			fmt.Println(string(b))
		}
	}
}

func printToolSchema(tool *mcp.Tool) {
	var schema map[string]interface{}
	schemaBytes, _ := json.Marshal(tool.InputSchema)
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return
	}

	fmt.Println("  Arguments:")
	requiredFields, _ := schema["required"].([]interface{})
	exampleArgs := make(map[string]interface{})

	for propName, propDetails := range properties {
		details, _ := propDetails.(map[string]interface{})
		isRequired := false
		for _, r := range requiredFields {
			if r == propName {
				isRequired = true
				break
			}
		}

		desc := details["description"]
		if desc == nil {
			desc = ""
		}

		optionalStr := ""
		if !isRequired {
			optionalStr = " (optional)"
		}
		fmt.Printf("    - %s%s: %v\n", propName, optionalStr, desc)

		// Build a simple example with required fields
		if isRequired {
			if enum, ok := details["enum"].([]interface{}); ok && len(enum) > 0 {
				exampleArgs[propName] = enum[0]
			} else {
				exampleArgs[propName] = "value"
			}
		}
	}
	if len(exampleArgs) > 0 {
		exampleJSON, _ := json.Marshal(exampleArgs)
		fmt.Printf("  Example: %s\n", string(exampleJSON))
	} else if len(properties) > 0 {
		fmt.Printf("  Example: {}\n")
	}
}

func interactiveMenu() {
	fmt.Println("Connecting to MCP server and fetching tools...")
	tools := getTools()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Println("\n--- Available Tools ---")
		for i, t := range tools {
			fmt.Printf("%d. %s - %s\n", i+1, t.Name, t.Description)
		}
		fmt.Println("0. Quit")
		fmt.Printf("Select a tool (0-%d): ", len(tools))

		if !scanner.Scan() {
			break
		}
		choice := strings.TrimSpace(scanner.Text())

		if choice == "0" || choice == "q" || choice == "quit" {
			break
		}

		idx, err := strconv.Atoi(choice)
		if err != nil || idx < 1 || idx > len(tools) {
			fmt.Println("Invalid choice. Please try again.")
			continue
		}

		selectedTool := tools[idx-1]
		fmt.Printf("\nSelected tool: %s\n", selectedTool.Name)
		fmt.Printf("Description: %s\n", selectedTool.Description)

		printToolSchema(selectedTool)

		fmt.Print("\nEnter arguments as JSON object or press Enter for no arguments: ")
		if !scanner.Scan() {
			break
		}
		argsStr := strings.TrimSpace(scanner.Text())
		
		var args map[string]interface{}
		if argsStr != "" {
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
				fmt.Printf("Error parsing JSON: %v\n", err)
				continue
			}
		}

		fmt.Printf("Calling %s...\n", selectedTool.Name)
		runTool(selectedTool.Name, args)
	}
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "mcp-client",
		Short: "A test client for systemd-mcp",
		Run: func(cmd *cobra.Command, args []string) {
			interactiveMenu()
		},
	}

	rootCmd.PersistentFlags().StringVarP(&endpoint, "endpoint", "e", "http://localhost:8080/mcp", "MCP server endpoint")
	rootCmd.PersistentFlags().StringVarP(&token, "token", "t", "", "Bearer token for authentication")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	rootCmd.PersistentFlags().BoolVarP(&interactive, "interactive", "i", false, "Use interactive browser login instead of username/password")
	rootCmd.PersistentFlags().StringVar(&callbackHost, "callback-host", "127.0.0.1", "Hostname to bind the interactive login callback to")

	rootCmd.PersistentFlags().StringVar(&kcURL, "kc-url", "", "Keycloak Server URL (e.g. http://localhost:8880/realms/mcp-realm)")
	rootCmd.PersistentFlags().StringVar(&kcUser, "kc-user", "mcp-user", "Keycloak username (ignored if --interactive is used)")
	rootCmd.PersistentFlags().StringVar(&kcPass, "kc-pass", "user123", "Keycloak password (ignored if --interactive is used)")
	rootCmd.PersistentFlags().StringVar(&kcClient, "kc-client", "systemd-mcp", "Keycloak client ID")

	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List all available tools",
		Run: func(cmd *cobra.Command, args []string) {
			tools := getTools()
			for _, t := range tools {
				fmt.Printf("- %s: %s\n", t.Name, t.Description)
				printToolSchema(t)
				fmt.Println()
			}
		},
	}

	var runCmd = &cobra.Command{
		Use:   "run [tool_name]",
		Short: "Run a specific tool",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			toolName := args[0]
			argsJSON, _ := cmd.Flags().GetString("args")

			var parsedArgs map[string]interface{}
			if argsJSON != "" {
				if err := json.Unmarshal([]byte(argsJSON), &parsedArgs); err != nil {
					fmt.Printf("Error parsing arguments JSON: %v\n", err)
					os.Exit(1)
				}
			}

			runTool(toolName, parsedArgs)
		},
	}
	runCmd.Flags().StringP("args", "a", "", "Arguments as JSON string. Use 'list' to see detailed arguments and examples for each tool.")

	rootCmd.AddCommand(listCmd, runCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
