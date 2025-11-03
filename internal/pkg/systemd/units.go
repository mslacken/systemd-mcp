package systemd

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/pkg/util"
)

// create a resource desription for getting the systemd states
/*
func UnitRessource(state string) mcp.Resource {
	return mcp.NewResource(fmt.Sprintf("systemd://units/state/%s", state),
		fmt.Sprintf("systemd units and services on the host with the state %s", state),
		mcp.WithMIMEType("application/json"),
	)
}

// create a handler for to get the given state, some extra handing for
// the 'all' state, which is not implemted by the API
func (conn *Connection) CreateResHandler(state string) func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return func(ctx context.Context,
		request mcp.ReadResourceRequest,
	) (resources []mcp.ResourceContents, err error) {
		var units []dbus.UnitStatus
		if strings.EqualFold(state, "all") {
			units, err = conn.dbus.ListUnitsContext(ctx)
			if err != nil {
				return resources, err
			}
		} else {
			units, err = conn.dbus.ListUnitsFilteredContext(ctx, []string{state})
			if err != nil {
				return nil, err
			}
		}
		jsonByte, err := json.Marshal(&units)
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     string(jsonByte),
			},
		}, nil
	}
}
*/
func ValidStates() []string {
	return []string{"active", "dead", "inactive", "loaded", "mounted", "not-found", "plugged", "running", "all"}
}

type ListUnitParams struct {
	State   string `json:"state" jsonschema:"List units that are in this state. The keyword 'all' can be used to get all available units on the system."`
	Verbose bool   `json:"verbose" jsonschema:"Set to true for more detail. Otherwise set to false."`
}

func (conn *Connection) ListUnitState(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitParams) (*mcp.CallToolResult, any, error) {
	var err error
	reqState := params.State
	if reqState == "" {
		reqState = "running"
	} else {
		if !slices.Contains(ValidStates(), reqState) {
			return nil, nil, fmt.Errorf("requsted state %s is not a valid state", reqState)
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
	Verbose bool     `json:"verbose" jsonschema:"Set to true for more detail. Otherwise set to false."`
}

/*
Handler to list the unit by name
*/
func (conn *Connection) ListUnitHandlerNameState(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitNameParams) (*mcp.CallToolResult, any, error) {
	var err error
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
	TimeOut      uint   `json:"timeout" jsonschema:"Time to wait for the restart or reload to finish. After the timeout the function will return and restart and reload will run in the background and the result can be retreived with a separate function."`
	Mode         string `json:"mode" jsonschema:"Mode used for the restart or reload. 'replace' should be used."`
	Forcerestart bool   `json:"forcerestart" jsonschema:"mode of the operation. 'replace' should be used per default and replace allready queued jobs. With 'fail' the operation will fail if other operations are in progress."`
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

// restart or reload a service
func (conn *Connection) RestartReloadUnit(ctx context.Context, req *mcp.CallToolRequest, params *RestartReloadParams) (res *mcp.CallToolResult, _ any, err error) {
	if params.Mode == "" {
		params.Mode = "replace"
	}
	if !slices.Contains(ValidRestartModes(), params.Mode) {
		return nil, nil, fmt.Errorf("invalid mode for restart or reload: %s", params.Mode)
	}
	if params.TimeOut > MaxTimeOut {
		return nil, nil, fmt.Errorf("not waiting longer than MaxTimeOut(%d), longer operation will run in the background and result can be gathered with separate function.", MaxTimeOut)
	}
	if params.Forcerestart {
		_, err = conn.dbus.RestartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	} else {
		_, err = conn.dbus.ReloadOrRestartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	}
	if err != nil {
		return nil, nil, err
	}
	return conn.CheckForRestartReloadRunning(ctx, req, &RestartReloadParams{
		TimeOut: params.TimeOut,
	})
}

func (conn *Connection) StartUnit(ctx context.Context, req *mcp.CallToolRequest, params *RestartReloadParams) (res *mcp.CallToolResult, _ any, err error) {
	if params.Mode == "" {
		params.Mode = "replace"
	}
	if !slices.Contains(ValidRestartModes(), params.Mode) {
		return nil, nil, fmt.Errorf("invalid mode for restart or reload: %s", params.Mode)
	}
	if params.TimeOut > MaxTimeOut {
		return nil, nil, fmt.Errorf("not waiting longer than MaxTimeOut(%d), longer operation will run in the background and result can be gathered with separate function.", MaxTimeOut)
	}
	_, err = conn.dbus.StartUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	if err != nil {
		return nil, nil, err
	}
	return conn.CheckForRestartReloadRunning(ctx, req, &RestartReloadParams{
		TimeOut: params.TimeOut,
	})
}

type CheckReloadRestartParams struct {
	TimeOut uint `json:"timeout" jsonschema:"Time to wait for the restart or reload to finish. After the timeout the function will return and restart and reload will run in the background and the result can be retreived with a separate function."`
}

// check status of reload or restart
func (conn *Connection) CheckForRestartReloadRunning(ctx context.Context, req *mcp.CallToolRequest, params *RestartReloadParams) (res *mcp.CallToolResult, _ any, err error) {
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

type StopParams struct {
	Name    string `json:"name" jsonschema:"Exact name of unit to stop"`
	TimeOut uint   `json:"timeout" jsonschema:"Time to wait for the stop to finish. After the timeout the function will return and stop run in the background and the result can be retreived with a separate function."`
	Mode    string `json:"mode" jsonschema:"mode of the operation. 'replace' should be used per default and replace allready queued jobs. With 'fail' the operation will fail if other operations are in progress."`
	Kill    bool   `json:"kill" jsonschema:"Kill the unit instead of shutting down cleanly. Use this option only if the unit doesn't shut down, even after waiting."`
}

// Stop or kill the given unit
func (conn *Connection) StopUnit(ctx context.Context, req *mcp.CallToolRequest, params *StopParams) (res *mcp.CallToolResult, _ any, err error) {
	if params.Mode == "" {
		params.Mode = "replace"
	}
	if !slices.Contains(ValidRestartModes(), params.Mode) {
		return nil, nil, fmt.Errorf("invalid mode for restart or reload: %s", params.Mode)
	}
	if params.TimeOut > MaxTimeOut {
		return nil, nil, fmt.Errorf("not waiting longer than MaxTimeOut(%d), longer operation will run in the background and result can be gathered with separate function.", MaxTimeOut)
	}
	if params.Kill {
		conn.dbus.KillUnitContext(ctx, params.Name, int32(9))
	} else {
		_, err = conn.dbus.StopUnitContext(ctx, params.Name, params.Mode, conn.rchannel)
	}
	if err != nil {
		return nil, nil, err
	}
	return conn.CheckForRestartReloadRunning(ctx, req, &RestartReloadParams{
		TimeOut: params.TimeOut,
	})
}

type EnableParams struct {
	File    string `json:"file" jsonschema:"Name of the service or unit if the unit is in the standard location. Takes the absolute path if the unit or service is not placed under '/etc/' or '/usr/lib/systemd'. Does not take wildcards. For the service foo, this would be 'foo.service' if foo is installed by a package."`
	Disable bool   `json"disable" jsonschema:"Set to true to disable the unit instead of enable."`
}

func (conn *Connection) EnableDisableUnit(ctx context.Context, req *mcp.CallToolRequest, params *EnableParams) (res *mcp.CallToolResult, _ any, err error) {
	if params.Disable {
		return conn.DisableUnit(ctx, req, params)
	} else {
		return conn.EnableUnit(ctx, req, params)
	}
}

func (conn *Connection) EnableUnit(ctx context.Context, req *mcp.CallToolRequest, params *EnableParams) (res *mcp.CallToolResult, _ any, err error) {
	_, enabledRes, err := conn.dbus.EnableUnitFilesContext(ctx, []string{params.File}, false, true)
	if err != nil {
		return nil, nil, fmt.Errorf("error when enabling: %w", err)
	}
	if len(enabledRes) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("nothing changed for %s", params.File),
				},
			},
		}, nil, nil
	} else {
		txtContentList := []mcp.Content{}
		for _, res := range enabledRes {
			resJson := struct {
				Type        string `json:"type"`
				Filename    string `json:"filename"`
				Destination string `json:"destination"`
			}{
				Type:        res.Type,
				Filename:    res.Filename,
				Destination: res.Destination,
			}
			jsonByte, err := json.Marshal(resJson)
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
}

func (conn *Connection) DisableUnit(ctx context.Context, req *mcp.CallToolRequest, params *EnableParams) (res *mcp.CallToolResult, _ any, err error) {
	disabledRes, err := conn.dbus.DisableUnitFilesContext(ctx, []string{params.File}, false)
	if err != nil {
		return nil, nil, fmt.Errorf("error when disabling: %w", err)
	}
	if len(disabledRes) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("nothing changed for %s", params.File),
				},
			},
		}, nil, nil
	} else {
		txtContentList := []mcp.Content{}
		for _, res := range disabledRes {
			resJson := struct {
				Type        string `json:"type"`
				Filename    string `json:"filename"`
				Destination string `json:"destination"`
			}{
				Type:        res.Type,
				Filename:    res.Filename,
				Destination: res.Destination,
			}
			jsonByte, err := json.Marshal(resJson)
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
}

type ListUnitFilesParams struct {
	Type []string `json:"types" jsonschema:"List of the type which should be returned."`
}

// returns the unit files known to systemd
func (conn *Connection) ListUnitFiles(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitFilesParams) (res *mcp.CallToolResult, _ any, err error) {
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
