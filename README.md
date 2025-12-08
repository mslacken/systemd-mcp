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

# Installation

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

Interacting with `systemd` requires root privileges. `systemd-mcp` is designed with a security model based on `polkit` to control access to potentially dangerous operations.

## Authorization Flow

1.  **Privilege Escalation**: When you start `systemd-mcp`, it will check if it is running as root. If not, it will use `pkexec` to request administrator privileges. You will be prompted for your password to allow the application to run as root.

2.  **Restricted by Default**: Once running as root, the daemon starts in a restricted mode. By default, it is not allowed to perform read or write operations on `systemd`.

3.  **Granting Permissions**: To grant permissions, you need to run a second `systemd-mcp` command in another terminal.
    *   To receive authorization prompts for operations, run:
        ```
        systemd-mcp --auth-register
        ```
        This will register a process to handle authorization requests from the main daemon. When a tool needs permissions, a `polkit` dialog will appear asking for your confirmation. You should keep this terminal window open.
    *  On `ssh` sessions, you can use the `--internal-agent` flag which is a convenience wrapper around `--auth-register` and `pkttyagent`.

4.  **Pre-authorizing Permissions**: You can also pre-authorize permissions when starting the daemon, or for a daemon that is already running:
    *   To start the daemon with read access pre-authorized: `systemd-mcp --allow-read`
    *   To start the daemon with write access pre-authorized: `systemd-mcp --allow-write`
    *   To grant read access to an already running daemon: `systemd-mcp --allow-read`
    *   To grant write access to an already running daemon: `systemd-mcp --allow-write`

5.  **Disabling Authorization**: For development or in trusted environments, you can disable the `polkit` authorization entirely:
    ```
    systemd-mcp --noauth
    ```
    > [!CAUTION]
    > Using `--noauth` gives any client with access to `systemd-mcp` full control over `systemd` as root. Use this with extreme caution.

# Command-line Options

| Flag                | Shorthand | Description                                                                                             | Default |
|---------------------|-----------|---------------------------------------------------------------------------------------------------------|---------|
| `--http`            |           | If set, use streamable HTTP at this address, instead of stdin/stdout.                                   | `""`      |
| `--logfile`         |           | If set, log to this file instead of stderr.                                                             | `""`      |
| `--verbose`         | `-v`      | Enable verbose logging.                                                                                 | `false` |
| `--debug`           | `-d`      | Enable debug logging.                                                                                   | `false` |
| `--log-json`        |           | Output logs in JSON format (machine-readable).                                                          | `false` |
| `--list-tools`      |           | List all available tools and exit.                                                                      | `false` |
| `--allow-write`     | `-w`      | Authorize write access to systemd. Can be used when starting the daemon or to authorize a running daemon. | `false` |
| `--allow-read`      | `-r`      | Authorize read access to systemd. Can be used when starting the daemon or to authorize a running daemon.  | `false` |
| `--auth-register`   | `-a`      | Register to handle authorization requests from a running daemon via polkit.                               | `false` |
| `--internal-agent`  |           | Starts `pkttyagent` to handle authorization requests. A convenience wrapper around `--auth-register`.     | `false` |
| `--enabled-tools`   |           | A comma-separated list of tools to enable.                                                              | all     |
| `--timeout`         |           | Set the timeout for authentication in seconds.                                                          | `5`     |
| `--noauth`          |           | Disable `polkit` authorization and always allow read and write access.                                  | `false` |

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
