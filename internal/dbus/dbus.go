package dbus

import (
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

type AuthKeeper struct {
	*dbus.Conn
	read  bool
	write bool
}

func checkAuth(conn *dbus.Conn, sender dbus.Sender, actionID string) *dbus.Error {
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
	/*
		flags := uint32(1) // AllowUserInteraction
		cancellationID := ""
	*/
	var result struct {
		IsAuthorized bool
		IsChallenge  bool
		Details      map[string]dbus.Variant
	}

	pkObj := conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
	err := pkObj.Call("org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
		subject, actionID, details, 1, "").Store(&result)

	if err != nil {
		slog.Error("error checking authorization", "error", err)
		return &dbus.Error{
			Name: "org.opensuse.systemdmcp.Error.AuthError",
			Body: []interface{}{"Error checking authorization: " + err.Error()},
		}
	}
	slog.Debug("authorization result", "result", result)
	if !result.IsAuthorized {
		return &dbus.Error{
			Name: "org.freedesktop.DBus.Error.AccessDenied",
			Body: []interface{}{"Authorization denied."},
		}
	}

	return nil
}

func (a *AuthKeeper) AuthRead(sender dbus.Sender) *dbus.Error {
	fmt.Println("read called")
	slog.Info("read called")
	err := checkAuth(a.Conn, sender, "org.opensuse.systemdmcp.AuthRead")
	if err == nil {
		a.read = true
	}
	return err
}

func (a *AuthKeeper) AuthWrite(sender dbus.Sender) *dbus.Error {
	fmt.Println("write called")
	slog.Info("write called")
	err := checkAuth(a.Conn, sender, "org.opensuse.systemdmcp.AuthWrite")
	if err == nil {
		a.write = true
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
