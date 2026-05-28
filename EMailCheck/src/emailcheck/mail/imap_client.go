package mail

import (
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type imapClient struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
	tag  int
	host string
	caps map[string]bool
}

type imapResponsePart struct {
	Line    string
	Literal []byte
}

func testIMAPLogin(ctx context.Context, account model.EmailAccount) (model.EmailAccount, []model.LoginAttempt, error) {
	client, err := dialIMAP(ctx, account)
	if err != nil {
		return account, nil, err
	}
	defer client.Close()
	username := strings.TrimSpace(account.Username)
	if err := client.Login(username, account.Password); err != nil {
		attempts := []model.LoginAttempt{{Username: RedactLoginUsername(username), Error: err.Error()}}
		return account, attempts, fmt.Errorf("imap login failed for configured username: %w", err)
	}
	_ = client.Logout()
	attempts := []model.LoginAttempt{{Username: RedactLoginUsername(username), Success: true}}
	return account, attempts, nil
}

func listIMAPMessages(ctx context.Context, account model.EmailAccount, req model.EmailListRequest) ([]model.EmailMessage, error) {
	client, err := dialIMAP(ctx, account)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := client.Login(account.Username, account.Password); err != nil {
		return nil, err
	}
	if err := client.Select(req.Folder); err != nil {
		return nil, err
	}

	uids := req.UIDs
	if len(uids) == 0 {
		uids, err = client.Search(req)
		if err != nil {
			return nil, err
		}
		uids = lastUIDs(uids, req.Limit)
	}
	messages := make([]model.EmailMessage, 0, len(uids))
	for _, uid := range uids {
		fetched, err := client.FetchRaw(uid, !req.MarkSeen)
		if err != nil {
			return nil, err
		}
		message, err := parseEmailMessage(fetched, req.IncludeBody)
		if err != nil {
			return nil, fmt.Errorf("parse uid %s: %w", uid, err)
		}
		messages = append(messages, message)
		if req.MarkSeen {
			_ = client.MarkSeen(uid)
		}
	}
	_ = client.Logout()
	return messages, nil
}

func markIMAPMessagesSeen(ctx context.Context, account model.EmailAccount, req model.EmailListRequest) error {
	if len(req.UIDs) == 0 {
		return errors.New("uids are required")
	}
	client, err := dialIMAP(ctx, account)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(account.Username, account.Password); err != nil {
		return err
	}
	if err := client.Select(req.Folder); err != nil {
		return err
	}
	for _, uid := range req.UIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := client.MarkSeen(uid); err != nil {
			return err
		}
	}
	_ = client.Logout()
	return nil
}

func dialIMAP(ctx context.Context, account model.EmailAccount) (*imapClient, error) {
	server := account.IMAP
	if strings.TrimSpace(server.Host) == "" {
		return nil, errors.New("imap.host is required")
	}
	if server.Port == 0 {
		server.Port = 993
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
		return nil, fmt.Errorf("imap connect failed: %w", err)
	}
	client := &imapClient{conn: conn, br: bufio.NewReader(conn), bw: bufio.NewWriter(conn), host: server.Host}
	if err := conn.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		_ = err
	}
	greeting, err := client.readLine()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("imap greeting failed: %w", err)
	}
	if !strings.Contains(strings.ToUpper(greeting), "OK") && !strings.HasPrefix(greeting, "* PREAUTH") {
		_ = client.Close()
		return nil, fmt.Errorf("imap greeting rejected: %s", greeting)
	}
	if server.StartTLS && !server.TLS {
		if _, err := client.command("STARTTLS"); err != nil {
			_ = client.Close()
			return nil, err
		}
		tlsConn := tls.Client(conn, tlsConfig(server, server.Host))
		if err := tlsConn.Handshake(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("imap starttls failed: %w", err)
		}
		client.conn = tlsConn
		client.br = bufio.NewReader(tlsConn)
		client.bw = bufio.NewWriter(tlsConn)
	}
	_ = client.refreshCapabilities()
	return client, nil
}

func tlsConfig(server model.EmailServer, host string) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		CipherSuites:       mailTLS12CipherSuites(),
		InsecureSkipVerify: server.InsecureSkipVerify,
	}
}

func mailTLS12CipherSuites() []uint16 {
	// 部分企業信箱仍只提供 TLS 1.2 的 RSA key exchange cipher。
	// Go 1.22+ 預設停用這類 cipher，這裡只補回 TLS 1.2 GCM 相容項目，不降低最低 TLS 版本。
	return []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	}
}

func (c *imapClient) Login(username string, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("imap username is required")
	}
	if c.supportsCapability("AUTH=PLAIN") {
		if err := c.authenticatePlain(username, password); err != nil {
			return fmt.Errorf("imap authenticate plain failed: %w", err)
		}
		return nil
	}
	if c.supportsCapability("AUTH=LOGIN") {
		if err := c.authenticateLogin(username, password); err != nil {
			return fmt.Errorf("imap authenticate login failed: %w", err)
		}
		return nil
	}
	if _, err := c.command("LOGIN %s %s", imapQuote(username), imapQuote(password)); err != nil {
		return fmt.Errorf("imap login failed: %w", err)
	}
	return nil
}

func RedactLoginUsername(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	if len(username) <= 6 {
		return username
	}
	at := strings.Index(username, "@")
	if at > 1 {
		domain := username[at+1:]
		if len(domain) > 8 {
			domain = domain[:4] + "..." + domain[len(domain)-3:]
		}
		return username[:2] + "..." + username[at-1:at] + "@" + domain
	}
	return username[:2] + "..." + username[len(username)-2:]
}

func (c *imapClient) refreshCapabilities() error {
	parts, err := c.command("CAPABILITY")
	if err != nil {
		return err
	}
	caps := map[string]bool{}
	for _, part := range parts {
		line := strings.TrimSpace(part.Line)
		if !strings.HasPrefix(strings.ToUpper(line), "* CAPABILITY") {
			continue
		}
		for _, field := range strings.Fields(line[len("* CAPABILITY"):]) {
			caps[strings.ToUpper(field)] = true
		}
	}
	c.caps = caps
	return nil
}

func (c *imapClient) supportsCapability(name string) bool {
	if len(c.caps) == 0 {
		return false
	}
	return c.caps[strings.ToUpper(strings.TrimSpace(name))]
}

func (c *imapClient) authenticatePlain(username string, password string) error {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if err := c.conn.SetDeadline(time.Now().Add(75 * time.Second)); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintf(c.bw, "%s AUTHENTICATE PLAIN\r\n", tag); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	lastLine := line
	if !strings.HasPrefix(strings.TrimSpace(line), "+") {
		return fmt.Errorf("%s", strings.TrimSpace(line))
	}
	token := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if _, err := fmt.Fprintf(c.bw, "%s\r\n", token); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	for {
		line, err := c.readLine()
		if err != nil {
			if strings.TrimSpace(lastLine) != "" {
				return fmt.Errorf("%w after %s", err, strings.TrimSpace(lastLine))
			}
			return err
		}
		lastLine = line
		if !strings.HasPrefix(line, tag+" ") {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.Contains(upper, " OK") || strings.HasPrefix(upper, tag+" OK") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(line))
	}
}

func (c *imapClient) authenticateLogin(username string, password string) error {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if err := c.conn.SetDeadline(time.Now().Add(75 * time.Second)); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintf(c.bw, "%s AUTHENTICATE LOGIN\r\n", tag); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	lastLine, err := c.expectIMAPContinuation()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.bw, "%s\r\n", base64.StdEncoding.EncodeToString([]byte(username))); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	lastLine, err = c.expectIMAPContinuation()
	if err != nil {
		if strings.TrimSpace(lastLine) != "" {
			return fmt.Errorf("%w after %s", err, imapContinuationSummary(lastLine))
		}
		return err
	}
	if _, err := fmt.Fprintf(c.bw, "%s\r\n", base64.StdEncoding.EncodeToString([]byte(password))); err != nil {
		return err
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	return c.expectIMAPTaggedCompletion(tag, lastLine)
}

func (c *imapClient) expectIMAPContinuation() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "+") {
		return line, fmt.Errorf("%s", strings.TrimSpace(line))
	}
	return line, nil
}

func (c *imapClient) expectIMAPTaggedCompletion(tag string, lastLine string) error {
	for {
		line, err := c.readLine()
		if err != nil {
			if strings.TrimSpace(lastLine) != "" {
				return fmt.Errorf("server closed connection after %s; check username, password, app password, or IMAP permission", imapContinuationSummary(lastLine))
			}
			return err
		}
		lastLine = line
		if !strings.HasPrefix(line, tag+" ") {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.Contains(upper, " OK") || strings.HasPrefix(upper, tag+" OK") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(line))
	}
}

func imapContinuationSummary(line string) string {
	prompt := strings.TrimSpace(line)
	if !strings.HasPrefix(prompt, "+") {
		return prompt
	}
	prompt = strings.TrimSpace(strings.TrimPrefix(prompt, "+"))
	if prompt == "" {
		return "continuation prompt"
	}
	decoded, err := base64.StdEncoding.DecodeString(prompt)
	if err != nil {
		return "+ " + prompt
	}
	label := strings.TrimSpace(strings.TrimRight(string(decoded), "\x00"))
	if label == "" {
		return "+ " + prompt
	}
	return label + " prompt"
}

func (c *imapClient) Select(folder string) error {
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if _, err := c.command("SELECT %s", imapQuote(folder)); err != nil {
		return fmt.Errorf("imap select failed: %w", err)
	}
	return nil
}

func (c *imapClient) Search(req model.EmailListRequest) ([]string, error) {
	criteria := []string{}
	if req.UnreadOnly {
		criteria = append(criteria, "UNSEEN")
	}
	if req.Since != "" {
		if t, err := time.Parse(time.RFC3339, req.Since); err == nil {
			criteria = append(criteria, "SINCE", t.Format("02-Jan-2006"))
		}
	} else if req.SinceDays > 0 {
		criteria = append(criteria, "SINCE", time.Now().AddDate(0, 0, -req.SinceDays).Format("02-Jan-2006"))
	}
	if len(criteria) == 0 {
		criteria = append(criteria, "ALL")
	}
	parts, err := c.command("UID SEARCH %s", strings.Join(criteria, " "))
	if err != nil {
		return nil, fmt.Errorf("imap search failed: %w", err)
	}
	var uids []string
	for _, part := range parts {
		line := strings.TrimSpace(part.Line)
		if !strings.HasPrefix(strings.ToUpper(line), "* SEARCH") {
			continue
		}
		for _, field := range strings.Fields(line[len("* SEARCH"):]) {
			if _, err := strconv.ParseUint(field, 10, 64); err == nil {
				uids = append(uids, field)
			}
		}
	}
	sort.SliceStable(uids, func(i, j int) bool {
		left, _ := strconv.ParseUint(uids[i], 10, 64)
		right, _ := strconv.ParseUint(uids[j], 10, 64)
		return left < right
	})
	return uids, nil
}

func (c *imapClient) FetchRaw(uid string, peek bool) (model.FetchedMessage, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return model.FetchedMessage{}, errors.New("uid is required")
	}
	bodyKey := "BODY[]"
	if peek {
		bodyKey = "BODY.PEEK[]"
	}
	parts, err := c.command("UID FETCH %s (UID FLAGS INTERNALDATE RFC822.SIZE %s)", uid, bodyKey)
	if err != nil {
		return model.FetchedMessage{}, fmt.Errorf("imap fetch failed: %w", err)
	}
	fetched := model.FetchedMessage{UID: uid}
	for _, part := range parts {
		if len(part.Literal) > 0 {
			fetched.Raw = append([]byte(nil), part.Literal...)
		}
		if strings.Contains(strings.ToUpper(part.Line), "FETCH") {
			if parsedUID := firstRegexGroup(part.Line, `UID\s+(\d+)`); parsedUID != "" {
				fetched.UID = parsedUID
			}
			if size := firstRegexGroup(part.Line, `RFC822\.SIZE\s+(\d+)`); size != "" {
				fetched.Size, _ = strconv.Atoi(size)
			}
			fetched.Flags = parseIMAPFlags(part.Line)
		}
	}
	if len(fetched.Raw) == 0 {
		return fetched, fmt.Errorf("uid %s has empty body", uid)
	}
	if fetched.Size == 0 {
		fetched.Size = len(fetched.Raw)
	}
	return fetched, nil
}

func (c *imapClient) MarkSeen(uid string) error {
	_, err := c.command("UID STORE %s +FLAGS.SILENT (\\Seen)", strings.TrimSpace(uid))
	return err
}

func (c *imapClient) Logout() error {
	_, err := c.command("LOGOUT")
	return err
}

func (c *imapClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *imapClient) command(format string, args ...any) ([]imapResponsePart, error) {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	command := fmt.Sprintf(format, args...)
	if err := c.conn.SetDeadline(time.Now().Add(75 * time.Second)); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintf(c.bw, "%s %s\r\n", tag, command); err != nil {
		return nil, err
	}
	if err := c.bw.Flush(); err != nil {
		return nil, err
	}

	var parts []imapResponsePart
	for {
		line, err := c.readLine()
		if err != nil {
			if len(parts) > 0 {
				return parts, fmt.Errorf("%w after %s", err, strings.TrimSpace(parts[len(parts)-1].Line))
			}
			return parts, err
		}
		part := imapResponsePart{Line: line}
		if n, ok := literalSize(line); ok {
			literal := make([]byte, n)
			if _, err := io.ReadFull(c.br, literal); err != nil {
				return parts, err
			}
			part.Literal = literal
			if tail, err := c.readLine(); err == nil && strings.TrimSpace(tail) != "" {
				part.Line = part.Line + " " + tail
			}
		}
		parts = append(parts, part)
		if strings.HasPrefix(line, tag+" ") {
			upper := strings.ToUpper(line)
			if strings.Contains(upper, " OK") || strings.HasPrefix(upper, tag+" OK") {
				return parts, nil
			}
			return parts, fmt.Errorf("%s", strings.TrimSpace(line))
		}
	}
}

func (c *imapClient) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil && line != "" {
		return strings.TrimRight(line, "\r\n"), nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func imapQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func literalSize(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	start := strings.LastIndex(line, "{")
	if start < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(line[start+1:], "}"))
	return n, err == nil && n >= 0
}

func parseIMAPFlags(line string) []string {
	raw := firstRegexGroup(line, `FLAGS\s+\(([^)]*)\)`)
	if raw == "" {
		return nil
	}
	fields := strings.Fields(raw)
	flags := make([]string, 0, len(fields))
	for _, field := range fields {
		flags = append(flags, field)
	}
	return flags
}

func firstRegexGroup(input string, pattern string) string {
	re := regexp.MustCompile(`(?i)` + pattern)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func lastUIDs(uids []string, limit int) []string {
	if limit <= 0 {
		return uids
	}
	if limit > 100 {
		limit = 100
	}
	if len(uids) <= limit {
		return uids
	}
	return uids[len(uids)-limit:]
}
