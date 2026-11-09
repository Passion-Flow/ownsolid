package install

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func (o *operation) allocationPSQL(args ...string) []string {
	return composeArguments(o.config, append([]string{"exec", "--no-TTY", "postgres",
		"psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-U", migratorRole, "-d", maintenanceDatabase}, args...)...)
}

// Send CREATE only after the session holds the lock and confirms the vacant
// placement. Recovery can then wait for any dispatched CREATE to settle.
func (o *operation) allocateRestoreDatabase(ctx context.Context, i restoreIntent) (_ restoreIntent, err error) {
	const locked = "RESTORE_ALLOCATION_LOCKED"
	lockSQL := fmt.Sprintf("SELECT '%s' FROM pg_catalog.pg_advisory_lock(%d)", locked, restoreSwapLock)
	args := o.allocationPSQL("-c", lockSQL, "-c", i.catalogSQL(), "-f", "-")
	// Give the subprocess a file descriptor so it can exit with an error
	// before reading stdin without waiting on a Go copy goroutine.
	input, send, err := os.Pipe()
	if err != nil {
		return i, err
	}
	receive, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		diagnostics, err := o.stream.Stream(ctx, input, output, "docker", args...)
		input.Close()
		output.Close()
		if err != nil {
			err = fmt.Errorf("INSTALL_RESTORE_LOAD_FAILED: %w: %s", err, lastLines(diagnostics))
		}
		done <- err
	}()
	stop := context.AfterFunc(ctx, func() {
		send.Close()
		receive.CloseWithError(ctx.Err())
	})
	defer func() {
		send.Close()
		receive.Close()
		err = errors.Join(err, <-done)
		stop()
	}()

	response := bufio.NewScanner(receive)
	if !response.Scan() || response.Text() != locked || !response.Scan() || response.Text() != i.before().key() {
		return i, errors.Join(restoreIdentityError(), response.Err())
	}
	if _, err := fmt.Fprintf(send, "SET synchronous_commit = on;\nCREATE DATABASE \"%s\" TEMPLATE template0;\n%s;\n", i.incomingName(), i.catalogSQL()); err != nil {
		return i, err
	}
	if !response.Scan() {
		return i, errors.Join(restoreIdentityError(), response.Err())
	}
	catalog, err := parseRestoreCatalog(response.Text())
	if err != nil {
		return i, err
	}
	i.Incoming = catalog.incoming
	if catalog != i.before() || i.Incoming == 0 || i.Incoming == i.Original {
		return i, restoreIdentityError()
	}
	i.Phase = restoreLoading
	return i, writeReleaseRecord(o.config, restoreIntentName, i)
}

func (o *operation) recoverRestoreAllocation(ctx context.Context, i restoreIntent) error {
	// Take the snapshot in a separate statement after acquiring the lock.
	sql := fmt.Sprintf("BEGIN ISOLATION LEVEL READ COMMITTED;\nSELECT pg_catalog.pg_advisory_xact_lock(%d);\n%s;\nCOMMIT;", restoreSwapLock, i.catalogSQL())
	var answer bytes.Buffer
	diagnostics, err := o.stream.Stream(ctx, nil, &answer, "docker", o.allocationPSQL("-c", sql)...)
	if err != nil {
		return fmt.Errorf("INSTALL_RESTORE_ALLOCATION_CHECK_FAILED: %w: %s", err, lastLines(diagnostics))
	}
	catalog, err := parseRestoreCatalog(strings.TrimSpace(answer.String()))
	if err != nil {
		return err
	}
	observed := catalog.incoming
	catalog.incoming = 0
	if catalog != i.before() {
		return restoreIdentityError()
	}
	if observed != 0 {
		return fmt.Errorf("INSTALL_RESTORE_ALLOCATION_UNBOUND: preserved database %s (observed OID %d); inspect its ownership and move it aside if appropriate before running recover-restore again", i.incomingName(), observed)
	}
	if err := o.clearRestoreIntent(); err != nil {
		return err
	}
	o.record("restore", "rolled-back", "")
	fmt.Fprintln(o.out, "RESTORE_ALLOCATION_ROLLED_BACK")
	return nil
}
