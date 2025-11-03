package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/pkg/journal"
	"github.com/openSUSE/systemd-mcp/internal/pkg/systemd"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

func main() {
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "Systemd connection",
		Version: "0.0.1",
	}, nil)
	systemConn, err := systemd.NewSystem(context.Background())
	if err != nil {
		slog.Warn("couldn't add systemd tools", slog.Any("error", err))
	} else {
		// add systend tool handler
		mcp.AddTool(server, &mcp.Tool{
			Title:       "List units",
			Name:        "list_systemd_units_by_state",
			Description: fmt.Sprintf("List the requested systemd units and services on the host with the given state. Doesn't list the services in other states. As result the unit name, descrition and name are listed as json. Valid states are: %v", systemd.ValidStates()),
		}, systemConn.ListUnitState)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "list_systemd_units_by_name",
			Description: "List the requested systemd unit by it's names or patterns. The output is a json formated with all available non empty fields. This are properites of the unit/service.",
		}, systemConn.ListUnitHandlerNameState)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "restart_reload_unit",
			Description: "Reload or restart a unit or service.",
		}, systemConn.RestartReloadUnit)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "start_reload_unit",
			Description: "Start a unit or service. This doesn't enable the unit.",
		}, systemConn.StartUnit)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "stop_unit",
			Description: "Stop a unit or service or unit.",
		}, systemConn.StopUnit)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "check_restart_reload",
			Description: "Check the reload or restart status of a unit. Can only be called if the restart or reload job had a timeout.",
		}, systemConn.CheckForRestartReloadRunning)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "enable_or_disable_unit",
			Description: "Enable an unit or service for the next startup of the system. This doesn't start the unit.",
		}, systemConn.EnableDisableUnit)
		mcp.AddTool(server, &mcp.Tool{
			Name:        "list_unit_files",
			Description: "Returns a list of all the unit files known to systemd. This tool can be used to determine the correct names for all the other correct unit/service names for the other calls.",
		}, systemConn.ListUnitFiles)
	}
	descriptionJournal := "Get the last log entries for the given service or unit."
	if os.Geteuid() != 0 {
		descriptionJournal += "Please note that this tool is not running as root, so system ressources may not been listed correctly."
	}
	log, err := journal.NewLog()
	if err != nil {
		slog.Warn("couldn't open log, not adding journal tool", slog.Any("error", err))
	} else {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "list_log",
			Description: descriptionJournal,
		}, log.ListLog)
	}
	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		slog.Info("MCP handler listening at", slog.String("address", *httpAddr))
		http.ListenAndServe(*httpAddr, handler)
	} else {
		t := &mcp.LoggingTransport{Transport: &mcp.StdioTransport{}, Writer: os.Stderr}
		if err := server.Run(context.Background(), t); err != nil {
			slog.Error("Server failed", slog.Any("error", err))
		}
	}
	systemConn.Close()
}
