package mail_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestM01MailCrypto(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	path := filepath.Join(t.TempDir(), "ring")
	b, _ := json.Marshal(map[string]any{"active": "a", "keys": map[string]string{"a": base64.StdEncoding.EncodeToString(key)}})
	os.WriteFile(path, b, 0600)
	ring, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	kid, nonce, cipher, e := ring.Encrypt(id, "invitation", []byte("synthetic-secret"))
	if e != nil {
		t.Fatal(e)
	}
	plain, e := ring.Decrypt(kid, id, "invitation", nonce, cipher)
	if e != nil || string(plain) != "synthetic-secret" {
		t.Fatal("decrypt failed")
	}
	if _, e = ring.Decrypt(kid, uuid.NewString(), "invitation", nonce, cipher); e == nil {
		t.Fatal("AAD identity bypass")
	}
	original := append([]byte(nil), cipher...)
	cipher[0] ^= 1
	if _, e = ring.Decrypt(kid, id, "invitation", nonce, cipher); e == nil {
		t.Fatal("tamper accepted")
	}
	if _, e = ring.Decrypt("missing", id, "invitation", nonce, cipher); e == nil {
		t.Fatal("missing key bypass")
	}
	newer := make([]byte, 32)
	rand.Read(newer)
	b, _ = json.Marshal(map[string]any{"active": "b", "keys": map[string]string{"a": base64.StdEncoding.EncodeToString(key), "b": base64.StdEncoding.EncodeToString(newer)}})
	os.WriteFile(path, b, 0600)
	rotated, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rotated.Decrypt("a", id, "invitation", nonce, original); e != nil {
		t.Fatal("retained decryption key could not decrypt queued work")
	}
	active, _, _, e := rotated.Encrypt(id, "invitation", []byte("synthetic"))
	if e != nil || active != "b" {
		t.Fatal("active encryption key did not rotate")
	}
}
func TestM01TLSMailDispatch(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(implicit), func(t *testing.T) {
			s := fixture.New(t)
			password := fixture.Bootstrap(t, s)
			a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
			sink := smtpsink.Start(t, implicit, false)
			host, port, _ := net.SplitHostPort(sink.Listener.Addr().String())
			s.Mail.Config.SMTP.Host = host
			s.Mail.Config.SMTP.Port, _ = strconv.Atoi(port)
			s.Mail.Config.SMTP.CAFile = sink.CAFile
			s.Mail.Config.SMTP.ConnectTimeoutSeconds = 5
			s.Mail.Config.SMTP.OperationTimeoutSeconds = 20
			s.Mail.Config.SMTP.TLSMode = "starttls_required"
			if implicit {
				s.Mail.Config.SMTP.TLSMode = "implicit_tls"
			}
			invite, e := s.Invite(context.Background(), a, "", "createInvitation", uuid.NewString(), "recipient@example.invalid", "zh-CN", 0, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			token := fixture.InvitationToken(t, s, invite.ID)
			active, e := s.Mail.DispatchOne(context.Background())
			if e != nil || !active {
				t.Fatal("dispatch failed", e)
			}
			select {
			case message := <-sink.Messages:
				if !strings.Contains(string(message), "Content-Type: multipart/alternative") || !strings.Contains(string(message), "Message-ID:") {
					t.Fatal("invalid MIME")
				}
			case <-time.After(time.Second):
				t.Fatal("SMTP receiver did not receive")
			}
			var state string
			var ciphertext []byte
			if e = s.DB.QueryRow(context.Background(), "SELECT state,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", invite.ID).Scan(&state, &ciphertext); e != nil || state != "sent" || ciphertext != nil {
				t.Fatal("terminal secret not cleared", e)
			}
			if _, e = s.ValidateInvitation(context.Background(), token); e != nil {
				t.Fatal("delivered token unusable", e)
			}
		})
	}
}
func TestM01MailTLSRejectsDowngrade(t *testing.T) {
	s := fixture.New(t)
	for _, scenario := range []string{"no-starttls", "unknown-ca", "hostname"} {
		t.Run(scenario, func(t *testing.T) {
			sink := smtpsink.Start(t, false, scenario == "no-starttls")
			host, port, _ := net.SplitHostPort(sink.Listener.Addr().String())
			c := s.Config
			c.SMTP.Host = host
			c.SMTP.Port, _ = strconv.Atoi(port)
			c.SMTP.TLSMode = "starttls_required"
			c.SMTP.ConnectTimeoutSeconds = 1
			c.SMTP.OperationTimeoutSeconds = 2
			if scenario != "unknown-ca" {
				c.SMTP.CAFile = sink.CAFile
			}
			if scenario == "hostname" {
				cert, ca, _, _ := smtpsink.Certificate(t, t.TempDir(), "wrong-host.example.invalid")
				sink.SetCertificate(cert)
				c.SMTP.CAFile = ca
			}
			if e := mail.Send(context.Background(), c, "test@example.invalid", []byte("synthetic")); e == nil {
				t.Fatal("insecure relay accepted")
			}
			select {
			case <-sink.Messages:
				t.Fatal("message transmitted despite TLS failure")
			default:
			}
		})
	}
}
func TestM01ObsoleteAndRetryingMail(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	s.Mail.Config.SMTP.Host = "127.0.0.1"
	s.Mail.Config.SMTP.Port = 1
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 1
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 2
	s.Mail.Config.SMTP.TLSMode = "starttls_required"
	i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "test@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mail.DispatchOne(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	var attempts int
	var cipher []byte
	if e = s.DB.QueryRow(ctx, "SELECT state,attempt_count,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", i.ID).Scan(&state, &attempts, &cipher); e != nil || state != "queued" || attempts != 1 || len(cipher) == 0 {
		t.Fatal("retry work lost", e)
	}
	if _, e = s.RevokeInvitation(ctx, a, i.ID, i.Version, "Synthetic revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT state,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", i.ID).Scan(&state, &cipher); e != nil || state != "cancelled" || cipher != nil {
		t.Fatal("obsolete ciphertext retained", e)
	}
	i, e = s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "tamper@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE mail_deliveries SET payload_ciphertext='corrupted'::bytea WHERE invitation_id=$1", i.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mail.DispatchOne(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT state,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", i.ID).Scan(&state, &cipher); e != nil || state != "failed" || cipher != nil {
		t.Fatal("tampered payload did not fail safely", e)
	}
}

func TestM01MailLeaseRecovery(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	sink := smtpsink.Start(t, false, false)
	host, port, _ := net.SplitHostPort(sink.Listener.Addr().String())
	s.Mail.Config.SMTP.Host = host
	s.Mail.Config.SMTP.Port, _ = strconv.Atoi(port)
	s.Mail.Config.SMTP.TLSMode = "starttls_required"
	s.Mail.Config.SMTP.CAFile = sink.CAFile
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 5
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 20
	i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "lease@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	token := fixture.InvitationToken(t, s, i.ID)
	stale := uuid.NewString()
	if _, e = s.DB.Exec(ctx, "UPDATE mail_deliveries SET state='sending',lease_token=$2,locked_until=clock_timestamp()-interval '1 second',attempt_count=1 WHERE invitation_id=$1", i.ID, stale); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mail.DispatchOne(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-sink.Messages:
	case <-time.After(time.Second):
		t.Fatal("recovered lease did not send")
	}
	// This conditional update is the production fencing predicate after a stale worker resumes.
	result, e := s.DB.Exec(ctx, "UPDATE mail_deliveries SET error_category='stale_worker' WHERE invitation_id=$1 AND state='sending' AND lease_token=$2", i.ID, stale)
	if e != nil || result.RowsAffected() != 0 {
		t.Fatal("stale lease overwrote current state")
	}
	password := fixture.Password()
	if e = s.Register(ctx, token, "Lease", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = s.Register(ctx, token, "Lease", password, uuid.NewString()); e == nil {
		t.Fatal("mail replay registered twice")
	}
}

func TestConfiguredRetryBudgetCancelsPreviouslyQueuedExcessAttempts(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	password := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	invite, err := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "retry-policy@example.invalid", "en", 0, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE mail_deliveries SET attempt_count=2,error_category='smtp_transient' WHERE invitation_id=$1", invite.ID); err != nil {
		t.Fatal(err)
	}
	policy := s.Config.Policy()
	policy.Mail.MaxAttempts = 1
	s.Mail.Config.Runtime = &policy
	if _, err = s.Mail.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	var cipher []byte
	if err = s.DB.QueryRow(ctx, "SELECT state,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", invite.ID).Scan(&state, &cipher); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || len(cipher) != 0 {
		t.Fatal("lowered retry budget stranded an encrypted queued delivery")
	}
	body, err := s.Mail.Render(uuid.NewString(), "password_reset", "en", "recipient@example.invalid", mail.Payload{Link: "https://portal.example.invalid/reset-password#token=synthetic"}, "stored-id@old-sender.example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Message-ID: <stored-id@old-sender.example.invalid>") {
		t.Fatal("persisted message identity changed across sender reconfiguration")
	}
}
