package main

import (
	"io"
	"slices"
)

// These bounds limit diagnostic traversal, including malformed cyclic wrappers.
// The ordinary terminal error and process outcome remain unchanged.
const processErrorNodes = 64
const processErrorDepth = 8

// reportProcessFailure emits only fixed composition codes. Never print a vendor
// error, wrapper text, configuration value or arbitrary error leaf here.
func reportProcessFailure(out io.Writer, failure error) {
	var codes []string
	remaining := processErrorNodes
	var visit func(error, int)
	visit = func(e error, depth int) {
		if e == nil || depth > processErrorDepth || remaining == 0 {
			return
		}
		remaining--
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child, depth+1)
				if remaining == 0 {
					break
				}
			}
			return
		}
		if wrapped, ok := e.(interface{ Unwrap() error }); ok {
			visit(wrapped.Unwrap(), depth+1)
			return
		}
		code := e.Error()
		if processFailureCode(code) && !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	visit(failure, 0)
	slices.Sort(codes)
	for _, code := range codes {
		_, _ = io.WriteString(out, "CONTROL_FAILURE_CODE "+code+"\n")
	}
}

func processFailureCode(code string) bool {
	switch code {
	case "CONTROL_SCHEMA_VERIFICATION_FAILED", "CONTROL_SHUTDOWN_TIMEOUT",
		"CONTROL_WORKERS_NOT_JOINED", "CONTROL_STORE_CLOSE_FAILED",
		"INSTALLATION_LOCK_RELEASE_UNCONFIRMED", "INSTALLATION_LOCK_LOST",
		"SERVER_LISTEN_FAILED", "SERVER_FAILED", "SSO_MAINTENANCE_FAILED",
		"STORAGE_COMMIT_OUTCOME_UNKNOWN", "STORAGE_COMMIT_ROLLED_BACK":
		return true
	}
	for _, worker := range []string{
		"NODE_WORKER", "MODEL_WORKER", "DATASET_PROCESSOR", "TRAINING_WORKER",
		"PREPARATION_PROCESSOR", "SSO_MAINTENANCE", "KNOWLEDGE_PROCESSOR",
		"MAIL_WORKER", "RETENTION_WORKER", "MIDDLEWARE_WORKER", "ARTIFACT_INGRESS",
		"TASK_DELIVERY_WORKER", "NODE_RECONCILIATION", "MODEL_IMPORT",
		"TRAINING_PROCESSOR", "DAMENG_READINESS",
	} {
		if code == worker+"_FAILED" || code == worker+"_STOPPED" {
			return true
		}
	}
	return false
}
