package systemd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/pkg/util"
)

func ValidStates() []string {
	return []string{"active", "dead", "inactive", "loaded", "mounted", "not-found", "plugged", "running", "all"}
}

type ListUnitParams struct {
	State   string `json:"state" jsonschema:"List units that are in this state. The keyword 'all' can be used to get all available units on the system."`
	Verbose bool   `json:"verbose,omitempty" jsonschema:"Set to true for more detail. Otherwise set to false."`
}

func CreateListInputSchema() *jsonschema.Schema {
	inputschmema, _ := jsonschema.For[ListUnitParams](nil)
	var states []any
	for _, s := range ValidStates() {
		states = append(states, s)
	}
	inputschmema.Properties["state"].Enum = states
	return inputschmema
}

func (conn *Connection) ListUnitState(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitParams) (*mcp.CallToolResult, any, error) {
	slog.Debug("ListUnitState called", "params", params)
	allowed, err := conn.auth.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}
	reqState := params.State
	if reqState == "" {
		reqState = "running"
	} else {
		if !slices.Contains(ValidStates(), reqState) {
			return nil, nil, fmt.Errorf("requested state %s is not a valid state", reqState)
		}
	}
	var units []dbus.UnitStatus
	// route can't be taken as it confuses small modells
	if reqState == "all" {
		units, err = conn.dbus.ListUnitsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
	} else {
		units, err = conn.dbus.ListUnitsFilteredContext(ctx, []string{reqState})
		if err != nil {
			return nil, nil, err
		}
	}
	type LightUnit struct {
		Name        string `json:"name"`
		State       string `json:"state"`
		Description string `json:"description"`
	}

	txtContenList := []mcp.Content{}
	for _, u := range units {
		var jsonByte []byte
		if params.Verbose {
			jsonByte, _ = json.Marshal(&u)
		} else {
			lightUnit := LightUnit{
				Name:        u.Name,
				State:       u.ActiveState,
				Description: u.Description,
			}
			jsonByte, _ = json.Marshal(&lightUnit)
		}
		txtContenList = append(txtContenList, &mcp.TextContent{
			Text: string(jsonByte),
		})

	}

	return &mcp.CallToolResult{
		Content: txtContenList,
	}, nil, nil
}

type ListUnitNameParams struct {
	Names   []string `json:"names" jsonschema:"List units with the given by their names. Regular expressions should be used. The request foo* expands to foo.service. Useful patterns are '*.timer' for all timers, '*.service' for all services, '*.mount for all mounts, '*.socket' for all sockets."`
	Verbose bool     `json:"verbose,omitempty" jsonschema:"Set to true for more detail. Otherwise set to false."`
}

/*
Handler to list the unit by name
*/
func (conn *Connection) ListUnitHandlerNameState(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitNameParams) (*mcp.CallToolResult, any, error) {
	slog.Debug("ListUnitHandlerNameState called", "params", params)
	allowed, err := conn.auth.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}
	reqNames := params.Names
	// reqStates := request.GetStringSlice("states", []string{""})
	var units []dbus.UnitStatus
	units, err = conn.dbus.ListUnitsByPatternsContext(ctx, []string{}, reqNames)
	if err != nil {
		return nil, nil, err
	}
	// unitProps := make([]map[string]interface{}, 1, 1)
	txtContentList := []mcp.Content{}
	for _, u := range units {
		props, err := conn.dbus.GetAllPropertiesContext(ctx, u.Name)
		if err != nil {
			return nil, nil, err
		}
		props = util.ClearMap(props)
		jsonByte, err := json.Marshal(&props)
		if err != nil {
			return nil, nil, err
		}
		if params.Verbose {
			txtContentList = append(txtContentList, &mcp.TextContent{
				Text: string(jsonByte),
			})
		} else {
			prop := struct {
				Id          string `json:"Id"`
				Description string `json:"Description"`

				// Load state info
				LoadState      string `json:"LoadState"`
				FragmentPath   string `json:"FragmentPath"`
				UnitFileState  string `json:"UnitFileState"`
				UnitFilePreset string `json:"UnitFilePreset"`

				// Active state info
				ActiveState          string `json:"ActiveState"`
				SubState             string `json:"SubState"`
				ActiveEnterTimestamp uint64 `json:"ActiveEnterTimestamp"`

				// Process info
				InvocationID   string `json:"InvocationID"`
				MainPID        int    `json:"MainPID"`
				ExecMainPID    int    `json:"ExecMainPID"`
				ExecMainStatus int    `json:"ExecMainStatus"`

				// Resource usage
				TasksCurrent uint64 `json:"TasksCurrent"`
				TasksMax     uint64 `json:"TasksMax"`
				CPUUsageNSec uint64 `json:"CPUUsageNSec"`

				// Control group
				ControlGroup string `json:"ControlGroup"`

				// Exec commands (simplified - would need additional processing)
				ExecStartPre [][]interface{} `json:"ExecStartPre"`
				ExecStart    [][]interface{} `json:"ExecStart"`

				// Additional fields that might be useful
				Restart       string `json:"Restart"`
				MemoryCurrent uint64 `json:"MemoryCurrent"`
			}{}
			err = json.Unmarshal(jsonByte, &prop)
			if err != nil {
				return nil, nil, err
			}
			jsonByte, err = json.Marshal(&prop)
			if err != nil {
				return nil, nil, err
			}
			txtContentList = append(txtContentList, &mcp.TextContent{
				Text: string(jsonByte),
			})
		}

	}
	if len(txtContentList) == 0 {
		return nil, nil, fmt.Errorf("found no units with name pattern: %v", reqNames)
	}
	return &mcp.CallToolResult{
		Content: txtContentList,
	}, nil, nil
}

// helper function to get the valid states
func (conn *Connection) ListStatesHandler(ctx context.Context) (lst []string, err error) {
	units, err := conn.dbus.ListUnitsContext(ctx)
	if err != nil {
		return
	}
	states := make(map[string]bool)
	for _, u := range units {
		if _, ok := states[u.ActiveState]; !ok {
			states[u.ActiveState] = true
		}
		if _, ok := states[u.LoadState]; !ok {
			states[u.LoadState] = true
		}
		if _, ok := states[u.SubState]; !ok {
			states[u.SubState] = true
		}
	}
	for key := range states {
		lst = append(lst, key)
	}
	return
}

type RestartReloadParams struct {
	Name         string `json:"name" jsonschema:"Exact name of unit to restart"`
	TimeOut      uint   `json:"timeout,omitempty" jsonschema:"Time to wait for the restart or reload to finish. After the timeout the function will return and restart and reload will run in the background and the result can be retreived with a separate function."`
	Mode         string `json:"mode,omitempty" jsonschema:"Mode used for the restart or reload. 'replace' should be used."`
	Forcerestart bool   `json:"forcerestart,omitempty" jsonschema:"mode of the operation. 'replace' should be used per default and replace allready queued jobs. With 'fail' the operation will fail if other operations are in progress."`
}

// return which are define in the upstream documentation as:
// The mode needs to be one of
// replace, fail, isolate, ignore-dependencies, ignore-requirements. If
// "replace" the call will start the unit and its dependencies, possibly
// replacing already queued jobs that conflict with this. If "fail" the call
// will start the unit and its dependencies, but will fail if this would change
// an already queued job. If "isolate" the call will start the unit in question
// and terminate all units that aren't dependencies of it. If
// "ignore-dependencies" it will start a unit but ignore all its dependencies.
// If "ignore-requirements" it will start a unit but only ignore the
// requirement dependencies. It is not recommended to make use of the latter
// two options.
func ValidRestartModes() []string {
	return []string{"replace", "fail", "isolate", "ignore-dependencies", "ignore-requirements"}
}

const MaxTimeOut uint = 60

func GetRestsartReloadParamsSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[RestartReloadParams](nil)
	if err != nil {
		return nil, err
	}
	validList := []any{}
	for _, s := range ValidRestartModes() {
		validList = append(validList, any(s))
	}
	schema.Properties["mode"].Enum = validList
	return schema, nil
}

type CheckReloadRestartParams struct {
	TimeOut uint `json:"timeout,omitempty" jsonschema:"Time to wait for the restart or reload to finish. After the timeout the function will return and restart and reload will run in the background and the result can be retreived with a separate function."`
}

// check status of reload or restart
func (conn *Connection) CheckForRestartReloadRunning(ctx context.Context, req *mcp.CallToolRequest, params *RestartReloadParams) (res *mcp.CallToolResult, _ any, err error) {
	slog.Debug("CheckForRestartReloadRunning called", "params", params)
	allowed, err := conn.auth.IsAuthorizedSelf("org.freedesktop.systemd1.manage-units")
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}
	select {
	case result := <-conn.rchannel:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: result,
				},
			},
		}, nil, nil
	case <-time.After(3 * time.Second):
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: "Reload or restart still in progress.",
				},
			},
		}, nil, nil
	default:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: "Finished",
				},
			},
		}, nil, nil
	}
}

type ListUnitFilesParams struct {
	Type []string `json:"types,omitempty" jsonschema:"List of the type which should be returned."`
}

// returns the unit files known to systemd
func (conn *Connection) ListUnitFiles(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitFilesParams) (res *mcp.CallToolResult, _ any, err error) {
	slog.Debug("ListUnitFiles called", "params", params)
	allowed, err := conn.auth.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}
	unitList, err := conn.dbus.ListUnitFilesContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	txtContentList := []mcp.Content{}
	for _, unit := range unitList {
		uInfo := struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}{
			Name: path.Base(unit.Path),
			Type: unit.Type,
		}
		jsonByte, err := json.Marshal(uInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("could not unmarshall result: %w", err)
		}
		txtContentList = append(txtContentList, &mcp.TextContent{
			Text: string(jsonByte),
		})

	}
	return &mcp.CallToolResult{
		Content: txtContentList,
	}, nil, nil
}

type ChangeUnitStateParams struct {
	Name    string `json:"name" jsonschema:"Exact name of unit to change state"`
	Action  string `json:"action" jsonschema:"Action to perform."`
	Mode    string `json:"mode,omitempty" jsonschema:"Mode when restarting a unit. Defaults to 'replace'."`
	TimeOut uint   `json:"timeout,omitempty" jsonschema:"Time to wait for the operation to finish. Max 60s."`
	Runtime bool   `json:"runtime,omitempty" jsonschema:"Enable/Disable only temporarily (runtime)."`
}

func ValidChanges() []string {
	return []string{"restart", "restart_force", "stop", "stop_kill", "reload", "enable", "enable_force", "disable"}
}
func ValidModes() []string {
	return []string{"replace", "fail", "isolate", "ignore-dependencies", "ignore-requirements"}
}

func CreateChangeInputSchema() *jsonschema.Schema {
	inputSchmema, _ := jsonschema.For[ChangeUnitStateParams](nil)
	var states []any
	var modes []any
	for _, s := range ValidChanges() {
		states = append(states, s)
	}
	for _, m := range ValidModes() {
		modes = append(modes, m)
	}
	inputSchmema.Properties["action"].Enum = states
	inputSchmema.Properties["action"].Default = json.RawMessage("\"enable\"")
	inputSchmema.Properties["mode"].Enum = modes
	inputSchmema.Properties["mode"].Default = json.RawMessage("\"replace\"")
	inputSchmema.Properties["timeout"].Default = json.RawMessage("30")

	return inputSchmema
}

func (conn *Connection) ChangeUnitState(ctx context.Context, req *mcp.CallToolRequest, params *ChangeUnitStateParams) (res *mcp.CallToolResult, _ any, err error) {
	slog.Debug("ChangeUnitState called", "params", params)

	var permission string
	if params.Action == "enable" || params.Action == "disable" {
		permission = "org.freedesktop.systemd1.manage-unit-files"
	} else {
		permission = "org.freedesktop.systemd1.manage-units"
	}
	allowed, err := conn.auth.IsAuthorizedSelf(permission)
	defer conn.auth.Deauthorize()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}

	if params.TimeOut > MaxTimeOut {
		return nil, nil, fmt.Errorf("not waiting longer than MaxTimeOut(%d), longer operation will run in the background and result can be gathered with separate function.", MaxTimeOut)
	}

	switch params.Action {
	case "start":
		if params.Mode == "" {
			params.Mode = "replace"
		}
		if !slices.Contains(ValidRestartModes(), params.Mode) {
			return nil, nil, fmt.Errorf("invalid mode for start: %s", params.Mode)
		}
		_, err = conn.dbus.StartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	case "stop":
		_, err = conn.dbus.StopUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	case "stop_kill":
		conn.dbus.KillUnitContext(ctx, params.Name, int32(9))
	case "restart_force":
		_, err = conn.dbus.RestartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	case "restart":
		_, err = conn.dbus.ReloadOrRestartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	case "reload":
		_, err = conn.dbus.ReloadOrRestartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	case "enable", "enable_force":
		_, enabledRes, err := conn.dbus.EnableUnitFilesContext(ctx, []string{params.Name}, params.Runtime, strings.HasSuffix(params.Action, "_force"))
		if err != nil {
			return nil, nil, fmt.Errorf("error when enabling: %w", err)
		}
		if len(enabledRes) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("nothing changed for %s", params.Name)},
				},
			}, nil, nil
		}
		txtContentList := []mcp.Content{}
		for _, res := range enabledRes {
			resJson := struct {
				Type        string `json:"type"`
				Filename    string `json:"filename"`
				Destination string `json:"destination"`
			}{Type: res.Type, Filename: res.Filename, Destination: res.Destination}
			jsonByte, _ := json.Marshal(resJson)
			txtContentList = append(txtContentList, &mcp.TextContent{Text: string(jsonByte)})
		}
		return &mcp.CallToolResult{Content: txtContentList}, nil, nil
	case "disable":
		disabledRes, err := conn.dbus.DisableUnitFilesContext(ctx, []string{params.Name}, params.Runtime)
		if err != nil {
			return nil, nil, fmt.Errorf("error when disabling: %w", err)
		}
		if len(disabledRes) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("nothing changed for %s", params.Name)},
				},
			}, nil, nil
		}
		txtContentList := []mcp.Content{}
		for _, res := range disabledRes {
			resJson := struct {
				Type        string `json:"type"`
				Filename    string `json:"filename"`
				Destination string `json:"destination"`
			}{Type: res.Type, Filename: res.Filename, Destination: res.Destination}
			jsonByte, _ := json.Marshal(resJson)
			txtContentList = append(txtContentList, &mcp.TextContent{Text: string(jsonByte)})
		}
		return &mcp.CallToolResult{Content: txtContentList}, nil, nil
	default:
		return nil, nil, fmt.Errorf("invalid action: %s", params.Action)
	}

	if err != nil {
		return nil, nil, err
	}

	return conn.CheckForRestartReloadRunning(ctx, req, &RestartReloadParams{
		TimeOut: params.TimeOut,
	})
}
