package systemd

import (
	"context"
	"fmt"
	"testing"

	"github.com/coreos/go-systemd/v22/dbus"
	dbus_pkg "github.com/openSUSE/systemd-mcp/dbus"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

type mockDbusConnection struct {
	DbusConnection
	listUnits           func() ([]dbus.UnitStatus, error)
	listUnitsFiltered   func(states []string) ([]dbus.UnitStatus, error)
	listUnitsByPatterns func(patterns []string, states []string) ([]dbus.UnitStatus, error)
	getAllProperties    func(unitName string) (map[string]interface{}, error)
	startUnit           func(name string, mode string) (int, error)
	stopUnit            func(name string, mode string) (int, error)
	restartUnit         func(name string, mode string) (int, error)
	reloadOrRestartUnit func(name string, mode string) (int, error)
	killUnit            func(name string, signal int32)
	enableUnitFiles     func(files []string, runtime bool, force bool) (bool, []dbus.EnableUnitFileChange, error)
	disableUnitFiles    func(files []string, runtime bool) ([]dbus.DisableUnitFileChange, error)
}

func (m *mockDbusConnection) ListUnitsContext(ctx context.Context) ([]dbus.UnitStatus, error) {
	return m.listUnits()
}

func (m *mockDbusConnection) ListUnitsFilteredContext(ctx context.Context, states []string) ([]dbus.UnitStatus, error) {
	return m.listUnitsFiltered(states)
}

func (m *mockDbusConnection) ListUnitsByPatternsContext(ctx context.Context, states []string, patterns []string) ([]dbus.UnitStatus, error) {
	return m.listUnitsByPatterns(patterns, states)
}

func (m *mockDbusConnection) GetAllPropertiesContext(ctx context.Context, unitName string) (map[string]interface{}, error) {
	return m.getAllProperties(unitName)
}

func (m *mockDbusConnection) StartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error) {
	if m.startUnit != nil {
		return m.startUnit(name, mode)
	}
	return 0, nil
}

func (m *mockDbusConnection) StopUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error) {
	if m.stopUnit != nil {
		return m.stopUnit(name, mode)
	}
	return 0, nil
}

func (m *mockDbusConnection) RestartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error) {
	if m.restartUnit != nil {
		return m.restartUnit(name, mode)
	}
	return 0, nil
}

func (m *mockDbusConnection) ReloadOrRestartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error) {
	if m.reloadOrRestartUnit != nil {
		return m.reloadOrRestartUnit(name, mode)
	}
	return 0, nil
}

func (m *mockDbusConnection) KillUnitContext(ctx context.Context, name string, signal int32) {
	if m.killUnit != nil {
		m.killUnit(name, signal)
	}
}

func (m *mockDbusConnection) EnableUnitFilesContext(ctx context.Context, files []string, runtime bool, force bool) (bool, []dbus.EnableUnitFileChange, error) {
	if m.enableUnitFiles != nil {
		return m.enableUnitFiles(files, runtime, force)
	}
	return false, nil, nil
}

func (m *mockDbusConnection) DisableUnitFilesContext(ctx context.Context, files []string, runtime bool) ([]dbus.DisableUnitFileChange, error) {
	if m.disableUnitFiles != nil {
		return m.disableUnitFiles(files, runtime)
	}
	return nil, nil
}

func TestListUnitHandlerNameState(t *testing.T) {
	tests := []struct {
		name          string
		params        *ListUnitNameParams
		mockListUnits func(patterns []string, states []string) ([]dbus.UnitStatus, error)
		mockGetProps  func(unitName string) (map[string]interface{}, error)
		want          []mcp.Content
		wantErr       bool
	}{
		{
			name: "success",
			params: &ListUnitNameParams{
				Names:   []string{"test.service"},
				Verbose: false,
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return []dbus.UnitStatus{{Name: "test.service"}}, nil
			},
			mockGetProps: func(unitName string) (map[string]interface{}, error) {
				return map[string]interface{}{"Id": unitName}, nil
			},
			want: []mcp.Content{
				&mcp.TextContent{
					Text: `{"Id":"test.service","Description":"","LoadState":"","FragmentPath":"","UnitFileState":"","UnitFilePreset":"","ActiveState":"","SubState":"","ActiveEnterTimestamp":0,"InvocationID":"","MainPID":0,"ExecMainPID":0,"ExecMainStatus":0,"TasksCurrent":0,"TasksMax":0,"CPUUsageNSec":0,"ControlGroup":"","ExecStartPre":null,"ExecStart":null,"Restart":"","MemoryCurrent":0}`,
				},
			},
			wantErr: false,
		},
		{
			name: "no units found",
			params: &ListUnitNameParams{
				Names: []string{"nonexistent.service"},
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return []dbus.UnitStatus{}, nil
			},
			wantErr: true,
		},
		{
			name: "dbus error on list units",
			params: &ListUnitNameParams{
				Names: []string{"test.service"},
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return nil, fmt.Errorf("dbus error")
			},
			wantErr: true,
		},
		{
			name: "dbus error on get properties",
			params: &ListUnitNameParams{
				Names: []string{"test.service"},
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return []dbus.UnitStatus{{Name: "test.service"}}, nil
			},
			mockGetProps: func(unitName string) (map[string]interface{}, error) {
				return nil, fmt.Errorf("dbus error")
			},
			wantErr: true,
		},
		{
			name: "success with multiple units",
			params: &ListUnitNameParams{
				Names: []string{"test1.service", "test2.service"},
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return []dbus.UnitStatus{{Name: "test1.service"}, {Name: "test2.service"}}, nil
			},
			mockGetProps: func(unitName string) (map[string]interface{}, error) {
				return map[string]interface{}{"Id": unitName}, nil
			},
			want: []mcp.Content{
				&mcp.TextContent{
					Text: `{"Id":"test1.service","Description":"","LoadState":"","FragmentPath":"","UnitFileState":"","UnitFilePreset":"","ActiveState":"","SubState":"","ActiveEnterTimestamp":0,"InvocationID":"","MainPID":0,"ExecMainPID":0,"ExecMainStatus":0,"TasksCurrent":0,"TasksMax":0,"CPUUsageNSec":0,"ControlGroup":"","ExecStartPre":null,"ExecStart":null,"Restart":"","MemoryCurrent":0}`,
				},
				&mcp.TextContent{
					Text: `{"Id":"test2.service","Description":"","LoadState":"","FragmentPath":"","UnitFileState":"","UnitFilePreset":"","ActiveState":"","SubState":"","ActiveEnterTimestamp":0,"InvocationID":"","MainPID":0,"ExecMainPID":0,"ExecMainStatus":0,"TasksCurrent":0,"TasksMax":0,"CPUUsageNSec":0,"ControlGroup":"","ExecStartPre":null,"ExecStart":null,"Restart":"","MemoryCurrent":0}`,
				},
			},
			wantErr: false,
		},
		{
			name: "success with additional properties",
			params: &ListUnitNameParams{
				Names:   []string{"test.service"},
				Verbose: false,
			},
			mockListUnits: func(patterns []string, states []string) ([]dbus.UnitStatus, error) {
				return []dbus.UnitStatus{{Name: "test.service"}}, nil
			},
			mockGetProps: func(unitName string) (map[string]interface{}, error) {
				return map[string]interface{}{"Id": unitName, "foo": "baar"}, nil
			},
			want: []mcp.Content{
				&mcp.TextContent{
					Text: `{"Id":"test.service","Description":"","LoadState":"","FragmentPath":"","UnitFileState":"","UnitFilePreset":"","ActiveState":"","SubState":"","ActiveEnterTimestamp":0,"InvocationID":"","MainPID":0,"ExecMainPID":0,"ExecMainStatus":0,"TasksCurrent":0,"TasksMax":0,"CPUUsageNSec":0,"ControlGroup":"","ExecStartPre":null,"ExecStart":null,"Restart":"","MemoryCurrent":0}`,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &Connection{
				dbus: &mockDbusConnection{
					listUnitsByPatterns: tt.mockListUnits,
					getAllProperties:    tt.mockGetProps,
				},
				auth: &dbus_pkg.AuthKeeper{
					ReadAllowed: true,
				},
			}

			got, nil, err := conn.ListUnitHandlerNameState(context.Background(), nil, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListUnitHandlerNameState() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got.Content) != len(tt.want) {
					t.Errorf("ListUnitHandlerNameState() got = %v, want %v", got.Content, tt.want)
					return
				}
				for i := range got.Content {
					gotText := got.Content[i].(*mcp.TextContent).Text
					wantText := tt.want[i].(*mcp.TextContent).Text

					assert.JSONEq(t, wantText, gotText)
				}
			}
		})
	}
}

func TestChangeUnitState(t *testing.T) {
	tests := []struct {
		name     string
		params   *ChangeUnitStateParams
		mockDbus *mockDbusConnection
		wantErr  bool
	}{
		{
			name: "start unit success",
			params: &ChangeUnitStateParams{
				Name:   "test.service",
				Action: "start",
			},
			mockDbus: &mockDbusConnection{
				startUnit: func(name string, mode string) (int, error) {
					if name != "test.service" {
						return 0, fmt.Errorf("wrong name")
					}
					if mode != "replace" {
						return 0, fmt.Errorf("wrong mode")
					}
					return 0, nil
				},
			},
			wantErr: false,
		},
		{
			name: "restart unit success",
			params: &ChangeUnitStateParams{
				Name:   "test.service",
				Action: "restart",
				Force:  true,
			},
			mockDbus: &mockDbusConnection{
				restartUnit: func(name string, mode string) (int, error) {
					if name != "test.service" {
						return 0, fmt.Errorf("wrong name")
					}
					return 0, nil
				},
			},
			wantErr: false,
		},
		{
			name: "reload success",
			params: &ChangeUnitStateParams{
				Name:   "test.service",
				Action: "reload",
			},
			mockDbus: &mockDbusConnection{
				reloadOrRestartUnit: func(name string, mode string) (int, error) {
					return 0, nil
				},
			},
			wantErr: false,
		},
		{
			name: "enable success",
			params: &ChangeUnitStateParams{
				Name:   "test.service",
				Action: "enable",
			},
			mockDbus: &mockDbusConnection{
				enableUnitFiles: func(files []string, runtime bool, force bool) (bool, []dbus.EnableUnitFileChange, error) {
					return true, []dbus.EnableUnitFileChange{{Type: "symlink", Filename: "foo", Destination: "bar"}}, nil
				},
			},
			wantErr: false,
		},
		{
			name: "invalid action",
			params: &ChangeUnitStateParams{
				Name:   "test.service",
				Action: "dance",
			},
			mockDbus: &mockDbusConnection{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &Connection{
				dbus: tt.mockDbus,
				auth: &dbus_pkg.AuthKeeper{
					WriteAllowed: true,
				},
				rchannel: make(chan string, 10),
			}

			_, _, err := conn.ChangeUnitState(context.Background(), nil, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ChangeUnitState() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}