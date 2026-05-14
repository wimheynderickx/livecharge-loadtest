package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"
)

// Message is the rendered email ready to hand to a Sender.
//
// MIME structure is derived from which body fields are populated:
//
//   - Both TextBody and HTMLBody  → multipart/alternative
//   - TextBody only               → single text/plain part
//   - HTMLBody only               → single text/html part
//
// When Attachments is non-empty the structure above is wrapped in
// multipart/mixed so attachments appear as siblings of the body.
type Message struct {
	From    string
	To      []string
	CC      []string
	BCC     []string
	Subject string

	// TextBody is the rendered text/plain body. Empty means no text part.
	TextBody string

	// HTMLBody is the rendered text/html body. Empty means no HTML part.
	HTMLBody string

	// Attachments are added as multipart/mixed parts when present.
	Attachments []Attachment
}

// Attachment is one file to attach. Name is the filename shown to the
// recipient; Content is the raw bytes; MIMEType defaults to
// "text/plain; charset=utf-8" when empty (everything we attach right now
// is text).
type Attachment struct {
	Name     string
	Content  []byte
	MIMEType string
}

// Sender encapsulates the SMTP transport details so tests can swap in a
// custom dialer pointing at the in-process mock server.
type Sender struct {
	cfg Config
	// Dialer is the function used to open the initial TCP connection.
	// Production code uses net.Dialer{}; tests inject one that resolves
	// 127.0.0.1:0 to whatever port their mock landed on.
	Dialer func(network, addr string) (net.Conn, error)
	// InsecureSkipVerify controls TLS cert verification during the
	// STARTTLS upgrade. Defaults to false; tests with self-signed
	// certificates set it true. Never set in production.
	InsecureSkipVerify bool
	// ServerName overrides the SNI value sent during STARTTLS. Used by
	// tests where the dialer points at 127.0.0.1 but the certificate is
	// issued for "localhost".
	ServerName string
}

// NewSender returns a sender configured from cfg with sane defaults.
func NewSender(cfg Config) *Sender {
	cfg.ApplyDefaults()
	return &Sender{cfg: cfg}
}

// SendAsync renders the message in the foreground (so we can fail fast
// on a bad template) and then dispatches the SMTP dialogue in a
// goroutine. status is updated in place; callers should keep a reference
// for the TUI to poll.
//
// The returned channel closes when the goroutine has finished — whether
// success or failure — so the program-exit path can wait on it bounded by
// the configured timeout.
func (s *Sender) SendAsync(msg Message, status *Status) <-chan struct{} {
	done := make(chan struct{})
	if status == nil {
		status = &Status{}
	}

	// Record the primary recipient for the TUI summary. We pick the first
	// To/CC/BCC in that order — the user will recognise their own address.
	recipient := firstNonEmpty(msg.To, msg.CC, msg.BCC)
	status.MarkPending(recipient)

	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout())
		defer cancel()
		if err := s.send(ctx, msg); err != nil {
			status.MarkFailed(err)
			return
		}
		status.MarkSent()
	}()
	return done
}

// send is the blocking SMTP dialogue. Public only via SendAsync so the
// status tracking is uniform.
func (s *Sender) send(ctx context.Context, msg Message) error {
	addr := net.JoinHostPort(s.cfg.SMTPHost, strconv.Itoa(s.cfg.SMTPPort))
	dialer := s.Dialer
	if dialer == nil {
		nd := &net.Dialer{Timeout: 10 * time.Second}
		dialer = nd.Dial
	}

	// We honour ctx.Deadline at the TCP layer by passing it through the
	// dialer when one is set on the context, and at every later step by
	// short-circuiting if ctx.Err() returns non-nil. SMTP itself doesn't
	// expose deadlines on the textproto reads, so this is best-effort but
	// covers the slow-network case the user cared about.
	conn, err := dialer("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, s.cfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	// Identify ourselves. Some servers care about the EHLO name.
	host, _ := os.Hostname()
	if host == "" {
		host = "loadtest.local"
	}
	if err := client.Hello(host); err != nil {
		return fmt.Errorf("EHLO: %w", err)
	}

	// Attempt STARTTLS. Required whenever we have credentials and the
	// target isn't loopback — sending the password in cleartext over the
	// network would be unforgivable. Loopback gets a pass for tests and
	// developer convenience.
	wantAuth := s.cfg.SMTPUser != "" && s.cfg.SMTPPass != ""
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         s.serverName(),
			InsecureSkipVerify: s.InsecureSkipVerify,
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	} else if wantAuth && !isLoopback(s.cfg.SMTPHost) {
		return errors.New("server does not advertise STARTTLS but credentials were provided; refusing to send password in cleartext")
	}

	if wantAuth {
		auth := smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	fromAddr := extractAddress(msg.From)
	if err := client.Mail(fromAddr); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range allRecipients(msg) {
		if err := client.Rcpt(extractAddress(rcpt)); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.Copy(w, bytes.NewReader(buildRFC822(msg))); err != nil {
		_ = w.Close()
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close DATA: %w", err)
	}

	return client.Quit()
}

// serverName chooses the SNI/cert-name for STARTTLS. ServerName override
// wins; otherwise we use the configured SMTPHost.
func (s *Sender) serverName() string {
	if s.ServerName != "" {
		return s.ServerName
	}
	return s.cfg.SMTPHost
}

// allRecipients concatenates To+CC+BCC. BCC is included here because they
// still need RCPT TO commands; the difference is only that BCC addresses
// don't appear in the headers (handled in buildRFC822).
func allRecipients(m Message) []string {
	out := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	out = append(out, m.To...)
	out = append(out, m.CC...)
	out = append(out, m.BCC...)
	return out
}

// firstNonEmpty picks the first non-empty entry across the given slices.
// Used to compute the recipient label shown in the TUI.
func firstNonEmpty(lists ...[]string) string {
	for _, list := range lists {
		for _, item := range list {
			if strings.TrimSpace(item) != "" {
				return item
			}
		}
	}
	return ""
}

// isLoopback reports whether host refers to the loopback interface. Used
// to decide whether STARTTLS is mandatory before transmitting credentials.
func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// extractAddress turns "Display Name <addr@example.com>" into just
// "addr@example.com" because the SMTP envelope commands want bare addresses.
// Falls back to the input unchanged when no angle brackets are present.
func extractAddress(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.LastIndex(s, ">"); j > i {
			return strings.TrimSpace(s[i+1 : j])
		}
	}
	return strings.TrimSpace(s)
}

// buildRFC822 serialises a Message into an RFC 822 byte stream.
//
// Layering rules:
//
//   - Two bodies (text + html) become a multipart/alternative subtree.
//   - Attachments wrap whatever the body subtree is in a
//     multipart/mixed envelope.
//   - One body and no attachments collapse to a single-part message
//     with the body's Content-Type at the top level.
//
// We build the body subtree first, then optionally wrap it. This keeps
// the four-case logic small and identical between "single body" and
// "alternative bodies".
func buildRFC822(m Message) []byte {
	var buf bytes.Buffer
	hdr := textproto.MIMEHeader{}
	hdr.Set("From", m.From)
	if len(m.To) > 0 {
		hdr.Set("To", strings.Join(m.To, ", "))
	}
	if len(m.CC) > 0 {
		hdr.Set("Cc", strings.Join(m.CC, ", "))
	}
	hdr.Set("Subject", encodeSubject(m.Subject))
	hdr.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	hdr.Set("MIME-Version", "1.0")

	// Build the body subtree. bodyTopType is what the message's
	// Content-Type header reads when there are no attachments;
	// bodyTopBytes is the raw bytes that follow the headers (or that get
	// nested inside multipart/mixed when attachments are present).
	bodyTopType, bodyTopBytes := buildBodyPart(m)

	if len(m.Attachments) == 0 {
		hdr.Set("Content-Type", bodyTopType)
		// 8bit is correct for both text/plain and text/html as long as
		// the body is UTF-8 (which our renderer guarantees). For
		// multipart/alternative the header is omitted because the
		// children carry their own encodings.
		if !strings.HasPrefix(bodyTopType, "multipart/") {
			hdr.Set("Content-Transfer-Encoding", "8bit")
		}
		writeHeader(&buf, hdr)
		buf.WriteString("\r\n")
		buf.Write(bodyTopBytes)
		return buf.Bytes()
	}

	// Attachments present → wrap whatever the body subtree is in
	// multipart/mixed.
	boundary := randomBoundary()
	hdr.Set("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	writeHeader(&buf, hdr)
	buf.WriteString("\r\n")
	buf.WriteString("This is a multi-part message in MIME format.\r\n")

	// First child: the body subtree, kept verbatim with its own headers
	// (Content-Type + any per-part encoding) so a multipart/alternative
	// subtree nests correctly.
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: " + bodyTopType + "\r\n")
	if !strings.HasPrefix(bodyTopType, "multipart/") {
		buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	}
	buf.WriteString("\r\n")
	buf.Write(bodyTopBytes)
	buf.WriteString("\r\n")

	// Remaining children: each attachment as a sibling part.
	for _, a := range m.Attachments {
		mimeType := a.MIMEType
		if mimeType == "" {
			mimeType = "text/plain; charset=utf-8"
		}
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: " + mimeType + "\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString(`Content-Disposition: attachment; filename="` + a.Name + `"` + "\r\n\r\n")
		buf.WriteString(base64Wrap(a.Content))
		buf.WriteString("\r\n")
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return buf.Bytes()
}

// buildBodyPart returns the Content-Type to declare for the message
// body subtree and the bytes that compose it. Callers either emit those
// bytes directly (single-part message) or wrap them in multipart/mixed
// alongside attachments.
//
// Cases:
//
//   - text + html → multipart/alternative wrapping both parts. RFC 2046
//     says clients should prefer the LAST renderable part — we place
//     text first, html last, so HTML clients show the rich version while
//     plain-text fallback readers still get the readable text.
//   - text only   → text/plain
//   - html only   → text/html
//   - neither     → text/plain with an empty body. Shouldn't happen
//     because ResolveBodies always populates TextBody, but it keeps the
//     function total.
func buildBodyPart(m Message) (contentType string, body []byte) {
	hasText := m.TextBody != ""
	hasHTML := m.HTMLBody != ""

	switch {
	case hasText && hasHTML:
		boundary := randomBoundary()
		var buf bytes.Buffer
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		buf.WriteString(m.TextBody)
		buf.WriteString("\r\n")
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		buf.WriteString(m.HTMLBody)
		buf.WriteString("\r\n")
		buf.WriteString("--" + boundary + "--\r\n")
		return `multipart/alternative; boundary="` + boundary + `"`, buf.Bytes()

	case hasHTML:
		return "text/html; charset=utf-8", []byte(m.HTMLBody)

	default: // text only or both empty
		return "text/plain; charset=utf-8", []byte(m.TextBody)
	}
}

// encodeSubject applies RFC 2047 Q-encoding when the subject contains any
// non-ASCII byte. For pure-ASCII subjects we emit the raw text.
func encodeSubject(s string) string {
	for _, r := range s {
		if r > 0x7E || r < 0x20 {
			return "=?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

// writeHeader writes the textproto headers to w using CRLF separators as
// required by RFC 822.
func writeHeader(w io.Writer, h textproto.MIMEHeader) {
	for k, vs := range h {
		for _, v := range vs {
			_, _ = io.WriteString(w, k+": "+v+"\r\n")
		}
	}
}

// randomBoundary returns a fresh MIME boundary token. Length and charset
// match what the standard library uses internally; we don't reuse it
// because mime/multipart insists on owning the entire writer.
func randomBoundary() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "loadtest-boundary-" + base64.RawURLEncoding.EncodeToString(buf[:])
}

// base64Wrap encodes b and inserts a CRLF every 76 characters so the
// resulting body conforms to the RFC 2045 line-length limit. A naive
// "single huge line" encoding works on most servers but some refuse it.
func base64Wrap(b []byte) string {
	const lineLen = 76
	enc := base64.StdEncoding.EncodeToString(b)
	if len(enc) <= lineLen {
		return enc
	}
	var out strings.Builder
	for i := 0; i < len(enc); i += lineLen {
		end := i + lineLen
		if end > len(enc) {
			end = len(enc)
		}
		out.WriteString(enc[i:end])
		out.WriteString("\r\n")
	}
	return out.String()
}
