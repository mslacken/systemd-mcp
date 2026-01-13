# Model Context Protocol (MCP) for systemd

The server directly connects to systemd via its C API and so doesn't need systemctl to run.

# Installation

Compile directly with
```
  go build systemd-mcp.go
```
or
```
  make build
```

A manual installation can be done with
```
  cp systemd-mcp /usr/local/bin/systemd-mcp
  cp ./configs/org.opensuse.systemdmcp.conf /etc/dbus-1/system.d/
  cp ./configs/org.opensuse.systemdmcp.policy /etc/polkit-1/actions/
```
or
```
  make install
```

# Security

Interacting with `systemd` requires privileges. `systemd-mcp` supports two authorization modes depending on the transport used.

## Stdio Transport (Polkit/DBus)

When running over Stdio (default), `systemd-mcp` uses `polkit` and `dbus` for authorization.

**Configuration:**
For this to work, the D-Bus and Polkit configuration files must be installed to the system paths:
*   Copy `./configs/org.opensuse.systemdmcp.conf` to `/etc/dbus-1/system.d/`
*   Copy `./configs/org.opensuse.systemdmcp.policy` to `/etc/polkit-1/actions/`

**Privilege Elevation:**
The daemon connects to the system bus. Operations requiring higher privileges (like writing to systemd units) trigger a PolicyKit (polkit) authentication request via D-Bus. If the user is not authorized, the operation will fail or prompt for authentication depending on the environment and policy settings.

## HTTP Transport (OAuth2)

When running over HTTP (using `--http`), `systemd-mcp` uses OAuth2 for authorization.
You must specify an OAuth2 controller address using `--controller` (or `-c`).

**Privilege Elevation:**
Unlike Stdio mode which can use polkit for on-demand elevation, the HTTP server must be started with elevated privileges (e.g., as `root`) to ensure it has direct access to systemd and journal logs.

**OAuth2 Configuration:**
The following values must be configured on your OAuth2 Authorization Server (controller) to match the expected credentials:
*   **Audience**: `systemd-mcp-server`
*   **Supported Scopes**:
    *   `mcp:read`: Allows read-only access (e.g., listing units, reading logs).
    *   `mcp:write`: Allows write access (e.g., starting/stopping units).

# Command-line Options

| Flag                | Shorthand | Description                                                                                             | Default |
|---------------------|-----------|---------------------------------------------------------------------------------------------------------|---------|
| `--http`            |           | If set, use streamable HTTP at this address, instead of stdin/stdout.                                   | `""`      |
| `--controller`      | `-c`      | OAuth2 controller address (required for HTTP mode).                                                     | `""`      |
| `--logfile`         |           | If set, log to this file instead of stderr.                                                             | `""`      |
| `--verbose`         | `-v`      | Enable verbose logging.                                                                                 | `false` |
| `--debug`           | `-d`      | Enable debug logging.                                                                                   | `false` |
| `--log-json`        |           | Output logs in JSON format (machine-readable).                                                          | `false` |
| `--list-tools`      |           | List all available tools and exit.                                                                      | `false` |
| `--allow-write`     | `-w`      | Authorize write to systemd or allow pending write if started without write.                             | `false` |
| `--allow-read`      | `-r`      | Authorize read to systemd or allow pending read if started without read.                                | `false` |
| `--enabled-tools`   |           | A list of tools to enable. Defaults to all tools.                                                       | all     |
| `--timeout`         |           | Set the timeout for authentication in seconds.                                                          | `5`     |
| `--noauth`          |           | Disable authorization via dbus/oauth2 always allow read and write access.                               | `false` |
| `--version`         |           | Print the version and exit.                                                                             | `false` |

# Functionality

Following tools are provided:
* `list_units`: List systemd units. Filter by states (e.g. `running`, `failed`) or patterns. Can return detailed properties. Use `mode='files'` to list all installed unit files.
* `change_unit_state`: Change the state of a unit or service (start, stop, restart, reload, enable, disable).
* `check_restart_reload`: Check the reload or restart status of a unit. Can only be called if the restart or reload job timed out.
* `list_log`: Get the last log entries for the given service or unit.
* `get_file`: Read a file from the system. Can show content and metadata. Supports pagination for large files.
* `get_man_page`: Retrieve a man page. Supports filtering by section and chapters, and pagination.

# Testing

You can test the functions with [mcptools](https://github.com/f/mcptools), with e.g.
```
  mcptools shell go run systemd-mcp.go
```