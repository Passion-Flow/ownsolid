//go:build offline_fixture && linux

// This test-only observer reads one synthetic invitation through real SQL and
// mail decryption. It never sends mail or mutates product data/authorization.
package offlinemailbox_test

import (
	"context"
	"encoding/json"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	platformmail "github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const privateMount = "/fixture/private"
const resultFile = "/results/private/invitation.json"
const maximumSecretBytes = 1 << 20

type request struct {
	FormatVersion int    `json:"format_version"`
	DSNFile       string `json:"dsn_file"`
	KeyringFile   string `json:"keyring_file"`
	InvitationID  string `json:"invitation_id"`
	TenantID      string `json:"tenant_id"`
	WorkspaceID   string `json:"workspace_id"`
	Recipient     string `json:"recipient"`
	PortalOrigin  string `json:"portal_origin"`
	PostgresHost  string `json:"postgres_host"`
	PostgresPort  uint16 `json:"postgres_port"`
}

func secret(t *testing.T, name string) []byte {
	t.Helper()
	if filepath.Dir(name) != privateMount || filepath.Clean(name) != name {
		t.Fatal("MAILBOX_EXPLICIT_PRIVATE_REFERENCE_REQUIRED")
	}
	before, e := os.Lstat(name)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() < 1 || before.Size() > maximumSecretBytes {
		t.Fatal("MAILBOX_PRIVATE_FILE_REQUIRED")
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Getuid()) {
		t.Fatal("MAILBOX_PRIVATE_OWNER_REQUIRED")
	}
	fd, e := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		t.Fatal("MAILBOX_PRIVATE_OPEN_FAILED")
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	raw := make([]byte, before.Size())
	read, e := f.ReadAt(raw, 0)
	after, ae := f.Stat()
	named, ne := os.Lstat(name)
	if e != nil || read != len(raw) || ae != nil || ne != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		t.Fatal("MAILBOX_PRIVATE_INPUT_CHANGED")
	}
	return raw
}

func TestReadOneSyntheticInvitationWithoutDeliveryOrMutation(t *testing.T) {
	raw := os.Getenv("PROJECT_ONE_TEST_MAILBOX_REQUEST")
	if raw == "" {
		t.Skip("explicit isolated synthetic mailbox request required")
	}
	var r request
	if len(raw) > 8192 || wire.StrictJSON([]byte(raw), &r) != nil || r.FormatVersion != 1 || os.Getuid() == 0 || r.PostgresHost == "" || r.PostgresPort == 0 {
		t.Fatal("MAILBOX_REQUEST_INVALID")
	}
	for _, id := range []string{r.InvitationID, r.TenantID, r.WorkspaceID} {
		if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
			t.Fatal("MAILBOX_EXPLICIT_UUID_REQUIRED")
		}
	}
	email, e := mail.ParseAddress(r.Recipient)
	if e != nil || email.Address != r.Recipient || !strings.HasSuffix(r.Recipient, "@example.invalid") {
		t.Fatal("MAILBOX_SYNTHETIC_RECIPIENT_REQUIRED")
	}
	origin, e := url.Parse(r.PortalOrigin)
	if e != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		t.Fatal("MAILBOX_EXPLICIT_HTTPS_ORIGIN_REQUIRED")
	}
	dsn := secret(t, r.DSNFile)
	_ = secret(t, r.KeyringFile)
	connection, e := pgx.ParseConfig(strings.TrimSpace(string(dsn)))
	if e != nil || connection.Host != r.PostgresHost || connection.Port != r.PostgresPort || connection.Database == "" || connection.User == "" || connection.Password == "" {
		t.Fatal("MAILBOX_DATABASE_REFERENCE_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, e := pgx.ConnectConfig(ctx, connection)
	if e != nil {
		t.Fatal("MAILBOX_DATABASE_CONNECTION_FAILED")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		db.Close(cleanup)
	}()
	tx, e := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		t.Fatal("MAILBOX_READONLY_TRANSACTION_REQUIRED")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		tx.Rollback(cleanup)
	}()
	var delivery, key string
	var nonce, cipher []byte
	e = tx.QueryRow(ctx, `SELECT d.id::text,d.payload_key_id,d.payload_nonce,d.payload_ciphertext FROM mail_deliveries d JOIN invitations i ON i.id=d.invitation_id WHERE i.id=$1 AND i.tenant_id=$2 AND i.workspace_id=$3 AND i.email_canonical=$4 AND d.recipient_email=$4 AND d.purpose='invitation' AND i.status='pending' AND i.expires_at>clock_timestamp() AND i.token_generation=d.invitation_generation AND d.payload_key_id IS NOT NULL ORDER BY d.created_at DESC,d.id DESC LIMIT 1`, r.InvitationID, r.TenantID, r.WorkspaceID, r.Recipient).Scan(&delivery, &key, &nonce, &cipher)
	if e != nil {
		t.Fatal("MAILBOX_EXACT_ACTIVE_SYNTHETIC_DELIVERY_UNAVAILABLE")
	}
	keys, e := platformmail.LoadKeyring(r.KeyringFile)
	if e != nil {
		t.Fatal("MAILBOX_KEYRING_INVALID")
	}
	plain, e := keys.Decrypt(key, delivery, "invitation", nonce, cipher)
	if e != nil {
		t.Fatal("MAILBOX_DECRYPTION_FAILED")
	}
	var payload platformmail.Payload
	if wire.StrictJSON(plain, &payload) != nil {
		t.Fatal("MAILBOX_PAYLOAD_INVALID")
	}
	link, e := url.Parse(payload.Link)
	if e != nil || link.Scheme != origin.Scheme || link.Host != origin.Host || link.User != nil || link.Path != "/register" || link.RawQuery != "" || !regexp.MustCompile(`^token=[A-Za-z0-9_-]{43}$`).MatchString(link.Fragment) {
		t.Fatal("MAILBOX_SYNTHETIC_REGISTRATION_LINK_REQUIRED")
	}
	if tx.Commit(ctx) != nil {
		t.Fatal("MAILBOX_READONLY_TRANSACTION_FAILED")
	}
	parent, e := os.Lstat(filepath.Dir(resultFile))
	if e != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		t.Fatal("MAILBOX_PRIVATE_RESULT_DIRECTORY_REQUIRED")
	}
	owner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) {
		t.Fatal("MAILBOX_PRIVATE_RESULT_OWNER_REQUIRED")
	}
	output, e := os.OpenFile(resultFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal("MAILBOX_EXCLUSIVE_RESULT_REQUIRED")
	}
	result := struct {
		Status string `json:"status"`
		Link   string `json:"link"`
	}{Status: "observed_exact_synthetic_queued_invitation_no_SMTP_delivery_claim", Link: payload.Link}
	e = json.NewEncoder(output).Encode(result)
	syncError, closeError := output.Sync(), output.Close()
	if e != nil || syncError != nil || closeError != nil {
		t.Fatal("MAILBOX_PRIVATE_RESULT_WRITE_FAILED")
	}
	directory, e := os.Open(filepath.Dir(resultFile))
	if e != nil {
		t.Fatal("MAILBOX_PRIVATE_RESULT_DIRECTORY_UNAVAILABLE")
	}
	e = directory.Sync()
	directory.Close()
	if e != nil {
		t.Fatal("MAILBOX_PRIVATE_RESULT_DURABILITY_FAILED")
	}
}
