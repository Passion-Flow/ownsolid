// dameng-connector-check checks one explicitly configured native DM installation.
// It neither selects the platform database nor starts or installs a server.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

type result struct {
	FormatVersion int      `json:"format_version"`
	Success       bool     `json:"success"`
	Code          string   `json:"code,omitempty"`
	Observation   any      `json:"observation,omitempty"`
	Checks        []string `json:"checks"`
	CleanupState  string   `json:"cleanup_state"`
	RetainedTable string   `json:"retained_table,omitempty"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
func run(args []string, out io.Writer) int {
	f := flag.NewFlagSet("dameng-connector-check", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configuration := f.String("config", "", "protected connector JSON file")
	environment := f.String("env-file", "", "protected environment mapping file")
	transactions := f.Bool("transaction-probe", false, "create one disposable table in the configured schema and exercise transactions")
	launch := f.Bool("print-launch-environment", false, "print derived nonsecret native-loader references without connecting")
	launchSettings := f.Bool("print-launch-settings", false, "print typed nonsecret child environment and working directory without connecting")
	r := result{FormatVersion: 1, Checks: []string{}, CleanupState: "not_created"}
	if f.Parse(args) != nil || f.NArg() != 0 || *configuration == "" || (*launch || *launchSettings) && *transactions || *launch && *launchSettings {
		r.Code = "DAMENG_CHECK_ARGUMENTS_INVALID"
	} else {
		c, e := dameng.LoadConnectionConfiguration(*configuration, *environment)
		if e != nil {
			r.Code = dameng.FailureCode(e)
		} else if *launch || *launchSettings {
			var values any
			var e error
			if *launchSettings {
				values, e = dameng.LaunchSettings(c)
			} else {
				values, e = dameng.LaunchEnvironment(c)
			}
			if e != nil {
				r.Code = dameng.FailureCode(e)
			} else {
				if json.NewEncoder(out).Encode(values) != nil {
					return 1
				}
				return 0
			}
		} else {
			r = checkNative(c, *transactions)
		}
	}
	if json.NewEncoder(out).Encode(r) != nil {
		return 1
	}
	if !r.Success {
		return 1
	}
	return 0
}
