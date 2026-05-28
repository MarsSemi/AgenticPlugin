package mail

import (
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type pop3Client struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
}

type pop3MessageRef struct {
	Number int
	UID    string
	Size   int
}

func testPOP3Login(ctx context.Context, account model.EmailAccount) error {
	client, err := dialPOP3(ctx, account)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(account.Username, account.Password); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}

func listPOP3Messages(ctx context.Context, account model.EmailAccount, req model.EmailListRequest) ([]model.EmailMessage, error) {
	client, err := dialPOP3(ctx, account)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := client.Login(account.Username, account.Password); err != nil {
		return nil, err
	}
	refs, err := client.MessageRefs()
	if err != nil {
		return nil, err
	}
	refs = selectPOP3Refs(refs, req)
	cutoff := requestCutoff(req)
	messages := make([]model.EmailMessage, 0, minInt(len(refs), firstPositive(req.Limit, 20)))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		headerRaw, headerErr := client.Top(ref.Number, 0)
		useHeaderOnly := headerErr == nil && len(headerRaw) > 0 && !req.IncludeBody && len(req.UIDs) == 0
		raw := headerRaw
		if !useHeaderOnly {
			raw, err = client.Retr(ref.Number)
			if err != nil {
				return nil, err
			}
		}
		fetched := model.FetchedMessage{
			UID:  firstNonEmpty(ref.UID, strconv.Itoa(ref.Number)),
			Raw:  raw,
			Size: firstPositive(ref.Size, len(raw)),
		}
		message, err := parseEmailMessage(fetched, req.IncludeBody && !useHeaderOnly)
		if err != nil {
			return nil, fmt.Errorf("parse pop3 message %s: %w", fetched.UID, err)
		}
		if !messageAfterCutoff(message, cutoff) {
			if cutoff.IsZero() || len(req.UIDs) > 0 {
				continue
			}
			break
		}
		messages = append(messages, message)
		if req.Limit > 0 && len(messages) >= req.Limit {
			break
		}
		if req.Limit <= 0 && cutoff.IsZero() && len(messages) >= 100 {
			break
		}
	}
	_ = client.Quit()
	return messages, nil
}

func dialPOP3(ctx context.Context, account model.EmailAccount) (*pop3Client, error) {
	server := account.POP3
	if strings.TrimSpace(server.Host) == "" {
		return nil, errors.New("pop3.host is required")
	}
	if server.Port == 0 {
		server.Port = 995
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
		return nil, fmt.Errorf("pop3 connect failed: %w", err)
	}
	client := &pop3Client{conn: conn, br: bufio.NewReader(conn), bw: bufio.NewWriter(conn)}
	if err := conn.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		_ = err
	}
	greeting, err := client.readLine()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("pop3 greeting failed: %w", err)
	}
	if !strings.HasPrefix(strings.ToUpper(greeting), "+OK") {
		_ = client.Close()
		return nil, fmt.Errorf("pop3 greeting rejected: %s", greeting)
	}
	if server.StartTLS && !server.TLS {
		if _, err := client.command("STLS"); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("pop3 stls failed: %w", err)
		}
		tlsConn := tls.Client(conn, tlsConfig(server, server.Host))
		if err := tlsConn.Handshake(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("pop3 stls handshake failed: %w", err)
		}
		client.conn = tlsConn
		client.br = bufio.NewReader(tlsConn)
		client.bw = bufio.NewWriter(tlsConn)
	}
	return client, nil
}

func (c *pop3Client) Login(username string, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("pop3 username is required")
	}
	if _, err := c.command("USER %s", username); err != nil {
		return fmt.Errorf("pop3 user failed: %w", err)
	}
	if _, err := c.command("PASS %s", password); err != nil {
		return fmt.Errorf("pop3 pass failed: %w", err)
	}
	return nil
}

func (c *pop3Client) MessageRefs() ([]pop3MessageRef, error) {
	lines, err := c.commandMulti("LIST")
	if err != nil {
		return nil, fmt.Errorf("pop3 list failed: %w", err)
	}
	refs := make([]pop3MessageRef, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		num, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		size, _ := strconv.Atoi(fields[1])
		refs = append(refs, pop3MessageRef{Number: num, Size: size})
	}
	uidLines, err := c.commandMulti("UIDL")
	if err == nil {
		uidByNumber := map[int]string{}
		for _, line := range uidLines {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			num, err := strconv.Atoi(fields[0])
			if err == nil {
				uidByNumber[num] = fields[1]
			}
		}
		for index := range refs {
			refs[index].UID = uidByNumber[refs[index].Number]
		}
	}
	return refs, nil
}

func (c *pop3Client) Top(number int, lines int) ([]byte, error) {
	responseLines, err := c.commandMulti("TOP %d %d", number, lines)
	if err != nil {
		return nil, fmt.Errorf("pop3 top %d failed: %w", number, err)
	}
	return []byte(strings.Join(responseLines, "\r\n") + "\r\n"), nil
}

func (c *pop3Client) Retr(number int) ([]byte, error) {
	lines, err := c.commandMulti("RETR %d", number)
	if err != nil {
		return nil, fmt.Errorf("pop3 retr %d failed: %w", number, err)
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n"), nil
}

func (c *pop3Client) Quit() error {
	_, err := c.command("QUIT")
	return err
}

func (c *pop3Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *pop3Client) command(format string, args ...any) (string, error) {
	if err := c.writeLine(format, args...); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(strings.ToUpper(line), "+OK") {
		return line, fmt.Errorf("%s", strings.TrimSpace(line))
	}
	return line, nil
}

func (c *pop3Client) commandMulti(format string, args ...any) ([]string, error) {
	if _, err := c.command(format, args...); err != nil {
		return nil, err
	}
	var lines []string
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, err
		}
		if line == "." {
			return lines, nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		lines = append(lines, line)
	}
}

func (c *pop3Client) writeLine(format string, args ...any) error {
	if err := c.conn.SetDeadline(time.Now().Add(75 * time.Second)); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintf(c.bw, format+"\r\n", args...); err != nil {
		return err
	}
	return c.bw.Flush()
}

func (c *pop3Client) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil && line != "" {
		return strings.TrimRight(line, "\r\n"), nil
	}
	if err != nil {
		if err == io.EOF {
			return "", errors.New("pop3 server closed connection")
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func selectPOP3Refs(refs []pop3MessageRef, req model.EmailListRequest) []pop3MessageRef {
	if len(req.UIDs) > 0 {
		wanted := map[string]bool{}
		for _, uid := range req.UIDs {
			wanted[strings.TrimSpace(uid)] = true
		}
		var selected []pop3MessageRef
		for _, ref := range refs {
			if wanted[ref.UID] || wanted[strconv.Itoa(ref.Number)] {
				selected = append(selected, ref)
			}
		}
		return selected
	}
	refs = reversePOP3Refs(refs)
	if req.Limit <= 0 || req.Limit >= len(refs) {
		return refs
	}
	return refs[:req.Limit]
}

func reversePOP3Refs(refs []pop3MessageRef) []pop3MessageRef {
	for left, right := 0, len(refs)-1; left < right; left, right = left+1, right-1 {
		refs[left], refs[right] = refs[right], refs[left]
	}
	return refs
}

func requestCutoff(req model.EmailListRequest) time.Time {
	if strings.TrimSpace(req.Since) != "" {
		parsed, err := time.Parse(time.RFC3339, req.Since)
		if err == nil {
			return parsed
		}
	}
	if req.SinceDays > 0 {
		return time.Now().AddDate(0, 0, -req.SinceDays)
	}
	return time.Time{}
}

func messageAfterCutoff(message model.EmailMessage, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return true
	}
	parsed, ok := parseMessageDate(message.Date)
	if !ok {
		return true
	}
	return !parsed.Before(cutoff)
}

func messageWithinRequestDays(message model.EmailMessage, req model.EmailListRequest) bool {
	return messageAfterCutoff(message, requestCutoff(req))
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
