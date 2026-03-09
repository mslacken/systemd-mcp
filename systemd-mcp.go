package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	_ "embed"

	"github.com/cheynewallace/tabby"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/internal/pkg/file"
	"github.com/openSUSE/systemd-mcp/internal/pkg/journal"
	"github.com/openSUSE/systemd-mcp/internal/pkg/man"
	"github.com/openSUSE/systemd-mcp/internal/pkg/systemd"
	"github.com/openSUSE/systemd-mcp/remoteauth"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	DBusName    = "org.opensuse.systemdmcp"
	DBusPath    = "/org/opensuse/systemdmcp"
	mcpPath     = "/mcp"
	magicNoauth = "ThisIsInsecure"
)

//go:embed VERSION
var version string

func systemdScopes() []string {
	return []string{"mcp:read"}
}

func main() {
	var err error
	// DO NOT SET DEFAULTS HERE
	pflag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
	pflag.Bool("skip-tls-verify", false, "Skip TLS certificate verification for outbound requests (e.g. to OAuth2 controller)")
	pflag.String("logfile", "", "if set, log to this file instead of stderr")
	pflag.String("controller", "", "oauth2 controller address")
	pflag.BoolP("verbose", "v", false, "Enable verbose logging")
	pflag.BoolP("debug", "d", false, "Enable debug logging")
	pflag.Bool("log-json", false, "Output logs in JSON format (machine-readable)")
	pflag.Bool("list-tools", false, "List all available tools and exit")
	pflag.BoolP("allow-write", "w", false, "Authorize write to systemd or allow pending write if started without write")
	pflag.BoolP("allow-read", "r", false, "Authorize read to systemd or allow pending read if started without read")
	pflag.StringSlice("enabled-tools", nil, "A list of tools to enable. Defaults to all tools.")
	pflag.Uint32("timeout", 5, "Set the timeout for authentication in seconds")
	pflag.String("noauth", "", fmt.Sprintf("Disable authorization via dbus/oauth2, this parameter has to be set to %s to work.", magicNoauth))
	pflag.String("cert-file", "", "Path to server certificate file (PEM format) for TLS. Requires --key-file")
	pflag.String("key-file", "", "Path to server private key file (PEM format) for TLS. Requires --cert-file")
	printVersion := pflag.Bool("version", false, "Print the version and exit")
	pflag.Parse()

	if *printVersion {
		fmt.Println(strings.TrimSpace(version))
		os.Exit(0)
	}

	viper.SetEnvPrefix("SYSTEMD_MCP")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)
	logLevel := slog.LevelInfo

	if viper.GetBool("debug") {
		logLevel = slog.LevelDebug
	}
	handlerOpts := &slog.HandlerOptions{
		Level: logLevel,
	}
	var logger *slog.Logger
	logOutput := os.Stderr
	if viper.GetString("logfile") != "" {
		f, err := os.OpenFile(viper.GetString("logfile"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			slog.Error("failed to open log file", "error", err)
			os.Exit(1)
		}
		defer f.Close()
		logOutput = f
	}

	// Choose handler based on format preference
	if viper.GetBool("log-json") {
		logger = slog.New(slog.NewJSONHandler(logOutput, handlerOpts))
	} else {
		logger = slog.New(slog.NewTextHandler(logOutput, handlerOpts))
	}
	slog.SetDefault(logger)
	slog.Debug("Logger initialized", "level", logLevel)

	authorization := &authkeeper.AuthKeeper{}
	if viper.GetString("noauth") == magicNoauth && viper.GetString("controller") == "" {
		authorization, _ = authkeeper.NewNoAuth()
	} else if viper.GetString("http") != "" && viper.GetString("noauth") != magicNoauth {
		if viper.GetString("controller") == "" {
			slog.Error("controller needs to be set when http is set")
			os.Exit(1)
		}
		authorization, err = authkeeper.NewOauth(viper.GetString("controller"), viper.GetBool("skip-tls-verify"))
		if err != nil {
			slog.Error("couldn't create connection to controller", "error", err)
			os.Exit(1)
		}
	} else if viper.GetString("controller") == "" {
		authorization, err = authkeeper.NewPolkitAuth(DBusName, DBusPath)
		if err != nil {
			slog.Error("failed to setup dbus", "error", err)
			os.Exit(1)
		}
		authorization.Timeout = viper.GetUint32("timeout")
		authorization.ReadAllowed = viper.GetBool("allow-read")
		authorization.WriteAllowed = viper.GetBool("allow-write")
	} else {
		slog.Error("http needs either controller or noauth")
		os.Exit(1)
	}
	defer authorization.Close()

	if viper.GetString("http") != "" {
		isKeyFileSet := viper.GetString("key-file") != ""
		isCertFileSet := viper.GetString("cert-file") != ""
		if isKeyFileSet != isCertFileSet {
			if !isKeyFileSet {
				slog.Error("--key-file is required with --cert-file")
			} else {
				slog.Error("--cert-file is required with --key-file")
			}
			os.Exit(1)
		}
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "Systemd connection",
		Version: strings.TrimSpace(version),
	},
		&mcp.ServerOptions{
			InitializedHandler: func(ctx context.Context, req *mcp.InitializedRequest) {
				slog.Debug("Session started", "ID", req.Session.ID())
			},
		})
	systemConn, err := systemd.NewSystem(context.Background(), authorization)
	if err != nil {
		slog.Warn("couldn't add systemd tools", slog.Any("error", err))
	}

	tools := []struct {
		Tool     *mcp.Tool
		Register func(server *mcp.Server, tool *mcp.Tool)
	}{}

	if systemConn != nil {
		defer systemConn.Close()
		tools = append(tools,
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Title:       "List units",
					Name:        "list_units",
					Description: fmt.Sprintf("List systemd units. Filter by states (%v) or patterns. Can return detailed properties. Use mode='files' to list all installed unit files.", systemd.ValidStates()),
					InputSchema: systemd.CreateListUnitsSchema(),
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.ListUnits)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "change_unit_state",
					Description: "Change the state of a unit or service (start, stop, restart, reload, enable, disable).",
					InputSchema: systemd.CreateChangeInputSchema(),
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.ChangeUnitState)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "check_restart_reload",
					Description: "Check the reload or restart status of a unit. Can only be called if the restart or reload job timed out.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.CheckForRestartReloadRunning)
				},
			},
		)
	}
	if journal.CanAccessLogs() {
		log, err := journal.NewLog(authorization)
		if err != nil {
			slog.Warn("couldn't open log, not adding journal tool", slog.Any("error", err))
		} else {
			tools = append(tools, struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "list_log",
					Description: "Get the last log entries for the given service or unit.",
					InputSchema: journal.CreateListLogsSchema(),
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *journal.ListLogParams) (*mcp.CallToolResult, any, error) {
						slog.Debug("list_log called", "args", args)
						res, out, err := log.ListLog(ctx, req, args)
						return res, out, err
					})
				},
			}, struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "get_file",
					Description: "Read a file from the system. Can show content and metadata. Supports pagination for large files.",
					InputSchema: file.CreateFileSchema(),
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *file.GetFileParams) (*mcp.CallToolResult, any, error) {
						slog.Debug("get_file called", "args", args)
						res, out, err := file.GetFile(ctx, req, args)
						return res, out, err
					})
				},
			})
		}
	} else {
		slog.Warn("Couldn't access the logs, removing the tools \"list_log\" and \"get_file\"")
	}
	tools = append(tools, struct {
		Tool     *mcp.Tool
		Register func(server *mcp.Server, tool *mcp.Tool)
	}{
		Tool: &mcp.Tool{
			Name:        "get_man_page",
			Description: "Retrieve a man page. Supports filtering by section and chapters, and pagination.",
			InputSchema: man.CreateManPageSchema(),
		},
		Register: func(server *mcp.Server, tool *mcp.Tool) {
			mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *man.GetManPageParams) (*mcp.CallToolResult, any, error) {
				slog.Debug("get_man_page called", "args", args)
				res, out, err := man.GetManPage(ctx, req, args)
				return res, out, err
			})
		},
	},
	)

	var allTools []string
	for _, tool := range tools {
		allTools = append(allTools, tool.Tool.Name)
	}
	if viper.GetBool("list-tools") {
		if viper.GetBool("verbose") {
			tb := tabby.New()
			tb.AddHeader("TOOL", "DESCRIPTION")
			for _, tool := range tools {
				tb.AddLine(tool.Tool.Name, tool.Tool.Description)
			}
			tb.Print()

		} else {
			fmt.Println(strings.Join(allTools, ","))
		}
		os.Exit(0)
	}
	var enabledTools []string
	if !pflag.CommandLine.Changed("enabled-tools") {
		enabledTools = allTools
	} else {
		enabledTools = viper.GetStringSlice("enabled-tools")
	}
	// register the enabled tools
	for _, tool := range tools {
		if slices.Contains(enabledTools, tool.Tool.Name) {
			tool.Register(server, tool.Tool)
		}
	}

	if httpAddr := viper.GetString("http"); httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		if viper.GetString("noauth") == magicNoauth {
			if viper.GetString("cert-file") == "" {
				slog.Debug("MCP handler listening at", slog.String("address", httpAddr))
				if err := http.ListenAndServe(httpAddr, handler); err != nil {
					slog.Error("couldn't start http server", "error", err)
				}
			} else {
				keyFile := viper.GetString("key-file")
				certFile := viper.GetString("cert-file")
				slog.Debug("MCP handler listening with TLS at", slog.String("address", httpAddr))
				if err := http.ListenAndServeTLS(httpAddr, certFile, keyFile, handler); err != nil {
					slog.Error("couldn't start tls http server", "error", err)
				}
			}
		} else {
			authMiddleware := auth.RequireBearerToken(authorization.Oauth2.VerifyJWT, &auth.RequireBearerTokenOptions{
				Scopes: systemdScopes(),
			})

			loggingMiddleware := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					authHeader := r.Header.Get("Authorization")
					slog.Debug("Received request at MCP endpoint",
						slog.String("path", r.URL.Path),
						slog.String("method", r.Method),
						slog.Bool("has_auth_header", authHeader != ""))
					next.ServeHTTP(w, r)
				})
			}

			http.HandleFunc(mcpPath, loggingMiddleware(authMiddleware(handler)).ServeHTTP)
			// handler for resourceMetaURL
			// TODO: replace with https://github.com/modelcontextprotocol/go-sdk/pull/643 after it's merged
			http.HandleFunc(remoteauth.DefaultProtectedResourceMetadataURI+mcpPath, func(w http.ResponseWriter, r *http.Request) {
				slog.Debug("Client requested OAuth metadata", slog.String("remote_addr", r.RemoteAddr))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Access-Control-Allow-Origin", "*")                     // for mcp-inspector
				w.Header().Set("Access-Control-Allow-Headers", "mcp-protocol-version") // for mcp-inspector
				prm := &oauthex.ProtectedResourceMetadata{
					AuthorizationServers:   []string{viper.GetString("controller")},
					ScopesSupported:        systemdScopes(),
					BearerMethodsSupported: []string{"header"},
					JWKSURI:                authorization.Oauth2.JwksUri,
				}
				slog.Debug("Sending OAuth protected resource metadata", slog.Any("metadata", prm))
				if err := json.NewEncoder(w).Encode(prm); err != nil {
					slog.Error("couldn't encode heaeder", "error", err)
				}
			})

			log.Print("MCP server listening on ", httpAddr+mcpPath)
			s := &http.Server{
				Addr:              httpAddr,
				ReadHeaderTimeout: 3 * time.Second,
			}
			if viper.GetString("cert-file") == "" {
				if err := s.ListenAndServe(); err != nil {
					slog.Error("couldn't start http server", "error", err)
				}
			} else {
				keyFile := viper.GetString("key-file")
				certFile := viper.GetString("cert-file")
				if err := s.ListenAndServeTLS(certFile, keyFile); err != nil {
					slog.Error("couldn't start tls http server", "error", err)
				}
			}
		}
	} else {
		slog.Debug("New client has connected via stdin/stdout")
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			slog.Error("Server failed", slog.Any("error", err))
		}
	}
}
