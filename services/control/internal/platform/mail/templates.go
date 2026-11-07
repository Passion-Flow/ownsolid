package mail

import (
	"bytes"
	"errors"
	"fmt"
	html "html/template"
	"mime"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"strings"
	text "text/template"
	"time"
)

type Template struct {
	Subject      string                        `json:"subject"`
	Text         string                        `json:"text"`
	HTML         string                        `json:"html"`
	Presentation localization.MailPresentation `json:"-"`
}

func LoadTemplates() (map[string]Template, error) {
	registry, e := localization.Default()
	if e != nil {
		return nil, e
	}
	return LoadTemplatesWithRegistry(registry)
}
func LoadTemplatesWithRegistry(registry *localization.Registry) (map[string]Template, error) {
	if registry == nil {
		return nil, errors.New("LOCALIZATION_UNAVAILABLE")
	}
	out := map[string]Template{}
	for key, value := range registry.MailTemplates() {
		locale, _, ok := strings.Cut(key, "/")
		if !ok {
			return nil, errors.New("LOCALIZATION_PACKAGE_INVALID")
		}
		presentation, err := registry.MailPresentation(locale)
		if err != nil {
			return nil, err
		}
		t := Template{Subject: value.Subject, Text: value.Text, HTML: value.HTML, Presentation: presentation}
		if _, e := text.New("text").Option("missingkey=error").Parse(t.Text); e != nil {
			return nil, e
		}
		if _, e := html.New("html").Option("missingkey=error").Parse(t.HTML); e != nil {
			return nil, e
		}
		out[key] = t
	}
	return out, nil
}
func (o *Outbox) Render(id, purpose, locale, recipient string, p Payload, messageIDs ...string) ([]byte, error) {
	messageID := o.messageID(id)
	if len(messageIDs) > 0 {
		messageID = messageIDs[0]
	}
	if messageID == "" || strings.ContainsAny(messageID, "\r\n <>\t") {
		return nil, errors.New("MAIL_MESSAGE_ID_INVALID")
	}
	t, ok := o.Templates[locale+"/"+purpose]
	if !ok {
		t, ok = o.Templates["en/"+purpose]
		if !ok {
			return nil, errors.New("MAIL_TEMPLATE_UNAVAILABLE")
		}
	}
	var plain, rich bytes.Buffer
	a, e := text.New("text").Option("missingkey=error").Parse(t.Text)
	if e != nil {
		return nil, e
	}
	if e = a.Execute(&plain, p); e != nil {
		return nil, e
	}
	b, e := html.New("html").Option("missingkey=error").Parse(t.HTML)
	if e != nil {
		return nil, e
	}
	if e = b.Execute(&rich, p); e != nil {
		return nil, e
	}
	presentation := t.Presentation
	if !localization.CanonicalLocale(presentation.Locale) || (presentation.Direction != "ltr" && presentation.Direction != "rtl") {
		return nil, errors.New("MAIL_LOCALIZATION_INVALID")
	}
	var document bytes.Buffer
	wrapper, err := html.New("document").Parse(`<!doctype html><html lang="{{.Locale}}" dir="{{.Direction}}"><body>{{.Content}}{{if .HasAttribution}}<footer><p>{{.Label}}</p><a href="{{.Link}}"><img src="cid:ownsolid-translation-attribution" alt="Google Translate"></a><p><a href="{{.Documentation}}">{{.Documentation}}</a></p></footer>{{end}}</body></html>`)
	if err != nil {
		return nil, err
	}
	if err = wrapper.Execute(&document, struct {
		Locale, Direction          string
		Content                    html.HTML
		HasAttribution             bool
		Label, Link, Documentation string
	}{presentation.Locale, presentation.Direction, html.HTML(rich.String()), len(presentation.TranslationBadge) > 0, presentation.TranslationLabel, presentation.TranslationLink, presentation.TranslationDocumentation}); err != nil {
		return nil, err
	}
	if len(presentation.TranslationBadge) > 0 {
		fmt.Fprintf(&plain, "\n\n%s\n%s\n%s\n", presentation.TranslationLabel, presentation.TranslationLink, presentation.TranslationDocumentation)
	}
	boundary := "ownsolid-" + id
	alternative := fmt.Sprintf("--%s\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--%s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--%s--\r\n", boundary, encodeBody(plain.Bytes()), boundary, encodeBody(document.Bytes()), boundary)
	contentType := fmt.Sprintf("multipart/alternative; boundary=%q", boundary)
	body := alternative
	if len(presentation.TranslationBadge) > 0 {
		related := boundary + "-related"
		contentType = fmt.Sprintf("multipart/related; boundary=%q", related)
		body = fmt.Sprintf("--%s\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n%s\r\n--%s\r\nContent-Type: image/png\r\nContent-Transfer-Encoding: base64\r\nContent-ID: <ownsolid-translation-attribution>\r\nContent-Disposition: inline; filename=translation-attribution.png\r\n\r\n%s\r\n--%s--\r\n", related, boundary, alternative, related, encodeBody(presentation.TranslationBadge), related)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Language: %s\r\nContent-Type: %s\r\n\r\n%s", o.Config.SMTP.FromAddress, recipient, mime.QEncoding.Encode("UTF-8", t.Subject), messageID, time.Now().UTC().Format(time.RFC1123Z), presentation.Locale, contentType, body)
	return []byte(message), nil
}
func encodeBody(b []byte) string {
	v := base64Body(b)
	var out strings.Builder
	for len(v) > 76 {
		out.WriteString(v[:76] + "\r\n")
		v = v[76:]
	}
	out.WriteString(v)
	return out.String()
}

// messageID derives its domain from the validated configured sender. The UUID stays stable across retries.
func (o *Outbox) messageID(id string) string {
	_, domain, _ := strings.Cut(o.Config.SMTP.FromAddress, "@")
	return id + "@" + domain
}
