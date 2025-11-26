package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/cheynewallace/tabby"
	godbus "github.com/godbus/dbus/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/internal/pkg/journal"
	"github.com/openSUSE/systemd-mcp/internal/pkg/man"
	"github.com/openSUSE/systemd-mcp/internal/pkg/systemd"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	DBusName = "org.opensuse.systemdmcp"
	DBusPath = "/org/opensuse/systemdmcp"
)

func main() {
	// DO NOT SET DEFAULTS HERE
	pflag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
	pflag.String("logfile", "", "if set, log to this file instead of stderr")
	pflag.BoolP("verbose", "v", false, "Enable verbose logging")
	pflag.BoolP("debug", "d", false, "Enable debug logging")
	pflag.Bool("log-json", false, "Output logs in JSON format (machine-readable)")
	pflag.Bool("list-tools", false, "List all available tools and exit")
	pflag.BoolP("allow-write", "w", false, "Authorize write to systemd or allow pending write if started without write")
	pflag.BoolP("allow-read", "r", false, "Authorize read to systemd or allow pending read if started without read")
	pflag.BoolP("auth-register", "a", false, "Register for auth call backs")
	pflag.StringSlice("enabled-tools", nil, "A list of tools to enable. Defaults to all tools.")
	pflag.Uint32("timeout", 5, "Set the timeout for authentication in seconds")
	pflag.Bool("noauth", false, "Disable authorization via dbus and always allow read and write access")
	pflag.Bool("internal-agent", false, "Starts pkttyagent for authorization")

	pflag.Parse()
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

	if (viper.GetBool("allow-read") || viper.GetBool("allow-write") || viper.GetBool("auth-register") || viper.GetBool("internal-agent")) && !viper.GetBool("noauth") && viper.GetString("http") == "" {
		taken, err := dbus.IsDBusNameTaken(DBusName)
		if err != nil {
			slog.Error("could not check if dbus name is taken", "error", err)
			os.Exit(1)
		}
		if taken {
			conn, err := godbus.ConnectSystemBus()
			if err != nil {
				slog.Error("failed to connect to system bus", "error", err)
				os.Exit(1)
			}
			defer conn.Close()

			obj := conn.Object(DBusName, DBusPath)
			if viper.GetBool("allow-read") {
				call := obj.Call(DBusName+".AuthRead", 0)
				if call.Err != nil {
					slog.Error("failed to authorize read", "error", call.Err)
					os.Exit(1)
				}
				slog.Info("Read access authorized")
			}
			if viper.GetBool("allow-write") {
				call := obj.Call(DBusName+".AuthWrite", 0)
				if call.Err != nil {
					slog.Error("failed to authorize write", "error", call.Err)
					os.Exit(1)
				}
				slog.Info("Write access authorized")
			}
			if viper.GetBool("auth-register") || viper.GetBool("internal-agent") {
				call := obj.Call(DBusName+".AuthRegister", 0)
				if call.Err != nil {
					slog.Error("failed to register for auth call backs", "error", call.Err)
					os.Exit(1)
				}
				slog.Info("Registered for auth call backs")
			}
			if viper.GetBool("internal-agent") {
				cmd := exec.Command("pkttyagent", "--process", fmt.Sprintf("%d", os.Getpid()))
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Stdin = os.Stdin
				if err := cmd.Run(); err != nil {
					slog.Error("pkttyagent failed", "error", err)
					os.Exit(1)
				}
			} else {
				slog.Info("Press Ctrl+C to exit and cancel authorizations.")
				sigs := make(chan os.Signal, 1)
				signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
				<-sigs
			}
			os.Exit(0)
		}
	}
	AuthKeeper := &dbus.AuthKeeper{}
	if !viper.GetBool("noauth") {
		var err error
		AuthKeeper, err = dbus.SetupDBus(DBusName, DBusPath)
		if err != nil {
			slog.Error("failed to setup dbus", "error", err)
			os.Exit(1)
		}
		AuthKeeper.Timeout = viper.GetUint32("timeout")
		AuthKeeper.ReadAllowed = viper.GetBool("allow-read")
		AuthKeeper.WriteAllowed = viper.GetBool("allow-write")
		defer AuthKeeper.Close()
	} else {
		AuthKeeper.ReadAllowed = true
		AuthKeeper.WriteAllowed = true
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "Systemd connection",
		Version: "0.1.0",
	}, nil)
	systemConn, err := systemd.NewSystem(context.Background(), AuthKeeper)
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
	descriptionJournal := "Get the last log entries for the given service or unit."
	log, err := journal.NewLog(AuthKeeper)
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
				InputSchema: journal.CreateListLogsSchema(),
			},
			Register: func(server *mcp.Server, tool *mcp.Tool) {
				mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args *journal.ListLogParams) (*mcp.CallToolResult, any, error) {
					slog.Debug("list_log called", "args", args)
					res, out, err := log.ListLog(ctx, req, args)
					return res, out, err
				})
			},
		})
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
	})

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
