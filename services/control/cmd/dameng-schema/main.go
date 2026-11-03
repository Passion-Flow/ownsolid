// dameng-schema owns explicit native foundation operations. It never selects a
// serving database, converts foreign SQL, creates users or starts a server.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
)

type nativeOperatorOptions struct{ Policy, Environment, Input, Output string }

func operatorMode(mode string) bool {
	switch mode {
	case "bootstrap-admin-file", "reconcile-bootstrap-file", "resume-bootstrap-file", "check-license":
		return true
	}
	return false
}
func operatorArguments(mode string, o nativeOperatorOptions) bool {
	if !operatorMode(mode) {
		return o == (nativeOperatorOptions{})
	}
	if o.Policy == "" {
		return false
	}
	if mode == "check-license" {
		return o.Input == "" && o.Output == ""
	}
	return o.Input != "" && o.Output != "" && o.Input != o.Output
}

type result struct {
	OperatorPolicy      any                   `json:"operator_policy,omitempty"`
	OperatorStatus      string                `json:"operator_status,omitempty"`
	FormatVersion       int                   `json:"format_version"`
	Success             bool                  `json:"success"`
	Code                string                `json:"code,omitempty"`
	Report              migrate.Report        `json:"report"`
	Plan                []migrate.PlannedStep `json:"plan,omitempty"`
	JournalSQL          string                `json:"journal_sql,omitempty"`
	Observation         any                   `json:"observation,omitempty"`
	CleanupState        string                `json:"cleanup_state"`
	RuntimeCleanupState string                `json:"runtime_cleanup_state,omitempty"`
}

func emptyResult() result {
	return result{FormatVersion: 1, Report: migrate.Report{Scope: migrate.Scope}, CleanupState: "not_created"}
}
func validMode(mode string) bool {
	if operatorMode(mode) {
		return true
	}
	switch mode {
	case "apply", "readiness", "reconcile", "retry", "grant", "check-runtime":
		return true
	}
	return false
}
func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
func run(args []string, out io.Writer) int {
	f := flag.NewFlagSet("dameng-schema", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	mode := f.String("mode", "", "explicit plan/apply/readiness/reconcile/retry/grant/check-runtime operation")
	configuration := f.String("config", "", "protected connector JSON reference")
	environment := f.String("env-file", "", "protected connector environment mapping")
	schema := f.String("schema-config", "", "protected schema operator JSON reference")
	schemaEnvironment := f.String("schema-env-file", "", "protected schema environment mapping")
	runtime := f.String("runtime-config", "", "protected second runtime connector for grant only")
	runtimeEnvironment := f.String("runtime-env-file", "", "protected runtime connector environment mapping for grant only")
	launch := f.Bool("print-launch-environment", false, "derive nonsecret native loader references without SQL")
	launchSettings := f.Bool("print-launch-settings", false, "derive typed child environment and working directory without SQL")
	policy := f.String("operator-policy", "", "protected native operator policy JSON")
	policyEnvironment := f.String("operator-policy-env-file", "", "protected native operator policy mapping")
	input := f.String("input", "", "protected bootstrap request reference")
	output := f.String("output", "", "exclusive protected bootstrap recovery intent reference")
	r := emptyResult()
	if f.Parse(args) != nil || f.NArg() != 0 || *launch && *launchSettings || !operatorArguments(*mode, nativeOperatorOptions{*policy, *policyEnvironment, *input, *output}) {
		r.Code = "DAMENG_SCHEMA_ARGUMENTS_INVALID"
	} else if *mode == "operator-policy-template" && !*launch && !*launchSettings && *configuration == "" && *environment == "" && *schema == "" && *schemaEnvironment == "" && *runtime == "" && *runtimeEnvironment == "" {
		r.OperatorPolicy = bootstrap.DamengPolicyTemplate()
		r.Success = true
	} else if *mode == "plan" && !*launch && !*launchSettings && *configuration == "" && *environment == "" && *schema == "" && *schemaEnvironment == "" && *runtime == "" && *runtimeEnvironment == "" {
		var e error
		r.Plan, e = migrate.Plan()
		r.Code = migrate.FailureCode(e)
		r.Success = e == nil
		r.Report.TotalSteps = len(r.Plan)
		r.JournalSQL = migrate.JournalSQL()
	} else if *configuration == "" || ((*launch || *launchSettings) && (*mode != "" || *schema != "" || *schemaEnvironment != "" || *runtime != "" || *runtimeEnvironment != "")) || (!*launch && !*launchSettings && (!validMode(*mode) || *schema == "" || (*mode == "grant" || operatorMode(*mode)) != (*runtime != "") || (*mode != "grant" && !operatorMode(*mode) && *runtimeEnvironment != ""))) {
		r.Code = "DAMENG_SCHEMA_ARGUMENTS_INVALID"
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
			sc, e := dameng.LoadSchemaConfiguration(*schema, *schemaEnvironment)
			if e != nil {
				r.Code = dameng.FailureCode(e)
			} else {
				var rc *dameng.ConnectionConfiguration
				if *mode == "grant" || operatorMode(*mode) {
					v, err := dameng.LoadConnectionConfiguration(*runtime, *runtimeEnvironment)
					e = err
					rc = &v
				}
				principal := sc.Schema
				if *mode == "check-runtime" {
					principal = sc.RuntimeUser
				}
				if e != nil {
					r.Code = dameng.FailureCode(e)
				} else if c.Schema != sc.Schema || c.User != principal || rc != nil && (rc.Schema != sc.Schema || rc.User != sc.RuntimeUser) {
					r.Code = "DAMENG_SCHEMA_IDENTITY_CONFIGURATION_MISMATCH"
				} else {
					r = operateNative(*mode, c, sc, rc, nativeOperatorOptions{*policy, *policyEnvironment, *input, *output})
				}
			}
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
