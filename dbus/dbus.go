package dbus

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

type DbusAuth struct {
	*dbus.Conn
	sender   dbus.Sender // store the sender which authorized the last call
	Timeout  uint32
	DbusName string
	DbusPath string
}

// Just register the sender for further call backs
func (a *DbusAuth) AuthRegister(sender dbus.Sender) *dbus.Error {
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

// Deauthorize revokes the authorization
func (a *DbusAuth) Deauthorize() *dbus.Error {
	slog.Debug("Deauthorize called")
	return nil
}

// Check if read was authorized. Triggers also a call back via
// dbus if read was authorized at another time
func (a *DbusAuth) IsReadAuthorized(ctx context.Context) (bool, error) {
	slog.Debug("checking read auth", "address", a.sender)

	readPermission, _ := ctx.Value(PermissionKey).(string)
	if readPermission == "" {
		readPermission = "com.suse.gatekeeper.readlog"
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()

	var state bool
	var err error

	if a.sender == "" {
		if os.Geteuid() == 0 {
			state = true
		} else {
			state, err = CheckPolkitByPID(int32(os.Getpid()), readPermission)
		}
	}
	if err != nil {
		return false, err
	}

	select {
	case <-ctx.Done():
		return false, fmt.Errorf("read authorization timed out: %w", ctx.Err())
	default:
		return state, nil
	}
}

// Check if write was authorized. Triggers also a call back via
// dbus if write was authorized at another time
type contextKey string

const PermissionKey contextKey = "systemdPermission"

func (a *DbusAuth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	slog.Debug("checking write auth", "sender", a.sender)

	systemdPermission, _ := ctx.Value(PermissionKey).(string)
	if systemdPermission == "" {
		systemdPermission = "org.freedesktop.systemd1.manage-units"
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()

	var state bool
	var err error

	if a.sender == "" {
		if os.Geteuid() == 0 {
			state = true
		} else {
			state, err = CheckPolkitByPID(int32(os.Getpid()), systemdPermission)
		}
	}
	if err != nil {
		return false, err
	}

	select {
	case <-ctx.Done():
		return false, fmt.Errorf("write authorization timed out: %w", ctx.Err())
	default:
		return state, nil
	}
}

// getProcessStartTime returns the start time of a process in clock ticks since system boot.
func getProcessStartTime(pid int32) (uint64, int32, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, -1, fmt.Errorf("failed to read %s: %w", statPath, err)
	}

	// The filename (second field) can contain spaces and parentheses,
	// so we find the last closing parenthesis and start from there.
	lastParen := bytes.LastIndexByte(data, ')')
	if lastParen == -1 || lastParen+2 >= len(data) {
		return 0, -1, fmt.Errorf("failed to parse %s", statPath)
	}

	fields := strings.Fields(string(data[lastParen+2:]))
	// field 22 in /proc/[pid]/stat is starttime (index 19 in fields since fields starts from index 0 which is field 3)
	if len(fields) < 20 {
		return 0, -1, fmt.Errorf("not enough fields in %s", statPath)
	}

	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, -1, fmt.Errorf("failed to parse starttime in %s: %w", statPath, err)
	}

	// Get UID
	var stat syscall.Stat_t
	if err := syscall.Stat(fmt.Sprintf("/proc/%d", pid), &stat); err != nil {
		return 0, -1, fmt.Errorf("failed to stat /proc/%d: %w", pid, err)
	}

	return startTime, int32(stat.Uid), nil
}

// CheckPolkitByPID checks if the given PID is authorized for the given actionID.
func CheckPolkitByPID(pid int32, actionID string) (bool, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return false, fmt.Errorf("could not connect to system dbus: %w", err)
	}
	defer conn.Close()

	startTime, uid, err := getProcessStartTime(pid)
	if err != nil {
		return false, fmt.Errorf("failed to get process info for PID %d: %w", pid, err)
	}

	subject := struct {
		A string
		B map[string]dbus.Variant
	}{
		"unix-process",
		map[string]dbus.Variant{
			"pid":        dbus.MakeVariant(uint32(pid)),
			"start-time": dbus.MakeVariant(uint64(startTime)),
			"uid":        dbus.MakeVariant(int32(uid)),
		},
	}

	details := make(map[string]string)
	flags := uint32(1) // AllowUserInteraction
	cancellationID := ""
	var result struct {
		IsAuthorized bool
		IsChallenge  bool
		Details      map[string]dbus.Variant
	}

	pkObj := conn.Object("org.freedesktop.PolicyKit1", "/org/freedesktop/PolicyKit1/Authority")
	err = pkObj.Call("org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
		subject, actionID, details, flags, cancellationID).Store(&result)

	if err != nil {
		return false, fmt.Errorf("error checking authorization: %w", err)
	}

	return result.IsAuthorized, nil
}
