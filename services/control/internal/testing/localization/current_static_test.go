//go:build localization_database_integration

package localization_test

import (
	"bytes"
	"encoding/base64"
	"github.com/google/uuid"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	internetmail "net/mail"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"strconv"
	"strings"
	"testing"
	"time"
)

func staticRegistry(t *testing.T) *localization.Registry {
	t.Helper()
	directory := os.Getenv("PROJECT_ONE_STATIC_LOCALE_PACKAGE_DIRECTORY")
	if directory == "" {
		t.Fatal("explicit current static package required")
	}
	r, e := localization.New(config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: []string{"en", "fr", "ar"}}, config.DefaultLocalizationPolicy())
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Catalog()) != 4 {
		t.Fatal("three packaged locales plus distinct Chinese seed required")
	}
	return r
}
func localeMIMEParts(t *testing.T, header string, raw []byte) map[string][]byte {
	t.Helper()
	media, parameters, e := mime.ParseMediaType(header)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(media, "multipart/") {
		return map[string][]byte{media: raw}
	}
	out := map[string][]byte{}
	reader := multipart.NewReader(bytes.NewReader(raw), parameters["boundary"])
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		var source io.Reader = part
		switch part.Header.Get("Content-Transfer-Encoding") {
		case "base64":
			source = base64.NewDecoder(base64.StdEncoding, part)
		case "quoted-printable":
			source = quotedprintable.NewReader(part)
		}
		data, e := io.ReadAll(source)
		if e != nil {
			t.Fatal(e)
		}
		for key, value := range localeMIMEParts(t, part.Header.Get("Content-Type"), data) {
			out[key] = value
		}
	}
	return out
}

func TestM16ActualInstalledStaticLocaleIdentityAndLoadedSMTPFallback(t *testing.T) {
	s := fixture.NewM16(t)
	password := fixture.Bootstrap(t, s)
	s.Locales = staticRegistry(t)
	templates, e := mail.LoadTemplatesWithRegistry(s.Locales)
	if e != nil {
		t.Fatal(e)
	}
	s.Mail.Templates = templates
	actor := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	version := actor.Me.User.Version
	for _, locale := range []string{"fr", "ar"} {
		changed, e := s.UpdateMe(t.Context(), actor, "Current installed language", locale, version, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		version++
		current := fixture.Login(t, s, "admin@example.invalid", password, "portal")
		if current.Me.User.Locale != locale || current.Me.User.ID != actor.Me.User.ID || current.Me.Workspace.ID != actor.Me.Workspace.ID || current.Epoch != actor.Epoch || changed.User.Version != version {
			t.Fatal("locale changed identity or lost durable revision")
		}
	}
	sink := smtpsink.Start(t, true, false)
	host, port, _ := net.SplitHostPort(sink.Listener.Addr().String())
	s.Mail.Config.SMTP.Host = host
	s.Mail.Config.SMTP.Port, _ = strconv.Atoi(port)
	s.Mail.Config.SMTP.CAFile = sink.CAFile
	s.Mail.Config.SMTP.TLSMode = "implicit_tls"
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 5
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 10
	for index, locale := range []string{"fr", "ar", "fr"} {
		invitation, e := s.Invite(t.Context(), actor, "", "createInvitation", uuid.NewString(), uuid.NewString()+"@example.invalid", locale, 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		token := fixture.InvitationToken(t, s, invitation.ID)
		want := locale
		if locale == "fr" && s.Mail.Templates["fr/invitation"].Subject == "" {
			t.Fatal("missing actual template")
		}
		if locale == "fr" && len(sink.Messages) > 0 {
			t.Fatal("unexpected retained SMTP message")
		}
		// Third invitation was accepted under the installed registry, then the maker
		// selected the seed template inventory, as on a compatible package rollback.
		if index == 2 {
			s.Mail.Templates, e = mail.LoadTemplates()
			if e != nil {
				t.Fatal(e)
			}
			want = "en"
		}
		dispatched, e := s.Mail.DispatchOne(t.Context())
		if e != nil || !dispatched {
			t.Fatal("actual TLS outbox", e)
		}
		select {
		case raw := <-sink.Messages:
			message, e := internetmail.ReadMessage(bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			body, e := io.ReadAll(message.Body)
			if e != nil {
				t.Fatal(e)
			}
			subject, e := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
			if e != nil {
				t.Fatal(e)
			}
			if message.Header.Get("Content-Language") != want || subject != s.Mail.Templates[want+"/invitation"].Subject {
				t.Fatal("loaded localized mail subject/language")
			}
			parts := localeMIMEParts(t, message.Header.Get("Content-Type"), body)
			direction := "ltr"
			if want == "ar" {
				direction = "rtl"
			}
			if !bytes.Contains(parts["text/html"], []byte(`lang="`+want+`" dir="`+direction+`"`)) {
				t.Fatal("actual localized HTML document metadata")
			}
			link := s.Config.Server.PortalOrigin + "/register#token=" + token
			if len(parts["text/plain"]) == 0 || len(parts["text/html"]) == 0 || !bytes.Contains(parts["text/plain"], []byte(link)) || !bytes.Contains(parts["text/html"], []byte(link)) || bytes.Contains(body, []byte("translate.googleapis.com")) {
				t.Fatal("actual mail alternatives/safe local link")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("local SMTP receipt missing")
		}
		var state, saved string
		var payload []byte
		if e = s.DB.QueryRow(t.Context(), "SELECT state,locale,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", invitation.ID).Scan(&state, &saved, &payload); e != nil || state != "sent" || saved != locale || payload != nil {
			t.Fatal("fallback rewrote locale or retained delivered secrets", e)
		}
		if _, e = s.ValidateInvitation(t.Context(), token); e != nil {
			t.Fatal("localized mail changed token identity", e)
		}
	}
}
