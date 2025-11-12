package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/cheynewallace/tabby"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/dbus"
	"github.com/openSUSE/systemd-mcp/internal/pkg/journal"
	"github.com/openSUSE/systemd-mcp/internal/pkg/systemd"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func main() {
	// DO NOT SET DEFAULTS HERE
	pflag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
	pflag.String("logfile", "", "if set, log to this file instead of stderr")
	pflag.BoolP("verbose", "v", false, "Enable verbose logging")
	pflag.BoolP("debug", "d", false, "Enable debug logging")
	pflag.Bool("log-json", false, "Output logs in JSON format (machine-readable)")
	pflag.Bool("list-tools", false, "List all available tools and exit")
	pflag.StringSlice("enabled-tools", nil, "A list of tools to enable. Defaults to all tools.")

	pflag.Parse()
	viper.SetEnvPrefix("SYSTEMD_MCP")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)

	AuthKeeper, err := dbus.SetupDBus()
	if err != nil {
		slog.Warn("failed to setup dbus, continuing without it", "error", err)
	}
	if AuthKeeper != nil {
		defer AuthKeeper.Close()
	}

	logLevel := slog.LevelInfo
	if viper.GetBool("verbose") {
		logLevel = slog.LevelInfo
	}
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

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "Systemd connection",
		Version: "0.0.1",
	}, nil)
	systemConn, err := systemd.NewSystem(context.Background())
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
					Name:        "list_systemd_units_by_state",
					Description: fmt.Sprintf("List the requested systemd units and services on the host with the given state. Does not list the services in other states. As a result the unit name, description and name are listed as json. Valid states are: %v", systemd.ValidStates()),
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.ListUnitState)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "list_systemd_units_by_name",
					Description: "List the requested systemd unit by its names or patterns. The output is a JSON formatted with all available non-empty fields. These are properties of the unit/service.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.ListUnitHandlerNameState)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "restart_reload_unit",
					Description: "Reload or restart a unit or service.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.RestartReloadUnit)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "start_reload_unit",
					Description: "Start a unit or service. This doesn't enable the unit.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.StartUnit)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "stop_unit",
					Description: "Stop a unit or service or unit.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.StopUnit)
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
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "enable_or_disable_unit",
					Description: "Enable a unit or service for the next startup of the system. This does not start the unit.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.EnableDisableUnit)
				},
			},
			struct {
				Tool     *mcp.Tool
				Register func(server *mcp.Server, tool *mcp.Tool)
			}{
				Tool: &mcp.Tool{
					Name:        "list_unit_files",
					Description: "Returns a list of all the unit files known to systemd. This tool can be used to determine the correct unit/service names for other calls.",
				},
				Register: func(server *mcp.Server, tool *mcp.Tool) {
					mcp.AddTool(server, tool, systemConn.ListUnitFiles)
				},
			},
		)
	}
	descriptionJournal := "Get the last log entries for the given service or unit."
	if os.Geteuid() != 0 {
		descriptionJournal += "Please note that this tool is not running as root, so system resources may not be listed correctly."
	}
	log, err := journal.NewLog()
	if err != nil {
		slog.Warn("couldn't open log, not adding journal tool", slog.Any("error", err))
	} else {
		tools = append(tools, struct {
			Tool     *mcp.Tool
			Register func(server *mcp.Server, tool *mcp.Tool)
		}{
			Tool: &mcp.Tool{
				Name:        "list_log",
				Description: descriptionJournal,
			},
			Register: func(server *mcp.Server, tool *mcp.Tool) {
				mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *journal.ListLogParams) (*mcp.CallToolResult, any, error) {
					slog.Debug("list_log called", "args", args)
					return log.ListLog(ctx, req, args)
				})
			},
		})
	}
	if AuthKeeper != nil {
		tools = append(tools, struct {
			Tool     *mcp.Tool
			Register func(server *mcp.Server, tool *mcp.Tool)
		}{
			Tool: &mcp.Tool{
				Name:        "is_read_authorized",
				Description: "Checks if read access is authorized via Polkit.",
			},
			Register: func(server *mcp.Server, tool *mcp.Tool) {
				mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *dbus.ReadAuthArgs) (*mcp.CallToolResult, any, error) {
					slog.Debug("is_read_authorized called", "args", args)
					return AuthKeeper.IsReadAuthorized(ctx, req, args)
				})
			},
		})
		tools = append(tools, struct {
			Tool     *mcp.Tool
			Register func(server *mcp.Server, tool *mcp.Tool)
		}{
			Tool: &mcp.Tool{
				Name:        "is_write_authorized",
				Description: "Checks if read access is authorized via Polkit.",
			},
			Register: func(server *mcp.Server, tool *mcp.Tool) {
				mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *dbus.ReadAuthArgs) (*mcp.CallToolResult, any, error) {
					slog.Debug("is_write_authorized called", "args", args)
					return AuthKeeper.IsWriteAuthorized(ctx, req, args)
				})
			},
		})
	}

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

	if viper.GetString("http") != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		slog.Info("MCP handler listening at", slog.String("address", viper.GetString("http")))
		http.ListenAndServe(viper.GetString("http"), handler)
	} else {
		slog.Info("New client has connected via stdin/stdout")
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			slog.Error("Server failed", slog.Any("error", err))
		}
	}
}
