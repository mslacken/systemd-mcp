package dbus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// gets executable for nicer error messages
func getExecutableName() string {
	exe, err := os.Executable()
	if err != nil {
		slog.Error("could not determine executable name", "error", err)
		return "systemd-mcp" // Fallback name
	}
	return filepath.Base(exe)
}

// IsDBusNameTaken checks if the dbus name is already taken.
func IsDBusNameTaken(dbusName string) (bool, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return false, fmt.Errorf("could not connect to system dbus: %w", err)
	}
	defer conn.Close()

	var names []string
	err = conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names)
	if err != nil {
		return false, err
	}

	for _, n := range names {
		if n == dbusName {
			return true, nil
		}
	}

	return false, nil
}

type AuthKeeper struct {
	*dbus.Conn
	sender             dbus.Sender // store the sender which authorized the last call
	Timeout            uint32
	ReadAlwaysAllowed  bool // allow read without auth
	WriteAlwaysAllowed bool // allow write without auth
	ReadAllowed        bool // read was allowed by user
	WriteAllowed       bool // write was allowed by user
	DbusName           string
	DbusPath           string
}

func (a *AuthKeeper) checkDbusAuth(conn *dbus.Conn, sender dbus.Sender, actionID string) (bool, *dbus.Error) {
	// not sensible to ask root for auth
	if os.Geteuid() == 0 {
		return true, nil
	}
	subject := struct {
		A string
		B map[string]dbus.Variant
	}{
		"system-bus-name",
		map[string]dbus.Variant{
			"name": dbus.MakeVariant(string(sender)),
		},
	}
	slog.Debug("checking auth", "subject", subject, "actionID", actionID)
	details := make(map[string]string)
	if os.Geteuid() == 0 && (strings.Contains(actionID, "AuthWrite") || strings.Contains(actionID, "AuthRead")) {
		details["polkit.message"] = "Don't touch the blinkenlights"
	}
	flags := uint32(1) // AllowUserInteraction
	cancellationID := ""
	var result struct {
		IsAuthorized bool
		IsChallenge  bool
		Details      map[string]dbus.Variant
	}

	pkObj := conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
	err := pkObj.Call("org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
		subject, actionID, details, flags, cancellationID).Store(&result)

	if err != nil {
		slog.Error("error checking authorization", "error", err)
		return false, &dbus.Error{
			Name: "org.opensuse.systemdmcp.Error.AuthError",
			Body: []interface{}{"Error checking authorization: " + err.Error()},
		}
	}
	slog.Debug("authorization result", "result", result)
	if !result.IsAuthorized {
		return false, &dbus.Error{
			Name: "org.freedesktop.DBus.Error.AccessDenied",
			Body: []interface{}{"Authorization denied."},
		}
	}

	return true, nil
}

// read authorization method exposed to dbus
func (a *AuthKeeper) AuthRead(sender dbus.Sender) *dbus.Error {
	_, err := a.checkDbusAuth(a.Conn, sender, a.DbusName+".AuthRead")
	if err == nil {
		a.sender = sender
	}
	return err
}

// write authorization method exposed to dbus
func (a *AuthKeeper) AuthWrite(sender dbus.Sender) *dbus.Error {
	_, err := a.checkDbusAuth(a.Conn, sender, a.DbusName+".AuthWrite")
	if err == nil {
		a.sender = sender
	}
	return err
}

// Just register the sender for further call backs
func (a *AuthKeeper) AuthRegister(sender dbus.Sender) *dbus.Error {
	a.sender = sender
	return nil
}

func getSessionIdFromPid(pid uint32) (string, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Look for line like: 0::/user.slice/user-1000.slice/session-3.scope
		if strings.Contains(line, "session-") && strings.HasSuffix(line, ".scope") {
			parts := strings.Split(line, "/")
			for _, p := range parts {
				if strings.HasPrefix(p, "session-") && strings.HasSuffix(p, ".scope") {
					return strings.TrimSuffix(strings.TrimPrefix(p, "session-"), ".scope"), nil
				}
			}
		}
	}
	return "", fmt.Errorf("session scope not found in cgroup for pid %d", pid)
}

func getSessionID(conn *dbus.Conn) (string, error) {
	var seats []struct {
		Name string
		Path dbus.ObjectPath
	}
	err := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.ListSeats", 0).
		Store(&seats)
	if err != nil {
		return "", fmt.Errorf("failed to list seats: %w", err)
	}

	if len(seats) == 0 {
		return "", fmt.Errorf("no seats found")
	}
	slog.Debug("active seats", "seats", seats)
	for _, seat := range seats {
		// ActiveSession property is (so) - (session_id, session_object_path)
		variant, err := conn.Object("org.freedesktop.login1", seat.Path).
			GetProperty("org.freedesktop.login1.Seat.ActiveSession")
		if err != nil {
			slog.Debug("failed to get ActiveSession property", "seat", seat.Name, "error", err)
			continue
		}

		// godbus decodes structs to []interface{} by default in variants
		val, ok := variant.Value().([]interface{})
		if !ok || len(val) < 2 {
			continue
		}

		sessionID, ok := val[0].(string)
		if !ok || sessionID == "" {
			continue
		}

		return sessionID, nil
	}

	return "", fmt.Errorf("no active session found on any seat")
}

// IsLocal checks if the current session is local.
func (a *AuthKeeper) IsLocal() bool {
	sessionID, err := getSessionID(a.Conn)
	if err != nil {
		slog.Error("could not get session ID for IsLocal check", "error", err)
		return false
	}

	var sessionPath dbus.ObjectPath
	err = a.Conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.GetSession", 0, sessionID).
		Store(&sessionPath)
	if err != nil {
		slog.Error("failed to get session path", "sessionID", sessionID, "error", err)
		return false
	}

	variant, err := a.Conn.Object("org.freedesktop.login1", sessionPath).
		GetProperty("org.freedesktop.login1.Session.Remote")
	if err != nil {
		slog.Error("failed to get Remote property for session", "sessionID", sessionID, "error", err)
		return false
	}

	isRemote, ok := variant.Value().(bool)
	if !ok {
		slog.Error("Remote property is not a boolean", "sessionID", sessionID)
		return false
	}

	slog.Debug("session", "remote", isRemote)
	return !isRemote
}

// Deauthorize revokes the authorization
func (a *AuthKeeper) Deauthorize() *dbus.Error {
	slog.Debug("Deauthorize called")
	/*
		a.WriteAlwaysAllowed = false
		if a.sender != "" {
			sessionID, err := getSessionID(a.Conn)
			if err != nil {
				slog.Error("could not get session ID for deauthorization", "error", err)
			}

			var subject interface{}
			subject = struct {
				A string
				B map[string]dbus.Variant
			}{
				"unix-session",
				map[string]dbus.Variant{
					"session-id": dbus.MakeVariant(sessionID),
				},
			}

			slog.Debug("revoking auth", "subject", subject)

			pkObj := a.Conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
			err = pkObj.Call("org.freedesktop.PolicyKit1.Authority.RevokeTemporaryAuthorizations", 0,
				subject).Store()

			if err != nil {
				// Log warning but do not fail the operation; revocation is best-effort
				slog.Warn("error revoking polkit authorization (this is expected if systemd-mcp is running as a system service)", "error", err)
			}
			a.sender = ""
		}
	*/
	return nil
}

// setup the dbus authorization call back. Creates AuthWrite and
// AuthRead dbus methods so that authorization can be done by
// another process calliing this methods.
func SetupDBus(dbusName, dbusPath string) (*AuthKeeper, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Warn("could not connect to system dbus", "error", err)
		return nil, err
	}

	keeper := &AuthKeeper{
		Conn:     conn,
		Timeout:  30,
		DbusName: dbusName,
		DbusPath: dbusPath,
	}

	intro := `
<node>
	<interface name="` + dbusName + `">
		<method name="AuthRead">
		</method>
		<method name="AuthWrite">
		</method>
		<method name="AuthRegister">
		</method>
		<method name="Deauthorize">
		</method>
</interface>` + introspect.IntrospectDataString + `</node> `

	conn.Export(keeper, dbus.ObjectPath(dbusPath), dbusName)
	conn.Export(introspect.Introspectable(intro), dbus.ObjectPath(dbusPath), "org.freedesktop.DBus.Introspectable")

	reply, err := conn.RequestName(dbusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		slog.Warn("could not request dbus name", "error", err)
		conn.Close()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		slog.Warn("dbus name already taken", "name", dbusName)
		conn.Close()
		return nil, fmt.Errorf("dbus name already taken")
	}
	slog.Debug("Listening on dbus", "name", dbusName)
	return keeper, nil
}

// Check if read was authorized. Triggers also a call back via
// dbus if read was authorized at another time
func (a *AuthKeeper) IsReadAuthorized() (bool, error) {
	if a.ReadAllowed {
		return true, nil
	}
	slog.Debug("checking read auth", "address", a.sender)

	// would always succeed if root so skip for root
	if a.sender == "" && a.IsLocal() && os.Geteuid() != 0 {
		err := a.Conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, a.DbusName).Store(&a.sender)
		if err != nil {
			return false, fmt.Errorf("could not get unique name for self: %w", err)

		}
		slog.Debug("name owner", "sender", a.sender)
	} else if !a.IsLocal() {
		return false, fmt.Errorf("read to systemd must authorized externally")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.Timeout)*time.Second)
	defer cancel()

	state, err := a.checkDbusAuth(a.Conn, a.sender, a.DbusName+".AuthRead")
	if err != nil {
		return false, fmt.Errorf("couldn't get read authorization: %s", err)
	}
	/*
		state, err = a.checkDbusAuth(a.Conn, a.sender, "org.freedesktop.systemd1.manage-units")
		if err != nil {
			return false, fmt.Errorf("couldn't get a: %s", err)
		}
	*/
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("write authorization timed out: %w", ctx.Err())
	default:
		return state, nil
	}
}

// Check if write was authorized. Triggers also a call back via
// dbus if write was authorized at another time
func (a *AuthKeeper) IsWriteAuthorized(systemdPermission string) (bool, error) {
	if a.WriteAllowed {
		return true, nil
	}
	slog.Debug("checking write auth", "sender", a.sender)
	// would always succeed if root so skip for root
	if a.sender == "" && a.IsLocal() && os.Geteuid() != 0 {
		err := a.Conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, a.DbusName).Store(&a.sender)
		if err != nil {
			return false, fmt.Errorf("could not get unique name for self: %w", err)

		}
	} else if !a.IsLocal() {
		return false, fmt.Errorf("write to systemd must authorized externally")
	}
	slog.Debug("dbus sender", "address", a.sender)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.Timeout)*time.Second)
	defer cancel()

	state, dbuserr := a.checkDbusAuth(a.Conn, a.sender, a.DbusName+".AuthWrite")
	if dbuserr != nil {
		if systemdPermission == "" {
			systemdPermission = "org.freedesktop.systemd1.manage-units"
		}
		state, dbuserr = a.checkDbusAuth(a.Conn, a.sender, systemdPermission)
		if dbuserr != nil {
			return false, fmt.Errorf("authorization error: %s", dbuserr)
		}
	}

	select {
	case <-ctx.Done():
		return false, fmt.Errorf("write authorization timed out: %w", ctx.Err())
	default:
		return state, nil
	}
	// return false, fmt.Errorf("authorize writing by calling: %s --allow-write", getExecutableName())
}

// Check if write was authorized for the process itself.
/*
func (a *AuthKeeper) IsAuthorizedSelf(actionID string) (bool, error) {
	if os.Getuid() == 0 {
		return true, nil
	}
	if a.WriteAlwaysAllowed {
		return true, nil
	}
	targetAction := actionID
	if targetAction == "" {
		targetAction = a.DbusName + ".AuthWrite"
	}
	slog.Debug("checking write auth self", "action", targetAction)

	var uniqueName string
	err := a.Conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, a.DbusName).Store(&uniqueName)
	if err != nil {
		return false, fmt.Errorf("could not get unique name for self: %w", err)
	}

	state, dbuserr := a.checkDbusAuth(a.Conn, dbus.Sender(uniqueName), targetAction)
	if dbuserr != nil {
		return false, fmt.Errorf("authorization error: %s", dbuserr)
	}
	return state, nil
}
*/
type ReadAuthArgs struct{}

type IsAuthorizedResult struct {
	SenderAuth bool `json:"sender_auth"`
}

// debug function to check authorization
func (a *AuthKeeper) IsReadAuthorizedTool(ctx context.Context, req *mcp.CallToolRequest, args *ReadAuthArgs) (*mcp.CallToolResult, any, error) {
	slog.Debug("IsReadAuthorized called", "args", args)
	result := &IsAuthorizedResult{}
	state, err := a.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	result.SenderAuth = state
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}}},
		nil, nil
}

// debug function to check authorization
func (a *AuthKeeper) IsWriteAuthorizedTool(ctx context.Context, req *mcp.CallToolRequest, args *ReadAuthArgs) (*mcp.CallToolResult, any, error) {
	slog.Debug("IsWriteAuthorized called", "args", args)
	result := &IsAuthorizedResult{}
	state, err := a.IsWriteAuthorized("")
	if err != nil {
		return nil, nil, err
	}
	result.SenderAuth = state
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}}},
		nil, nil
}
