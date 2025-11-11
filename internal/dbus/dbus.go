package dbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AuthKeeper struct {
	*dbus.Conn
	sender dbus.Sender
	read   bool
	write  bool
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

func (a *AuthKeeper) AuthRead(sender dbus.Sender) *dbus.Error {
	state, err := checkAuth(a.Conn, sender, "org.opensuse.systemdmcp.AuthRead")
	if err == nil {
		a.read = state
		a.sender = sender
	}
	return err
}

func (a *AuthKeeper) AuthWrite(sender dbus.Sender) *dbus.Error {
	state, err := checkAuth(a.Conn, sender, "org.opensuse.systemdmcp.AuthWrite")
	if err == nil {
		a.write = state
		a.sender = sender
	}
	return err
}

func SetupDBus() (*AuthKeeper, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Warn("could not connect to system dbus", "error", err)
		return nil, err
	}

	keeper := &AuthKeeper{Conn: conn}

	const intro = `
<node>
	<interface name="org.opensuse.systemdmcp">
		<method name="AuthRead">
		</method>
		<method name="AuthWrite">
		</method>
</interface>` + introspect.IntrospectDataString + `</node> `

	conn.Export(keeper, "/org/opensuse/systemdmcp", "org.opensuse.systemdmcp")
	conn.Export(introspect.Introspectable(intro), "/org/opensuse/systemdmcp", "org.freedesktop.DBus.Introspectable")

	reply, err := conn.RequestName("org.opensuse.systemdmcp", dbus.NameFlagDoNotQueue)
	if err != nil {
		slog.Warn("could not request dbus name", "error", err)
		conn.Close()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		slog.Warn("dbus name already taken", "name", "org.opensuse.systemdmcp")
		conn.Close()
		return nil, fmt.Errorf("dbus name already taken")
	}
	slog.Info("Listening on dbus", "name", "org.opensuse.systemdmcp")
	return keeper, nil
}

func (a *AuthKeeper) Read() bool {
	return a.read
}

func (a *AuthKeeper) Write() bool {
	return a.write
}

type ReadAuthArgs struct{}

type IsAuthorizedResult struct {
	Authorized bool `json:"authorized"`
	SenderAuth bool `json:"sender_auth"`
}

func (a *AuthKeeper) IsReadAuthorized(ctx context.Context, req *mcp.CallToolRequest, args *ReadAuthArgs) (*mcp.CallToolResult, any, error) {
	slog.Debug("IsReadAuthorized called", "args", args)
	result := &IsAuthorizedResult{
		Authorized: a.read,
	}
	if a.sender != "" {
		slog.Debug("dbus sender", "address", a.sender)
		state, dbuserr := checkAuth(a.Conn, a.sender, "org.opensuse.systemdmcp.AuthRead")
		if dbuserr != nil {
			return nil, nil, dbuserr
		}
		result.SenderAuth = state
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}, nil, nil
}

func (a *AuthKeeper) IsWriteAuthorized(ctx context.Timenil, req *mcp.CallToolRequest, args *ReadAuthArgs) (*mcp.CallToolResult, any, error) {
	slog.Debug("IsWriteAuthorized called", "args", args)
	result := &IsAuthorizedResult{
		Authorized: a.write,
	}
	if a.sender != "" {
		slog.Debug("dbus sender", "address", a.sender)
		state, dbuserr := checkAuth(a.Conn, a.sender, "org.opensuse.systemdmcp.AuthWrite")
		if dbuserr != nil {
			return nil, nil, dbuserr
		}
		result.SenderAuth = state
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}, nil, nil
}
