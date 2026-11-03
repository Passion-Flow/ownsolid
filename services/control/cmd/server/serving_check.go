package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

const servingCheckCommand = servingcheck.Command

type servingCheckFlag struct {
	value string
	seen  bool
}

func (v *servingCheckFlag) String() string { return "" }
func (v *servingCheckFlag) Set(value string) error {
	if v.seen {
		return errors.New("SERVING_TRANSPORT_ARGUMENT_INVALID")
	}
	v.value, v.seen = value, true
	return nil
}

func servingCheckPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

// This read-only command has no signal trap, database or worker constructor.
// Its caller owns bounded process supervision for a hung filesystem syscall.
func runServingCheckCommand(args []string, output io.Writer) error {
	argumentError := errors.New("SERVING_TRANSPORT_ARGUMENT_INVALID")
	flags := flag.NewFlagSet(servingCheckCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := map[string]*servingCheckFlag{}
	for _, name := range []string{"config", "env-file", "expected-server-json", "expected-machine-json", "expected-application-profile", "expected-release-view-json"} {
		value := &servingCheckFlag{}
		values[name] = value
		flags.Var(value, name, "Explicit serving transport verification input")
	}
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 || output == nil {
		return argumentError
	}
	for _, name := range []string{"config", "expected-server-json"} {
		if !values[name].seen || values[name].value == "" {
			return argumentError
		}
	}
	if !servingCheckPath(values["config"].value) || values["env-file"].seen && !servingCheckPath(values["env-file"].value) {
		return argumentError
	}
	if values["expected-machine-json"].seen && values["expected-machine-json"].value == "" {
		return argumentError
	}
	if values["expected-application-profile"].seen && values["expected-application-profile"].value != config.MVPPostgreSQLCPUV1Profile {
		return argumentError
	}
	expected, e := servingcheck.ParseExpected([]byte(values["expected-server-json"].value))
	if e != nil {
		return e
	}
	var machine *config.MachineServerConfiguration
	if values["expected-machine-json"].seen {
		value, e := servingcheck.ParseExpectedMachine([]byte(values["expected-machine-json"].value))
		if e != nil {
			return e
		}
		machine = &value
	}
	c, e := config.LoadProtectedWithEnvironment(values["config"].value, values["env-file"].value)
	if e != nil {
		return errors.New("SERVING_TRANSPORT_CONFIGURATION_INVALID")
	}
	if values["expected-application-profile"].seen && c.Server.Profile() != values["expected-application-profile"].value {
		return errors.New("SERVING_APPLICATION_PROFILE_MISMATCH")
	}
	if values["expected-release-view-json"].seen {
		policy, err := view.ParseConfiguration([]byte(values["expected-release-view-json"].value))
		if err != nil {
			return argumentError
		}
		actual, err := releases.Load(c.ReleaseViewConfigFile)
		if err != nil || actual != policy {
			return errors.New("SERVING_RELEASE_VIEW_POLICY_MISMATCH")
		}
		if err = releases.VerifyProjection(actual); err != nil {
			return err
		}
	}
	if e := servingcheck.VerifyWithMachine(c, expected, machine); e != nil {
		return e
	}
	if _, e := fmt.Fprintln(output, servingcheck.Success); e != nil {
		return errors.New("SERVING_TRANSPORT_OUTPUT_FAILED")
	}
	return nil
}
