//go:build repository_integration

package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	stdmail "net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

const mailCrashInputEnvironment = "PROJECT_ONE_M01_MAIL_CRASH_INPUT"

type mailCrashInput struct {
	Configuration config.Config `json:"configuration"`
	Schema        string        `json:"schema"`
}

// Identified test pause: SQL remains real. The second transaction is finish,
// reached only after production Send has returned success from actual SMTP.
type mailCompletionPause struct {
	transaction.Store
	marker string
	calls  int
}

func (s *mailCompletionPause) Within(ctx context.Context, readOnly bool, operation func(context.Context, transaction.Executor) error) error {
	s.calls++
	if s.calls == 2 {
		f, e := os.OpenFile(s.marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString("accepted_before_completion\n")
		if e == nil {
			e = f.Sync()
		}
		closeError := f.Close()
		if e != nil {
			return e
		}
		if closeError != nil {
			return closeError
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Store.Within(ctx, readOnly, operation)
}

// A fixed subprocess helper, never a production execution entrance.
func TestM01CurrentMailWorkerCrashChild(t *testing.T) {
	path := os.Getenv(mailCrashInputEnvironment)
	if path == "" {
		t.Skip("fixed parent-owned private crash input required")
	}
	directory := filepath.Dir(path)
	rel, e := filepath.Rel(os.TempDir(), directory)
	info, statError := os.Lstat(directory)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) || statError != nil || !info.IsDir() || info.Mode().Perm() != 0700 || filepath.Base(path) != "input.json" {
		t.Fatal("MAIL_CRASH_PRIVATE_INPUT_REQUIRED")
	}
	raw, e := config.Secret(path)
	var input mailCrashInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e != nil || len(raw) > 64<<10 || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || !regexp.MustCompile(`^m01_[a-f0-9]{32}$`).MatchString(input.Schema) {
		t.Fatal("MAIL_CRASH_TYPED_INPUT_INVALID")
	}
	dsn, e := config.Secret(filepath.Join(directory, "database.dsn"))
	pc, parseError := pgxpool.ParseConfig(strings.TrimSpace(string(dsn)))
	if e != nil || parseError != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") || pc.ConnConfig.TLSConfig == nil || pc.ConnConfig.TLSConfig.InsecureSkipVerify {
		t.Fatal("MAIL_CRASH_ISOLATED_VERIFIED_DATABASE_REQUIRED")
	}
	pc.MaxConns = 2
	pc.ConnConfig.RuntimeParams["search_path"] = input.Schema
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("MAIL_CRASH_DATABASE_OPEN_FAILED")
	}
	defer db.Close()
	store, e := transaction.FromPostgreSQL(db)
	keys, keyError := mail.LoadKeyring(input.Configuration.Security.MailKeyringFile)
	templates, templateError := mail.LoadTemplates()
	if e != nil || keyError != nil || templateError != nil || input.Configuration.SMTP.Host != "127.0.0.1" || input.Configuration.SMTP.TLSMode != "starttls_required" {
		t.Fatal("MAIL_CRASH_LOCAL_DISPATCH_CONFIGURATION_REQUIRED")
	}
	outbox := mail.Outbox{Storage: &mailCompletionPause{Store: store, marker: filepath.Join(directory, "accepted-before-finish")}, Keys: keys, Templates: templates, Config: input.Configuration}
	_, _ = outbox.DispatchOne(ctx)
	t.Fatal("MAIL_CRASH_HELPER_RETURNED_WITHOUT_EXPECTED_SIGNAL")
}

func currentMailSMTP(t *testing.T, s *identity.Service) *smtpsink.Sink {
	t.Helper()
	sink := smtpsink.Start(t, false, false)
	host, port, e := net.SplitHostPort(sink.Listener.Addr().String())
	if e != nil || host != "127.0.0.1" {
		t.Fatal("CURRENT_MAIL_SYNTHETIC_LOOPBACK_REQUIRED")
	}
	s.Mail.Config.SMTP.Host = host
	s.Mail.Config.SMTP.Port, e = strconv.Atoi(port)
	if e != nil {
		t.Fatal("CURRENT_MAIL_SYNTHETIC_PORT_INVALID")
	}
	s.Mail.Config.SMTP.TLSMode = "starttls_required"
	s.Mail.Config.SMTP.CAFile = sink.CAFile
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 1
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 5
	return sink
}

func currentMailReceive(t *testing.T, sink *smtpsink.Sink) []byte {
	t.Helper()
	select {
	case raw := <-sink.Messages:
		return raw
	case <-time.After(5 * time.Second):
		t.Fatal("CURRENT_MAIL_SYNTHETIC_RECEIPT_TIMEOUT")
		return nil
	}
}

func currentMailMessageIdentity(t *testing.T, raw []byte, token string) string {
	t.Helper()
	message, e := stdmail.ReadMessage(bytes.NewReader(raw))
	if e != nil || message.Header.Get("Message-ID") == "" {
		t.Fatal("CURRENT_MAIL_MIME_HEADER_INVALID")
	}
	found := map[string]bool{}
	parts := 0
	var inspect func(io.Reader, string, int)
	inspect = func(body io.Reader, contentType string, depth int) {
		media, parameters, e := mime.ParseMediaType(contentType)
		if e != nil || depth > 1 || (media != "multipart/alternative" && media != "multipart/related") || parameters["boundary"] == "" {
			t.Fatal("CURRENT_MAIL_MIME_MULTIPART_INVALID")
		}
		reader := multipart.NewReader(body, parameters["boundary"])
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			parts++
			if e != nil || parts > 4 {
				t.Fatal("CURRENT_MAIL_MIME_PART_BOUND_EXCEEDED")
			}
			kind, _, e := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if e != nil {
				t.Fatal("CURRENT_MAIL_MIME_PART_TYPE_INVALID")
			}
			if kind == "multipart/alternative" {
				inspect(part, part.Header.Get("Content-Type"), depth+1)
			} else {
				if part.Header.Get("Content-Transfer-Encoding") != "base64" || (kind != "text/plain" && kind != "text/html" && kind != "image/png") {
					t.Fatal("CURRENT_MAIL_MIME_LEAF_ENCODING_INVALID")
				}
				decoded, e := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, part), 65537))
				if e != nil || len(decoded) > 65536 {
					t.Fatal("CURRENT_MAIL_MIME_LEAF_BOUND_EXCEEDED")
				}
				if kind != "image/png" {
					if found[kind] || !bytes.Contains(decoded, []byte(token)) {
						t.Fatal("CURRENT_MAIL_DECODED_TOKEN_MISSING_OR_DUPLICATE_PART")
					}
					found[kind] = true
				}
			}
			part.Close()
		}
	}
	inspect(message.Body, message.Header.Get("Content-Type"), 0)
	if len(found) != 2 {
		t.Fatal("CURRENT_MAIL_BOTH_RENDERED_PARTS_REQUIRED")
	}
	return message.Header.Get("Message-ID")
}

func TestM01CurrentHTTPMailAcceptedWorkerCrash(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	sink := currentMailSMTP(t, s)
	invite := currentHTTPInvite(admin, "crash-mail@example.invalid")
	token := fixture.InvitationToken(t, s, invite["id"].(string))
	directory := filepath.Join(t.TempDir(), "mail-crash-private")
	if os.Mkdir(directory, 0700) != nil {
		t.Fatal("MAIL_CRASH_PRIVATE_ROOT_FAILED")
	}
	var schema, delivery string
	if s.DB.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema) != nil || s.DB.QueryRow(t.Context(), "SELECT id::text FROM mail_deliveries WHERE invitation_id=$1", invite["id"]).Scan(&delivery) != nil {
		t.Fatal("MAIL_CRASH_OWNED_SCOPE_UNAVAILABLE")
	}
	configuration := s.Mail.Config
	configuration.Security.MailKeyringFile = filepath.Join(directory, "keyring.json")
	keyring, e := json.Marshal(s.Mail.Keys)
	if e != nil || os.WriteFile(configuration.Security.MailKeyringFile, keyring, 0600) != nil {
		t.Fatal("MAIL_CRASH_PRIVATE_KEYRING_WRITE_FAILED")
	}
	raw, e := json.Marshal(mailCrashInput{Configuration: configuration, Schema: schema})
	input := filepath.Join(directory, "input.json")
	if e != nil || os.WriteFile(input, raw, 0600) != nil || os.WriteFile(filepath.Join(directory, "database.dsn"), []byte(s.DB.Config().ConnString()), 0600) != nil {
		t.Fatal("MAIL_CRASH_PRIVATE_INPUT_WRITE_FAILED")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal("MAIL_CRASH_OWNED_EXECUTABLE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestM01CurrentMailWorkerCrashChild$", "-test.timeout=25s")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, mailCrashInputEnvironment+"=") {
			child.Env = append(child.Env, value)
		}
	}
	child.Env = append(child.Env, mailCrashInputEnvironment+"="+input)
	child.Stdout, child.Stderr = io.Discard, io.Discard
	if child.Start() != nil {
		t.Fatal("MAIL_CRASH_OWNED_CHILD_START_FAILED")
	}
	joined := false
	defer func() {
		if !joined {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	first := currentMailReceive(t, sink)
	marker := filepath.Join(directory, "accepted-before-finish")
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waitBoundary:
	for {
		if value, e := os.ReadFile(marker); e == nil && string(value) == "accepted_before_completion\n" {
			break waitBoundary
		}
		select {
		case <-deadline.C:
			t.Fatal("MAIL_CRASH_COMPLETION_BOUNDARY_TIMEOUT")
		case <-ticker.C:
		}
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE id=$1 AND state='sending' AND attempt_count=1 AND lease_token IS NOT NULL AND payload_ciphertext IS NOT NULL AND completed_at IS NULL", 1, delivery)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('mail.sent','mail.failed')", 0, delivery)
	if child.Process.Kill() != nil {
		t.Fatal("MAIL_CRASH_OWNED_CHILD_SIGNAL_FAILED")
	}
	waitError := child.Wait()
	joined = true
	status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
	if waitError == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("MAIL_CRASH_EXPECTED_SIGKILL_NOT_OBSERVED")
	}
	currentHTTPExec(t, s, "UPDATE mail_deliveries SET locked_until=clock_timestamp()-interval '1 second' WHERE id=$1", delivery)
	recovered := *s.Mail
	if active, e := recovered.DispatchOne(t.Context()); e != nil || !active {
		t.Fatal("MAIL_CRASH_FRESH_DISPATCHER_RECOVERY_FAILED")
	}
	second := currentMailReceive(t, sink)
	if currentMailMessageIdentity(t, first, token) != currentMailMessageIdentity(t, second, token) {
		t.Fatal("MAIL_CRASH_DUPLICATE_MESSAGE_LINK_OR_ID_CHANGED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE id=$1 AND state='sent' AND attempt_count=2 AND lease_token IS NULL AND payload_ciphertext IS NULL AND completed_at IS NOT NULL", 1, delivery)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='mail.sent'", 1, delivery)
	body := map[string]any{"token": token, "display_name": "Synthetic crash recovery", "password": fixture.Password()}
	portal.mutate("POST", "/api/v1/auth/register", body, 201, "RegisterResponse")
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/register", body, 400, "Problem"), "TOKEN_UNUSABLE")
	currentHTTPCount(t, s, "SELECT count(*) FROM users WHERE email_canonical='crash-mail@example.invalid'", 1)
	t.Log("CURRENT_M01_MAIL_CRASH actual_owned_dispatcher_child_sigkill=true production_send_accepted_before_finish_pause=true sending_sql_survives=true child_wait_joined=true fresh_dispatcher_recovered_lease=true duplicate_receipts=2 stable_message_id_and_token=true terminal_audit_once=true registration_201_then_400=true")
}
