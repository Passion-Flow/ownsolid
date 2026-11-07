package mail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	internetmail "net/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"strings"
	"testing"
	"unicode"
)

func readLocalizationParts(t *testing.T, header string, raw []byte) map[string][]byte {
	t.Helper()
	media, parameters, e := mime.ParseMediaType(header)
	if e != nil {
		t.Fatal(e)
	}
	if strings.HasPrefix(media, "multipart/") {
		result := map[string][]byte{}
		reader := multipart.NewReader(bytes.NewReader(raw), parameters["boundary"])
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			data, e := io.ReadAll(part)
			if e != nil {
				t.Fatal(e)
			}
			if part.Header.Get("Content-Transfer-Encoding") == "base64" {
				data, e = base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(string(data), "\r", ""), "\n", ""))
				if e != nil {
					t.Fatal(e)
				}
			}
			for key, value := range readLocalizationParts(t, part.Header.Get("Content-Type"), data) {
				result[key] = value
			}
		}
		return result
	}
	return map[string][]byte{media: raw}
}
func TestLocalizationMailActualSeedLanguageAndUnavailableFallback(t *testing.T) {
	templates, e := LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	outbox := &Outbox{Templates: templates}
	outbox.Config.SMTP.FromAddress = "noreply@example.invalid"
	for _, locale := range []string{"en", "zh-CN", "uninstalled"} {
		t.Run(locale, func(t *testing.T) {
			raw, e := outbox.Render("synthetic-id", "password_reset", locale, "recipient@example.invalid", Payload{Link: "https://portal.example.invalid/reset#token=synthetic&scope=review"})
			if e != nil {
				t.Fatal(e)
			}
			message, e := internetmail.ReadMessage(bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			expected := locale
			if locale == "uninstalled" {
				expected = "en"
			}
			if message.Header.Get("Content-Language") != expected {
				t.Fatal("fallback header does not match actual body")
			}
			body, e := io.ReadAll(message.Body)
			if e != nil {
				t.Fatal(e)
			}
			parts := readLocalizationParts(t, message.Header.Get("Content-Type"), body)
			if len(parts) != 2 || !bytes.Contains(parts["text/html"], []byte(`lang="`+expected+`" dir="ltr"`)) || !bytes.Contains(parts["text/plain"], []byte("token=synthetic&scope=review")) || !bytes.Contains(parts["text/html"], []byte("token=synthetic&amp;scope=review")) {
				t.Fatal("actual seed mail/link/markup incorrect")
			}
			if strings.Contains(string(body), "Google Translate") || parts["image/png"] != nil {
				t.Fatal("seed falsely attributed to provider")
			}
		})
	}
}
func TestLocalizationMailExplicitAttributionMIMEFixtureEmbedsOfflineOriginalBytes(t *testing.T) {
	// Deliberate renderer fixture only: not a persisted locale, provider result or review.
	badge := []byte("EXPLICIT SYNTHETIC PNG TRANSPORT FIXTURE")
	template := Template{Subject: "Synthetic renderer", Text: "Open {{.Link}}", HTML: `<p>Open <a href="{{.Link}}">{{.Link}}</a></p>`, Presentation: localization.MailPresentation{Locale: "ar", Direction: "rtl", TranslationLabel: "EXPLICIT TEST ONLY", TranslationLink: "https://translate.google.com/", TranslationDocumentation: "https://docs.cloud.google.com/translate/attribution", TranslationBadge: badge}}
	outbox := &Outbox{Templates: map[string]Template{"ar/password_reset": template}}
	outbox.Config.SMTP.FromAddress = "noreply@example.invalid"
	raw, e := outbox.Render("synthetic-related", "password_reset", "ar", "recipient@example.invalid", Payload{Link: "https://portal.example.invalid/review"})
	if e != nil {
		t.Fatal(e)
	}
	message, e := internetmail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(message.Body)
	if e != nil {
		t.Fatal(e)
	}
	parts := readLocalizationParts(t, message.Header.Get("Content-Type"), body)
	if len(parts) != 3 || !bytes.Equal(parts["image/png"], badge) || !bytes.Contains(parts["text/html"], []byte(`lang="ar" dir="rtl"`)) || !bytes.Contains(parts["text/html"], []byte(`src="cid:ownsolid-translation-attribution"`)) || !bytes.Contains(parts["text/plain"], []byte("https://translate.google.com/")) {
		t.Fatal("offline nested MIME attribution/language invalid")
	}
	if !strings.Contains(string(body), "Content-ID: <ownsolid-translation-attribution>") || message.Header.Get("Content-Language") != "ar" {
		t.Fatal("CID or actual body language absent")
	}
}
func TestLocalizationMailMissingTemplateAndInjectedMetadataRefuse(t *testing.T) {
	outbox := &Outbox{Templates: map[string]Template{}}
	outbox.Config.SMTP.FromAddress = "noreply@example.invalid"
	if _, e := outbox.Render("id", "unknown", "en", "recipient@example.invalid", Payload{}); e == nil {
		t.Fatal("missing template rendered success")
	}
	outbox.Templates["en/password_reset"] = Template{Text: "Body", HTML: "<p>Body</p>", Presentation: localization.MailPresentation{Locale: "en\r\nInjected: true", Direction: "ltr"}}
	if _, e := outbox.Render("id", "password_reset", "en", "recipient@example.invalid", Payload{}); e == nil {
		t.Fatal("unsafe language header accepted")
	}
}

// Every email reads in both languages from subject to body, and says which one it is in.
func TestEveryMailPurposeReadsInEachLanguage(t *testing.T) {
	templates, e := LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	outbox := &Outbox{Templates: templates}
	outbox.Config.SMTP.FromAddress = "noreply@example.invalid"
	for _, purpose := range []string{"invitation", "password_reset", "password_changed"} {
		for _, locale := range []string{"en", "zh-CN"} {
			t.Run(purpose+"/"+locale, func(t *testing.T) {
				raw, e := outbox.Render("synthetic-id", purpose, locale, "recipient@example.invalid", Payload{Link: "https://portal.example.invalid/t"})
				if e != nil {
					t.Fatal(e)
				}
				message, e := internetmail.ReadMessage(bytes.NewReader(raw))
				if e != nil {
					t.Fatal(e)
				}
				if got := message.Header.Get("Content-Language"); got != locale {
					t.Fatalf("Content-Language %q", got)
				}
				subject, e := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(message.Body)
				if e != nil {
					t.Fatal(e)
				}
				parts := readLocalizationParts(t, message.Header.Get("Content-Type"), body)
				for name, text := range map[string]string{"subject": subject, "text": string(parts["text/plain"]), "html": string(parts["text/html"])} {
					if hasHan(text) != (locale == "zh-CN") {
						t.Fatalf("%s is in the wrong language: %q", name, text)
					}
				}
			})
		}
	}
}

func hasHan(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}
