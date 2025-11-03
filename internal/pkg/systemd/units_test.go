package systemd

import (
	"context"
	"fmt"
	"testing"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

type mockDbusConnection struct {
	DbusConnection
	listUnits           func() ([]dbus.UnitStatus, error)
	listUnitsFiltered   func(states []string) ([]dbus.UnitStatus, error)
	listUnitsByPatterns func(patterns []string, states []string) ([]dbus.UnitStatus, error)
	getAllProperties    func(unitName string) (map[string]interface{}, error)
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
