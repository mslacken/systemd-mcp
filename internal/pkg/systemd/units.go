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

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/internal/pkg/util"
)

func ValidStates() []string {
	return []string{"active", "dead", "inactive", "loaded", "mounted", "not-found", "plugged", "running", "all"}
}

func ValidUnitFileStates() []string {
	return []string{"enabled", "enabled-runtime", "linked", "linked-runtime", "masked", "masked-runtime", "static", "disabled", "invalid"}
}

type UnitProperties struct {
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
}

type ListUnitsParams struct {
	States     []string `json:"states,omitempty" jsonschema:"List units in these states. For loaded units (mode='loaded'), these are active/load states (e.g. 'running', 'failed'). For unit files (mode='files'), these are enablement states (e.g. 'enabled', 'disabled')."`
	Patterns   []string `json:"patterns,omitempty" jsonschema:"List units by their names or patterns (e.g. '*.service')."`
	Properties bool     `json:"properties,omitempty" jsonschema:"If true, return detailed properties for each unit. Only applies to mode='loaded'."`
	Verbose    bool     `json:"verbose,omitempty" jsonschema:"Return more details in the response."`
	Mode       string   `json:"mode,omitempty" jsonschema:"'loaded' (default) to list loaded units (active/inactive), 'files' to list all installed unit files."`
}

func CreateListUnitsSchema() *jsonschema.Schema {
	inputSchema, _ := jsonschema.For[ListUnitsParams](nil)
	var states []any
	for _, s := range ValidStates() {
		states = append(states, s)
	}

	inputSchema.Properties["mode"].Enum = []any{"loaded", "files"}
	inputSchema.Properties["mode"].Default = json.RawMessage(`"loaded"`)

	return inputSchema
}

func (conn *Connection) ListUnits(ctx context.Context, req *mcp.CallToolRequest, params *ListUnitsParams) (*mcp.CallToolResult, any, error) {
	slog.Debug("ListUnits called", "params", params)
	allowed, err := conn.auth.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}

	mode := params.Mode
	if mode == "" {
		mode = "loaded"
	}

	if mode == "files" {
		return conn.listUnitFilesInternal(ctx, params)
	}

	// Mode "loaded"
	reqStates := []string{}
	if len(params.States) > 0 {
		if slices.Contains(params.States, "all") {
			reqStates = []string{}
		} else {
			reqStates = params.States
			// Optional: Validate states for loaded mode
			valid := ValidStates()
			for _, s := range reqStates {
				if !slices.Contains(valid, s) {
					// We warn or error?
					// Let's error to be helpful, unless strict validation is off.
					// But user might mix up states if they don't know the mode.
					// Let's be lenient or just filter? The underlying dbus call filters.
					// If we pass invalid state to dbus, it might fail or return nothing.
					// Let's check validity.
					if !slices.Contains(valid, s) {
						return nil, nil, fmt.Errorf("requested state %s is not a valid state for mode 'loaded'", s)
					}
				}
			}
		}
	} else if len(params.Patterns) == 0 {
		reqStates = []string{"running"}
	}

	units, err := conn.dbus.ListUnitsByPatternsContext(ctx, reqStates, params.Patterns)
	if err != nil {
		return nil, nil, err
	}

	txtContentList := []mcp.Content{}

	if params.Properties {
		for _, u := range units {
			props, err := conn.dbus.GetAllPropertiesContext(ctx, u.Name)
			if err != nil {
				slog.Warn("failed to get properties for unit", "unit", u.Name, "error", err)
				continue
			}
			props = util.ClearMap(props)

			var jsonByte []byte
			if params.Verbose {
				jsonByte, err = json.Marshal(&props)
			} else {
				prop := UnitProperties{}
				tmp, _ := json.Marshal(props)
				if err := json.Unmarshal(tmp, &prop); err != nil {
					slog.Warn("failed to unmarshal properties", "unit", u.Name, "error", err)
					continue
				}
				jsonByte, err = json.Marshal(&prop)
			}
			if err != nil {
				return nil, nil, err
			}
			txtContentList = append(txtContentList, &mcp.TextContent{
				Text: string(jsonByte),
			})
		}
	} else {
		type LightUnit struct {
			Name        string `json:"name"`
			State       string `json:"state"`
			Description string `json:"description"`
		}

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
			txtContentList = append(txtContentList, &mcp.TextContent{
				Text: string(jsonByte),
			})
		}
	}

	if len(txtContentList) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "[]"}},
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: txtContentList,
	}, nil, nil
}

func (conn *Connection) listUnitFilesInternal(ctx context.Context, params *ListUnitsParams) (*mcp.CallToolResult, any, error) {
	unitList, err := conn.dbus.ListUnitFilesContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	txtContentList := []mcp.Content{}

	// Prepare filters
	filterStates := len(params.States) > 0 && !slices.Contains(params.States, "all")
	filterPatterns := len(params.Patterns) > 0

	for _, unit := range unitList {
		name := path.Base(unit.Path)
		state := unit.Type // In ListUnitFiles, Type corresponds to enablement state

		// Filter by state
		if filterStates {
			if !slices.Contains(params.States, state) {
				continue
			}
		}

		// Filter by pattern
		if filterPatterns {
			matched := false
			for _, pat := range params.Patterns {
				if match, _ := path.Match(pat, name); match {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		uInfo := struct {
			Name  string `json:"name"`
			State string `json:"state"` // changed from Type to State for consistency
		}{
			Name:  name,
			State: state,
		}
		jsonByte, err := json.Marshal(uInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("could not unmarshall result: %w", err)
		}
		txtContentList = append(txtContentList, &mcp.TextContent{
			Text: string(jsonByte),
		})
	}

	if len(txtContentList) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "[]"}},
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: txtContentList,
	}, nil, nil
}

// helper function to get the valid states
func (conn *Connection) ListStatesHandler(ctx context.Context) (lst []string, err error) {
	units, err := conn.dbus.ListUnitsByPatternsContext(ctx, []string{}, []string{})
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

	allowed, err := conn.auth.IsWriteAuthorized("")
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

type ChangeUnitStateParams struct {
	Name    string `json:"name" jsonschema:"Exact name of unit to change state"`
	Action  string `json:"action" jsonschema:"Action to perform."`
	Mode    string `json:"mode,omitempty" jsonschema:"Mode when restarting a unit. Defaults to 'replace'."`
	TimeOut uint   `json:"timeout,omitempty" jsonschema:"Time to wait for the operation to finish. Max 60s."`
	Runtime bool   `json:"runtime,omitempty" jsonschema:"Enable/Disable only temporarily (runtime)."`
}

func ValidChanges() []string {
	return []string{"restart", "restart_force", "start", "stop", "stop_kill", "reload", "enable", "enable_force", "disable"}
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
	if params.Action == "enable" || params.Action == "enable_force" || params.Action == "disable" {
		permission = "org.freedesktop.systemd1.manage-unit-files"
	} else {
		permission = "org.freedesktop.systemd1.manage-units"
	}

	allowed, err := conn.auth.IsWriteAuthorized(permission)
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
			slog.Error("error when enabling", "dbus.error", err)
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
