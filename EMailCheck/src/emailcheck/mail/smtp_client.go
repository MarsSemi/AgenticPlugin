package mail

import (
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPSendResult struct {
	AccountID string   `json:"account_id"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	CC        []string `json:"cc,omitempty"`
	BCC       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject"`
	MessageID string   `json:"message_id"`
	SentAt    string   `json:"sent_at"`
}

func TestSMTPLogin(ctx context.Context, account model.EmailAccount) error {
	client, err := dialSMTP(ctx, account)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := authenticateSMTP(client, account); err != nil {
		return err
	}
	if err := client.Noop(); err != nil {
		return fmt.Errorf("smtp noop failed: %w", err)
	}
	_ = client.Quit()
	return nil
}

func SendSMTPReply(ctx context.Context, account model.EmailAccount, req model.EmailReplyRequest, original *model.EmailMessage) (SMTPSendResult, error) {
	if strings.TrimSpace(account.SMTP.Host) == "" {
		return SMTPSendResult{}, fmt.Errorf("smtp.host is required")
	}
	to := append([]string{}, req.To...)
	if len(to) == 0 && original != nil {
		to = []string{firstNonEmpty(original.ReplyTo, original.From)}
	}
	to = normalizeAddressList(to)
	cc := normalizeAddressList(req.CC)
	bcc := normalizeAddressList(req.BCC)
	if len(to)+len(cc)+len(bcc) == 0 {
		return SMTPSendResult{}, fmt.Errorf("at least one recipient is required")
	}
	subject := strings.TrimSpace(req.Subject)
	if subject == "" && original != nil {
		subject = replySubject(original.Subject)
	}
	if subject == "" {
		subject = "Re:"
	}

	fromAddress := mail.Address{Name: account.FromName, Address: firstNonEmpty(account.Email, account.Username)}
	messageID := generateMessageID(firstNonEmpty(account.Email, account.SMTP.Host, "email-check.local"))
	raw := buildMIMEMessage(fromAddress.String(), to, cc, bcc, subject, req.Text, req.HTML, messageID, original)

	client, err := dialSMTP(ctx, account)
	if err != nil {
		return SMTPSendResult{}, err
	}
	defer client.Close()
	if err := authenticateSMTP(client, account); err != nil {
		return SMTPSendResult{}, err
	}
	if err := client.Mail(fromAddress.Address); err != nil {
		return SMTPSendResult{}, fmt.Errorf("smtp mail from failed: %w", err)
	}
	for _, rcpt := range append(append([]string{}, to...), append(cc, bcc...)...) {
		addr, err := mail.ParseAddress(rcpt)
		if err != nil {
			return SMTPSendResult{}, fmt.Errorf("invalid recipient %q: %w", rcpt, err)
		}
		if err := client.Rcpt(addr.Address); err != nil {
			return SMTPSendResult{}, fmt.Errorf("smtp rcpt failed for %s: %w", addr.Address, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return SMTPSendResult{}, fmt.Errorf("smtp data failed: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return SMTPSendResult{}, err
	}
	if err := w.Close(); err != nil {
		return SMTPSendResult{}, fmt.Errorf("smtp data close failed: %w", err)
	}
	_ = client.Quit()
	return SMTPSendResult{
		AccountID: account.ID,
		From:      fromAddress.String(),
		To:        to,
		CC:        cc,
		BCC:       bcc,
		Subject:   subject,
		MessageID: messageID,
		SentAt:    time.Now().Format(time.RFC3339),
	}, nil
}

func dialSMTP(ctx context.Context, account model.EmailAccount) (*smtp.Client, error) {
	server := account.SMTP
	if server.Port == 0 {
		server.Port = 465
	}
	addr := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if server.TLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig(server, server.Host))
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("smtp connect failed: %w", err)
	}
	client, err := smtp.NewClient(conn, server.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smtp client failed: %w", err)
	}
	if err := client.Hello("localhost"); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("smtp hello failed: %w", err)
	}
	if server.StartTLS && !server.TLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, fmt.Errorf("smtp server does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsConfig(server, server.Host)); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("smtp starttls failed: %w", err)
		}
	}
	return client, nil
}

func authenticateSMTP(client *smtp.Client, account model.EmailAccount) error {
	if strings.TrimSpace(account.Username) == "" && strings.TrimSpace(account.Password) == "" {
		return nil
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		return fmt.Errorf("smtp server does not advertise AUTH")
	}
	auth := smtp.PlainAuth("", account.Username, account.Password, account.SMTP.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth failed: %w", err)
	}
	return nil
}

func buildMIMEMessage(from string, to []string, cc []string, bcc []string, subject string, textBody string, htmlBody string, messageID string, original *model.EmailMessage) []byte {
	var headers [][2]string
	headers = append(headers,
		[2]string{"From", from},
		[2]string{"To", strings.Join(to, ", ")},
		[2]string{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		[2]string{"Date", time.Now().Format(time.RFC1123Z)},
		[2]string{"Message-ID", messageID},
		[2]string{"MIME-Version", "1.0"},
	)
	if len(cc) > 0 {
		headers = append(headers, [2]string{"Cc", strings.Join(cc, ", ")})
	}
	if original != nil {
		if original.MessageID != "" {
			headers = append(headers, [2]string{"In-Reply-To", original.MessageID})
		}
		references := strings.TrimSpace(strings.Join([]string{original.References, original.MessageID}, " "))
		if references != "" {
			headers = append(headers, [2]string{"References", references})
		}
	}

	var buf bytes.Buffer
	if strings.TrimSpace(htmlBody) != "" {
		boundary := "email-check-" + randomHex(12)
		headers = append(headers, [2]string{"Content-Type", `multipart/alternative; boundary="` + boundary + `"`})
		writeHeaders(&buf, headers)
		buf.WriteString("\r\n--" + boundary + "\r\n")
		writeBodyPart(&buf, "text/plain; charset=utf-8", firstNonEmpty(textBody, stripHTML(htmlBody)))
		buf.WriteString("\r\n--" + boundary + "\r\n")
		writeBodyPart(&buf, "text/html; charset=utf-8", htmlBody)
		buf.WriteString("\r\n--" + boundary + "--\r\n")
		return buf.Bytes()
	}
	headers = append(headers, [2]string{"Content-Type", "text/plain; charset=utf-8"})
	headers = append(headers, [2]string{"Content-Transfer-Encoding", "quoted-printable"})
	writeHeaders(&buf, headers)
	buf.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&buf)
	_, _ = qp.Write([]byte(textBody))
	_ = qp.Close()
	buf.WriteString("\r\n")
	return buf.Bytes()
}

func writeBodyPart(buf *bytes.Buffer, contentType string, body string) {
	buf.WriteString("Content-Type: " + contentType + "\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(buf)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	buf.WriteString("\r\n")
}

func writeHeaders(buf *bytes.Buffer, headers [][2]string) {
	for _, header := range headers {
		if strings.TrimSpace(header[1]) == "" {
			continue
		}
		buf.WriteString(header[0])
		buf.WriteString(": ")
		buf.WriteString(strings.ReplaceAll(header[1], "\n", " "))
		buf.WriteString("\r\n")
	}
}

func normalizeAddressList(values []string) []string {
	out := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			text := strings.TrimSpace(part)
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re:"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

func generateMessageID(domainSource string) string {
	domain := "email-check.local"
	if at := strings.LastIndex(domainSource, "@"); at >= 0 && at < len(domainSource)-1 {
		domain = domainSource[at+1:]
	} else if strings.Contains(domainSource, ".") {
		domain = domainSource
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), randomHex(8), domain)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
