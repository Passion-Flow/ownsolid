package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/discovery"
	agent "github.com/Passion-Flow/ownsolid/services/agent/internal/runtime"
)

func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("AGENT_COMMAND_REQUIRED")
	}
	command := args[0]
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	config := flags.String("config", "", "Protected JSON configuration path")
	environment := flags.String("env-file", "", "Protected explicit typed agent environment file")
	ifNeeded := flags.Bool("if-needed", false, "Reuse a matching completed read-only enrollment")
	fencedNode := flags.String("confirm-fenced-node", "", "Already credential-revoked node ID, explicitly confirmed by the isolated operator")
	deployment := flags.String("deployment-id", "", "Exact owned deployment ID")
	receipt := flags.String("receipt-file", "", "New absolute private removal-receipt path")
	challenge := flags.String("challenge-file", "", "Protected audited exact workload removal challenge")
	preparation := flags.String("preparation-id", "", "Exact owned model preparation ID")
	trainingJob := flags.String("training-job-id", "", "Exact owned training job ID")
	registryTask := flags.String("task-id", "", "Exact existing Registry task journal UUID")
	registryManifest := flags.String("manifest-file", "", "Protected exported offline Registry manifest path")
	registryArchive := flags.String("archive-file", "", "Exact signed offline Registry archive path")
	disk := flags.String("disk-path", "/", "Existing filesystem path for facts command")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if *ifNeeded && command != "enroll" {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	registryCommand := command == agent.RegistryImportOffline || command == agent.RegistryReconcileOffline || command == agent.RegistryReconcileTask
	if registryCommand {
		validFlags := true
		flags.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "config", "env-file", "task-id", "manifest-file", "archive-file":
			default:
				validFlags = false
			}
		})
		options := agent.RegistryOptions{Operation: command, TaskID: *registryTask, ManifestFile: *registryManifest, ArchiveFile: *registryArchive}
		if !validFlags || options.Validate() != nil {
			return errors.New("AGENT_ARGUMENT_INVALID")
		}
	} else if *registryTask != "" || *registryManifest != "" || *registryArchive != "" {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	cleanupCommand := command == "remove-fenced-inference" || command == "remove-fenced-training" || command == "remove-fenced-preparation"
	if *environment != "" && command != "run" && command != "enroll" && !cleanupCommand && !registryCommand {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if command == "facts" {
		f, e := discovery.Collect(*disk)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(f)
	}
	if command != "enroll" && command != "run" && command != "remove-fenced-inference" && command != "remove-fenced-training" && command != "remove-fenced-preparation" && !registryCommand {
		return errors.New("AGENT_COMMAND_INVALID")
	}
	var c agent.Config
	var e error
	if command == "remove-fenced-preparation" {
		c, e = agent.LoadPreparationCleanupConfigWithEnvironment(*config, *environment)
	} else {
		c, e = agent.LoadConfigWithEnvironment(*config, *environment)
	}
	if e != nil {
		return e
	}
	if registryCommand {
		if e = agent.RunRegistryOperation(ctx, c, agent.RegistryOptions{Operation: command, TaskID: *registryTask, ManifestFile: *registryManifest, ArchiveFile: *registryArchive}); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Operation               string `json:"operation"`
			ImageState              string `json:"image_state"`
			TaskExecutionAuthorized bool   `json:"task_execution_authorized"`
		}{command, "verified_present", false})
	}
	if command == "remove-fenced-inference" {
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		defer cancel()
		return agent.RemoveFencedInference(bounded, c, *fencedNode, *deployment, *receipt)
	}
	if command == "remove-fenced-preparation" {
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		defer cancel()
		return agent.RemoveFencedPreparation(bounded, c, *fencedNode, *preparation, *challenge, *receipt)
	}
	if command == "remove-fenced-training" {
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		defer cancel()
		return agent.RemoveFencedTraining(bounded, c, *fencedNode, *trainingJob, *challenge, *receipt)
	}
	if command == "enroll" {
		bounded, cancel := context.WithTimeout(ctx, settings.Seconds(c.Policy().EnrollSeconds))
		defer cancel()
		if *ifNeeded {
			return agent.EnrollIfNeeded(bounded, c)
		}
		return agent.Enroll(bounded, c)
	}
	return agent.Run(ctx, c)
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := execute(ctx, os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		if e.Error() == "AGENT_CREDENTIAL_REVOKED" {
			os.Exit(77)
		}
		os.Exit(1)
	}
}
