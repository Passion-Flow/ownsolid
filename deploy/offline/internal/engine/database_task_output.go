package engine

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumTaskOutputBytes = 4096
const dockerOutputHeaderBytes = 8
const maximumTaskOutputFrames = 512
const maximumTaskAttachStreamBytes = 16 * 1024

// Only exact owned backend protocol codes may enter diagnostics. Arbitrary
// process output (including a credential-shaped uppercase string) is redacted.
var databaseOutputCodes = map[string]bool{
	"RELEASES_UPGRADE_COMPLETE": true,
	"SCHEMA_CURRENT":            true, "MIGRATION_COMPLETE": true, "RESTORED_AUTH_INVALIDATED": true, "MODEL_FILE_OPERATION_COMPLETE": true, "DATASET_FILE_OPERATION_COMPLETE": true, "DATASET_REFERENCED_FILES_VERIFIED": true,
	"CONFIG_UNREADABLE": true, "CONFIG_REQUIRED": true, "AUTH_KEY_INVALID": true, "DATABASE_UNAVAILABLE": true, "DATABASE_CONFIG_INVALID": true, "STORAGE_UNAVAILABLE": true, "OPERATOR_LOCK_UNAVAILABLE": true, "INSTALLATION_PROCESS_ALREADY_RUNNING": true,
	"RUNTIME_ACCESS_AMBIENT_DRIVER_CONFIGURATION_REFUSED": true, "RUNTIME_ACCESS_COMMIT_UNCERTAIN": true, "RUNTIME_ACCESS_CONNECTION_CONTEXT_INVALID": true, "RUNTIME_ACCESS_CONTEXT_INVALID": true, "RUNTIME_ACCESS_ELEVATED_OR_SCHEMA_PRIVILEGE_CONFLICT": true,
	"RUNTIME_ACCESS_FILE_PARENT_INVALID": true, "RUNTIME_ACCESS_FILE_PATH_INVALID": true, "RUNTIME_ACCESS_GRANT_FAILED": true, "RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED": true, "RUNTIME_ACCESS_INSTALLATION_LOCK_REQUIRED": true, "RUNTIME_ACCESS_LOGIN_FAILED": true, "RUNTIME_ACCESS_LOGIN_IDENTITY_MISMATCH": true, "RUNTIME_ACCESS_NOT_COMMITTED": true,
	"RUNTIME_ACCESS_PRIVATE_FILE_CHANGED": true, "RUNTIME_ACCESS_PRIVATE_FILE_INVALID": true, "RUNTIME_ACCESS_PRIVATE_TRANSACTION_UNAVAILABLE": true, "RUNTIME_ACCESS_PRIVILEGED_OPERATOR_REQUIRED": true, "RUNTIME_ACCESS_PRODUCT_TABLE_PRIVILEGE_CONFLICT": true, "RUNTIME_ACCESS_RESTORE_AUDIT_FAILED": true, "RUNTIME_ACCESS_RESTORE_CONTEXT_INVALID": true, "RUNTIME_ACCESS_RESTORE_STATE_MISMATCH": true, "RUNTIME_ACCESS_RESTORE_TARGET_MISMATCH": true,
	"RUNTIME_ACCESS_ROLE_CREATE_FAILED": true, "RUNTIME_ACCESS_ROLE_OR_CREDENTIAL_CONFLICT": true, "RUNTIME_ACCESS_ROLE_UNAVAILABLE": true, "RUNTIME_ACCESS_RUNTIME_TARGET_MISMATCH": true, "RUNTIME_ACCESS_SCHEMA_INCOMPATIBLE": true, "RUNTIME_ACCESS_SEQUENCE_PRIVILEGE_CONFLICT": true, "RUNTIME_ACCESS_TABLE_CATALOG_INCOMPATIBLE": true, "RUNTIME_ACCESS_TABLE_PRIVILEGES_UNAVAILABLE": true, "RUNTIME_ACCESS_TARGET_MISMATCH": true, "RUNTIME_ACCESS_TARGET_UNAVAILABLE": true, "RUNTIME_ACCESS_TRANSACTION_UNAVAILABLE": true, "RUNTIME_ACCESS_UNRELATED_TABLE_PRIVILEGE_CONFLICT": true, "RUNTIME_ACCESS_VERIFIED": true,
	"RUNTIME_CREDENTIAL_FORMAT_INVALID": true, "RUNTIME_CREDENTIAL_GENERATION_FAILED": true, "RUNTIME_DSN_CONFIG_INVALID": true, "RUNTIME_DSN_EXPLICIT_TLS_REFERENCES_REQUIRED": true, "RUNTIME_DSN_EXPLICIT_TRANSPORT_REQUIRED": true, "RUNTIME_DSN_PARAMETER_UNSUPPORTED": true, "RUNTIME_DSN_SINGLE_ENDPOINT_URI_REQUIRED": true, "RUNTIME_SCRAM_DERIVATION_FAILED": true, "RUNTIME_SCRAM_PARAMETERS_UNSUPPORTED": true,
}

type TaskOutputObservation struct {
	NativePostgreSQLRestoreRootsInspection *NativePostgreSQLRestoreRootsInspectionOutput `json:"-"`
	NativePostgreSQLTargetInitialization   *NativePostgreSQLTargetInitializationOutput   `json:"-"`
	NativePostgreSQLBackupCompletion       *NativePostgreSQLBackupCompletion             `json:"-"`
	NativePostgreSQLRestoreCompletion      *NativePostgreSQLRestoreCompletion            `json:"-"`
	NativePostgreSQLRestore                *NativePostgreSQLRestoreObservation           `json:"-"`
	NativePostgreSQLBackup                 *NativePostgreSQLBackupObservation            `json:"-"`
	StdoutCode                             string                                        `json:"stdout_code,omitempty"`
	StderrCode                             string                                        `json:"stderr_code,omitempty"`
	Redacted                               bool                                          `json:"redacted"`
	Bytes                                  int                                           `json:"bytes"`
	Qualification                          string                                        `json:"qualification"`
}
type taskOutputResult struct {
	value TaskOutputObservation
	err   error
}
type TaskOutputObserver struct {
	conn net.Conn
	done <-chan taskOutputResult
	stop func() bool
}

func (o *TaskOutputObserver) Close() error { o.stop(); return o.conn.Close() }
func (o *TaskOutputObserver) Wait(ctx context.Context) (TaskOutputObservation, error) {
	select {
	case result := <-o.done:
		return result.value, result.err
	case <-ctx.Done():
		o.Close()
		return TaskOutputObservation{}, failure("OFFLINE_ENGINE_TASK_OUTPUT_INTERRUPTED")
	}
}

func readDatabaseTaskOutput(reader io.Reader) (TaskOutputObservation, error) {
	result := TaskOutputObservation{Qualification: "sanitized_fixed_command_output_only_not_process_success_or_restored_qualification"}
	streams := map[byte][]byte{1: {}, 2: {}}
	frames := 0
	for {
		header := make([]byte, dockerOutputHeaderBytes)
		_, e := io.ReadFull(reader, header)
		if e == io.EOF {
			break
		}
		if e != nil || header[0] != 1 && header[0] != 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return result, failure("OFFLINE_ENGINE_TASK_OUTPUT_FRAME_INVALID")
		}
		frames++
		if frames > maximumTaskOutputFrames {
			return result, failure("OFFLINE_ENGINE_TASK_OUTPUT_LIMIT")
		}
		length := binary.BigEndian.Uint32(header[4:])
		if length > uint32(maximumTaskOutputBytes-result.Bytes) {
			return result, failure("OFFLINE_ENGINE_TASK_OUTPUT_LIMIT")
		}
		body := make([]byte, int(length))
		if _, e = io.ReadFull(reader, body); e != nil {
			return result, failure("OFFLINE_ENGINE_TASK_OUTPUT_FRAME_INVALID")
		}
		result.Bytes += len(body)
		streams[header[0]] = append(streams[header[0]], body...)
	}
	for stream, body := range streams {
		code := strings.TrimSpace(string(body))
		if code == "" {
			continue
		}
		if !databaseOutputCodes[code] {
			result.Redacted = true
			continue
		}
		if stream == 1 {
			result.StdoutCode = code
		} else {
			result.StderrCode = code
		}
	}
	return result, nil
}

// Attach before the single physical start. No log driver, stdin, arbitrary ID
// or mutation/retry is introduced; the existing exact task guards still apply.
func (c *Client) ObserveDatabaseTaskOutput(ctx context.Context, t DatabaseTask, id string) (*TaskOutputObserver, error) {
	t = t.freezeNativeVerifier().freezeNativeRecovery().freezeNativeTargetInitialization().freezeNativeRestoreRoots()
	state, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return nil, e
	}
	if state.State.Status != "created" {
		return nil, failure("OFFLINE_ENGINE_NEVER_STARTED_TASK_OUTPUT_REQUIRED")
	}
	dial := net.Dialer{Timeout: time.Duration(c.config.DialSeconds) * time.Second}
	conn, e := dial.DialContext(ctx, "unix", c.socket)
	if e != nil {
		return nil, failure("OFFLINE_ENGINE_TASK_OUTPUT_CONNECT_FAILED")
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	fail := func(code string) (*TaskOutputObserver, error) { stop(); conn.Close(); return nil, failure(code) }
	conn.SetDeadline(time.Now().Add(time.Duration(c.config.RequestSeconds) * time.Second))
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/attach?" + (url.Values{"stream": {"1"}, "logs": {"0"}, "stdin": {"0"}, "stdout": {"1"}, "stderr": {"1"}}).Encode()
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, address, nil)
	if e != nil {
		return fail("OFFLINE_ENGINE_TASK_OUTPUT_REQUEST_INVALID")
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "tcp")
	if request.Write(conn) != nil {
		return fail("OFFLINE_ENGINE_TASK_OUTPUT_ATTACH_FAILED")
	}
	streamLimit := int64(maximumTaskAttachStreamBytes)
	if t.NativePostgreSQLBackupVerifier != nil {
		streamLimit += t.NativePostgreSQLBackupVerifier.Request.Policy.MaximumManifestBytes + int64(maximumTaskOutputFrames*dockerOutputHeaderBytes)
	}
	if t.NativePostgreSQLRecovery != nil {
		streamLimit += t.nativeRecoveryPolicy().MaximumManifestBytes + int64(maximumTaskOutputFrames*dockerOutputHeaderBytes)
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		streamLimit += t.NativePostgreSQLTargetInitialization.Request.Policy.MaximumRecordBytes + int64(maximumTaskOutputFrames*dockerOutputHeaderBytes)
	}
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		streamLimit += t.NativePostgreSQLRestoreRootsInspection.Request.Policy.MaximumRecordBytes + int64(maximumTaskOutputFrames*dockerOutputHeaderBytes)
	}
	reader := bufio.NewReader(io.LimitReader(conn, streamLimit))
	response, e := http.ReadResponse(reader, request)
	if e != nil || response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), "tcp") {
		return fail("OFFLINE_ENGINE_TASK_OUTPUT_ATTACH_REFUSED")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Duration(c.config.LoadSeconds) * time.Second)
	}
	conn.SetDeadline(deadline)
	done := make(chan taskOutputResult, 1)
	go func() {
		var value TaskOutputObservation
		var e error
		if t.NativePostgreSQLRestoreRootsInspection != nil {
			value, e = readNativePostgreSQLRestoreRootsInspectionOutput(reader, t)
		} else if t.NativePostgreSQLTargetInitialization != nil {
			value, e = readNativePostgreSQLTargetInitializationOutput(reader, t)
		} else if t.NativePostgreSQLBackupVerifier != nil {
			value, e = readNativePostgreSQLVerifierOutput(reader, t)
		} else if t.NativePostgreSQLRecovery != nil {
			value, e = readNativePostgreSQLRecoveryOutput(reader, t)
		} else {
			value, e = readDatabaseTaskOutput(reader)
		}
		if e != nil {
			e = failure("OFFLINE_ENGINE_TASK_OUTPUT_STREAM_FAILED")
		}
		conn.Close()
		done <- taskOutputResult{value, e}
	}()
	return &TaskOutputObserver{conn: conn, done: done, stop: stop}, nil
}
