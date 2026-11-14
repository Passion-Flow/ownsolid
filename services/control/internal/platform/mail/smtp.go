package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strconv"
	"time"
)

func base64Body(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
func Send(ctx context.Context, c config.Config, recipient string, message []byte) error {
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if c.SMTP.CAFile != "" {
		b, e := os.ReadFile(c.SMTP.CAFile)
		if e != nil || !roots.AppendCertsFromPEM(b) {
			return errors.New("smtp_tls_rejected")
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.SMTP.Host, RootCAs: roots}
	dialer := net.Dialer{Timeout: time.Duration(c.SMTP.ConnectTimeoutSeconds) * time.Second}
	conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.SMTP.Host, strconv.Itoa(c.SMTP.Port)))
	if e != nil {
		return errors.New("smtp_unavailable")
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Duration(c.SMTP.OperationTimeoutSeconds) * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if c.SMTP.TLSMode == "implicit_tls" {
		secured := tls.Client(conn, tlsConfig)
		if e = secured.HandshakeContext(ctx); e != nil {
			return errors.New("smtp_tls_rejected")
		}
		conn = secured
	} else if c.SMTP.TLSMode != "starttls_required" {
		return errors.New("smtp_tls_required")
	}
	client, e := smtp.NewClient(conn, c.SMTP.Host)
	if e != nil {
		return errors.New("smtp_unavailable")
	}
	defer client.Close()
	if c.SMTP.TLSMode == "starttls_required" {
		if yes, _ := client.Extension("STARTTLS"); !yes {
			return errors.New("smtp_tls_required")
		}
		if e = client.StartTLS(tlsConfig); e != nil {
			return errors.New("smtp_tls_rejected")
		}
	}
	if c.SMTP.Username != "" {
		p, e := config.Secret(c.SMTP.PasswordFile)
		if e != nil {
			return errors.New("smtp_auth_rejected")
		}
		if e = client.Auth(smtp.PlainAuth("", c.SMTP.Username, string(p), c.SMTP.Host)); e != nil {
			return errors.New("smtp_auth_rejected")
		}
	}
	if e = client.Mail(c.SMTP.FromAddress); e != nil {
		return e
	}
	if e = client.Rcpt(recipient); e != nil {
		return e
	}
	w, e := client.Data()
	if e != nil {
		return e
	}
	if _, e = w.Write(message); e != nil {
		return e
	}
	return w.Close()
}
func Classify(e error) (string, bool) {
	for _, known := range []string{"smtp_tls_rejected", "smtp_tls_required", "smtp_auth_rejected", "smtp_unavailable"} {
		if e.Error() == known {
			return known, known != "smtp_unavailable"
		}
	}
	var se *textproto.Error
	if errors.As(e, &se) && se.Code >= 500 {
		return "smtp_recipient_rejected", true
	}
	return "smtp_unavailable", false
}
