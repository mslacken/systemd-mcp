package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/coreos/go-systemd/v22/sdjournal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	auth "github.com/openSUSE/systemd-mcp/dbus"
)

type HostLog struct {
	journal *sdjournal.Journal
	auth    *auth.AuthKeeper
}

// NewLog instance creates a new HostLog instance
func NewLog(auth *auth.AuthKeeper) (*HostLog, error) {
	j, err := sdjournal.NewJournal()
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}
	return &HostLog{journal: j, auth: auth}, nil
}

// Close the log and underlying journal
func (log *HostLog) Close() error {
	return log.journal.Close()
}

type ListLogParams struct {
	Count    int    `json:"count,omitempty" jsonschema:"Number of log lines to output"`
	Unit     string `json:"unit,omitempty" jsonschema:"Exact name of the service/unit from which to get the logs. Without an unit name the entries of all units are returned. This parameter is optional."`
	AllBoots bool   `json:"allboots,omitempty" jsonschema:"Get the log entries from all boots, not just the active one"`
}

type LogOutput struct {
	Time       time.Time `json:"time"`
	Identifier string    `json:"identifier"`
	UnitName   string    `json:"unit_name"`
	Host       string    `json:"host,omitempty"`
	Msg        string    `json:"message"`
	Boot       string    `json:"bootid,omitempty"`
}

type ListLogResult struct {
	Host          string      `json:"host"`
	NrMessages    int         `json:"nr_messages"`
	Hint          string      `json:"hint,omitempty"`
	Documentation string      `json:"documentation,omitempty"`
	Messages      []LogOutput `json:"messages"`
}

func CreateListLogsSchema() *jsonschema.Schema {
	inputSchema, _ := jsonschema.For[ListLogParams](nil)
	inputSchema.Properties["count"].Default = json.RawMessage(`100`)

	return inputSchema
}

func (sj *HostLog) seekAndSkip(count uint64) (uint64, error) {
	if err := sj.journal.SeekTail(); err != nil {
		return 0, fmt.Errorf("failed to seek to end: %w", err)
	}
	if skip, err := sj.journal.PreviousSkip(count); err != nil {
		return 0, fmt.Errorf("failed to move back entries: %w", err)
	} else {
		return skip, nil
	}
}

func (sj *HostLog) ListLogTimeout(ctx context.Context, req *mcp.CallToolRequest, params *ListLogParams) (*mcp.CallToolResult, any, error) {
	slog.Debug("ListLogTimeout called", "params", params)
	timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	resultChan := make(chan struct {
		res *mcp.CallToolResult
		out any
		err error
	}, 1)

	go func() {
		res, out, err := sj.ListLog(timeoutCtx, req, params)
		resultChan <- struct {
			res *mcp.CallToolResult
			out any
			err error
		}{res: res, out: out, err: err}
	}()

	select {
	case <-timeoutCtx.Done():
		// The timeout context was cancelled, meaning the timeout occurred.
		return nil, nil, fmt.Errorf("timed out: %w", timeoutCtx.Err())
	case result := <-resultChan:
		// ListLog completed within the timeout.
		return result.res, result.out, result.err
	}
}

// get the lat log entries for a given unit, else just the last messages
func (sj *HostLog) ListLog(ctx context.Context, req *mcp.CallToolRequest, params *ListLogParams) (*mcp.CallToolResult, any, error) {
	slog.Debug("ListLog called", "params", params)
	allowed, err := sj.auth.IsReadAuthorized()
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, fmt.Errorf("calling method was canceled by user")
	}
	sj.journal.FlushMatches()
	if params.Unit != "" {
		if err := sj.journal.AddMatch("SYSLOG_IDENTIFIER=" + params.Unit); err != nil {
			return nil, nil, fmt.Errorf("failed to add unit filter: %w", err)
		}
		if err := sj.journal.AddDisjunction(); err != nil {
			return nil, nil, err
		}
		if err := sj.journal.AddMatch("_SYSTEMD_USER_UNIT=" + params.Unit); err != nil {
			return nil, nil, fmt.Errorf("failed to add unit filter: %w", err)
		}
		if err := sj.journal.AddDisjunction(); err != nil {
			return nil, nil, err
		}
		if err := sj.journal.AddMatch("_SYSTEMD_UNIT=" + params.Unit); err != nil {
			return nil, nil, fmt.Errorf("failed to add unit filter: %w", err)
		}
		if err := sj.journal.AddConjunction(); err != nil {
			return nil, nil, err
		}
	}
	if !params.AllBoots {
		if bootId, err := sj.journal.GetBootID(); err != nil {
			return nil, nil, fmt.Errorf("failed to get boot id: %s", err)
		} else if err := sj.journal.AddMatch("_BOOT_ID=" + bootId); err != nil {
			return nil, nil, fmt.Errorf("failed to add boot filter: %w", err)
		}
	}
	_, err = sj.seekAndSkip(uint64(params.Count))
	if err != nil {
		return nil, nil, err
	}

	var messages []LogOutput
	host, _ := os.Hostname()

	for {
		entry, err := sj.journal.GetEntry()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get log entry for %s", params.Unit)
		}

		timestamp := time.Unix(0, int64(entry.RealtimeTimestamp)*int64(time.Microsecond))

		structEntr := LogOutput{
			Identifier: entry.Fields["SYSLOG_IDENTIFIER"],
			UnitName:   entry.Fields["_SYSTEMD_UNIT"],
			Time:       timestamp,
			Msg:        entry.Fields["MESSAGE"],
		}
		if params.AllBoots {
			structEntr.Boot = entry.Fields["_BOOT_ID"]
		}
		if host == entry.Fields["_HOSTNAME"] {
			host = entry.Fields["_HOSTNAME"]
		}
		if structEntr.Identifier == "" {
			structEntr.Identifier = fmt.Sprintf("%s:%s", entry.Fields["_SYSTEMD_UNIT"], entry.Fields["_SYSTEMD_USER_UNIT"])
		}
		messages = append(messages, structEntr)

		ret, err := sj.journal.Next()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read next entry: %w", err)
		}
		if ret == 0 {
			break
		}
	}

	res := ListLogResult{
		Host:       host,
		NrMessages: len(messages),
		Messages:   messages,
	}

	jsonBytes, err := json.Marshal(res)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: string(jsonBytes),
			},
		},
	}, nil, nil
}
