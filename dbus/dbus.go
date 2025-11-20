package dbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	sender       dbus.Sender // store the sender which authorized the last call
	Timeout      uint32
	ReadAllowed  bool // allow read without auth
	WriteAllowed bool // allow write without auth
	DbusName     string
	DbusPath     string
}

func checkAuth(conn *dbus.Conn, sender dbus.Sender, actionID string) (bool, *dbus.Error) {
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
	_, err := checkAuth(a.Conn, sender, a.DbusName+".AuthRead")
	if err == nil {
		a.sender = sender
	}
	return err
}

// write authorization method exposed to dbus
func (a *AuthKeeper) AuthWrite(sender dbus.Sender) *dbus.Error {
	_, err := checkAuth(a.Conn, sender, a.DbusName+".AuthWrite")
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

// Deauthorize revokes the authorization
func (a *AuthKeeper) Deauthorize() *dbus.Error {
	slog.Debug("Deauthorize called")
	a.WriteAllowed = false
	if a.sender != "" {
		subject := struct {
			A string
			B map[string]dbus.Variant
		}{
			"system-bus-name",
			map[string]dbus.Variant{
				"name": dbus.MakeVariant(string(a.sender)),
			},
		}
		slog.Debug("revoking auth", "subject", subject)

		pkObj := a.Conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
		err := pkObj.Call("org.freedesktop.PolicyKit1.Authority.RevokeTemporaryAuthorizations", 0,
			subject).Store()

		if err != nil {
			slog.Error("error revoking authorization", "error", err)
			return &dbus.Error{
				Name: "org.opensuse.systemdmcp.Error.AuthError",
				Body: []interface{}{"Error revoking authorization: " + err.Error()},
			}
		}
		a.sender = ""
	}
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
		Timeout:  5,
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
	slog.Info("Listening on dbus", "name", dbusName)
	return keeper, nil
}

// Check if read was authorized. Triggers also a call back via
// dbus if read was authorized at another time
func (a *AuthKeeper) IsReadAuthorized() (bool, error) {
	if a.ReadAllowed {
		return true, nil
	}
	slog.Debug("checking read auth")
	if a.sender != "" {
		slog.Debug("dbus sender", "address", a.sender)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.Timeout)*time.Second)
		defer cancel()

		state, dbuserr := checkAuth(a.Conn, a.sender, a.DbusName+".AuthRead")
		if dbuserr != nil {
			return false, fmt.Errorf("authorization error: %s", dbuserr)
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("read authorization timed out: %w", ctx.Err())
		default:
			return state, nil
		}
	}
	return false, fmt.Errorf("authorize reading by calling: %s --allow-read", getExecutableName())
}

// Check if write was authorized. Triggers also a call back via
// dbus if write was authorized at another time
func (a *AuthKeeper) IsWriteAuthorized() (bool, error) {
	if a.WriteAllowed {
		return true, nil
	}
	slog.Debug("checking write auth")
	if a.sender == "" {
		var uniqueName string
		err := a.Conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, a.DbusName).Store(&uniqueName)
		if err != nil {
			return false, fmt.Errorf("could not get unique name for self: %w", err)

		}
		slog.Debug("geeting send name", "uniqname", uniqueName)
		a.sender = dbus.Sender(uniqueName)
	}
	slog.Debug("dbus sender", "address", a.sender)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.Timeout)*time.Second)
	defer cancel()

	state, dbuserr := checkAuth(a.Conn, a.sender, a.DbusName+".AuthWrite")
	if dbuserr != nil {
		return false, fmt.Errorf("authorization error: %s", dbuserr)
	}
	/*
		state, dbuserr = checkAuth(a.Conn, a.sender, "org.freedesktop.systemd1.manage-units")
		if dbuserr != nil {
			return false, fmt.Errorf("authorization error: %s", dbuserr)
		}
	*/
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("write authorization timed out: %w", ctx.Err())
	default:
		return state, nil
	}
	// return false, fmt.Errorf("authorize writing by calling: %s --allow-write", getExecutableName())
}

// Check if write was authorized for the process itself.
func (a *AuthKeeper) IsAuthorizedSelf(actionID string) (bool, error) {
	if os.Getuid() == 0 {
		return true, nil
	}
	if a.WriteAllowed {
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

	state, dbuserr := checkAuth(a.Conn, dbus.Sender(uniqueName), targetAction)
	if dbuserr != nil {
		return false, fmt.Errorf("authorization error: %s", dbuserr)
	}
	return state, nil
}

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
	state, err := a.IsWriteAuthorized()
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
