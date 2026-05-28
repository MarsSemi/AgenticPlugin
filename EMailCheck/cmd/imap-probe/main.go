package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type probeClient struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
	tag  int
	caps map[string]bool
}

func main() {
	host := flag.String("host", envOr("EMAILCHECK_IMAP_HOST", "ms.mailcloud.com.tw"), "IMAP host")
	port := flag.Int("port", envIntOr("EMAILCHECK_IMAP_PORT", 993), "IMAP port")
	username := flag.String("username", os.Getenv("EMAILCHECK_IMAP_USERNAME"), "IMAP username")
	password := flag.String("password", os.Getenv("EMAILCHECK_IMAP_PASSWORD"), "IMAP password")
	folder := flag.String("folder", envOr("EMAILCHECK_IMAP_FOLDER", "INBOX"), "IMAP folder to select after login")
	insecure := flag.Bool("insecure", envBool("EMAILCHECK_IMAP_INSECURE"), "skip TLS certificate verification")
	flag.Parse()

	if strings.TrimSpace(*username) == "" || *password == "" {
		fatalf("EMAILCHECK_IMAP_USERNAME and EMAILCHECK_IMAP_PASSWORD are required")
	}

	client, err := dial(*host, *port, *insecure)
	if err != nil {
		fatalf("%v", err)
	}
	defer client.close()

	fmt.Printf("connected: %s:%d\n", *host, *port)
	fmt.Printf("capability: %s\n", strings.Join(sortedCaps(client.caps), " "))

	if err := client.login(*username, *password); err != nil {
		fatalf("login failed for %s: %v", redactUsername(*username), err)
	}
	fmt.Printf("login ok: %s\n", redactUsername(*username))

	if strings.TrimSpace(*folder) != "" {
		if _, err := client.command("SELECT %s", quote(*folder)); err != nil {
			fatalf("select %s failed: %v", *folder, err)
		}
		fmt.Printf("select ok: %s\n", *folder)
	}
	_, _ = client.command("LOGOUT")
}

func dial(host string, port int, insecure bool) (*probeClient, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig(host, insecure))
	if err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	client := &probeClient{
		conn: conn,
		br:   bufio.NewReader(conn),
		bw:   bufio.NewWriter(conn),
		caps: map[string]bool{},
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	greeting, err := client.readLine()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("greeting failed: %w", err)
	}
	if !strings.Contains(strings.ToUpper(greeting), "OK") && !strings.HasPrefix(greeting, "* PREAUTH") {
		_ = conn.Close()
		return nil, fmt.Errorf("greeting rejected: %s", greeting)
	}
	if err := client.refreshCapabilities(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

func tlsConfig(host string, insecure bool) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		CipherSuites:       mailTLS12CipherSuites(),
		InsecureSkipVerify: insecure,
	}
}

func mailTLS12CipherSuites() []uint16 {
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

func (c *probeClient) refreshCapabilities() error {
	parts, err := c.command("CAPABILITY")
	if err != nil {
		return fmt.Errorf("capability failed: %w", err)
	}
	for _, line := range parts {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "* CAPABILITY") {
			continue
		}
		for _, field := range strings.Fields(line[len("* CAPABILITY"):]) {
			c.caps[strings.ToUpper(field)] = true
		}
	}
	return nil
}

func (c *probeClient) login(username string, password string) error {
	switch {
	case c.caps["AUTH=PLAIN"]:
		return c.authenticatePlain(username, password)
	case c.caps["AUTH=LOGIN"]:
		return c.authenticateLogin(username, password)
	default:
		_, err := c.command("LOGIN %s %s", quote(username), quote(password))
		return err
	}
}

func (c *probeClient) authenticatePlain(username string, password string) error {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if err := c.writeLine("%s AUTHENTICATE PLAIN", tag); err != nil {
		return err
	}
	if _, err := c.expectContinuation(); err != nil {
		return err
	}
	token := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if err := c.writeLine("%s", token); err != nil {
		return err
	}
	return c.expectTagged(tag, "AUTHENTICATE PLAIN")
}

func (c *probeClient) authenticateLogin(username string, password string) error {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if err := c.writeLine("%s AUTHENTICATE LOGIN", tag); err != nil {
		return err
	}
	if _, err := c.expectContinuation(); err != nil {
		return err
	}
	if err := c.writeLine("%s", base64.StdEncoding.EncodeToString([]byte(username))); err != nil {
		return err
	}
	if _, err := c.expectContinuation(); err != nil {
		return err
	}
	if err := c.writeLine("%s", base64.StdEncoding.EncodeToString([]byte(password))); err != nil {
		return err
	}
	return c.expectTagged(tag, "AUTHENTICATE LOGIN")
}

func (c *probeClient) command(format string, args ...any) ([]string, error) {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if err := c.writeLine("%s %s", tag, fmt.Sprintf(format, args...)); err != nil {
		return nil, err
	}
	lines := []string{}
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" ") {
			upper := strings.ToUpper(line)
			if strings.Contains(upper, " OK") || strings.HasPrefix(upper, tag+" OK") {
				return lines, nil
			}
			return lines, fmt.Errorf("%s", strings.TrimSpace(line))
		}
	}
}

func (c *probeClient) expectContinuation() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "+") {
		return line, fmt.Errorf("%s", strings.TrimSpace(line))
	}
	return line, nil
}

func (c *probeClient) expectTagged(tag string, action string) error {
	lastLine := action
	for {
		line, err := c.readLine()
		if err != nil {
			return fmt.Errorf("%s failed after %s: %w", action, lastLine, err)
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

func (c *probeClient) writeLine(format string, args ...any) error {
	_ = c.conn.SetDeadline(time.Now().Add(75 * time.Second))
	if _, err := fmt.Fprintf(c.bw, format+"\r\n", args...); err != nil {
		return err
	}
	return c.bw.Flush()
}

func (c *probeClient) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil && line != "" {
		return strings.TrimRight(line, "\r\n"), nil
	}
	if err != nil {
		if err == io.EOF {
			return "", fmt.Errorf("server closed connection")
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *probeClient) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func quote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func sortedCaps(caps map[string]bool) []string {
	out := make([]string, 0, len(caps))
	for cap := range caps {
		out = append(out, cap)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func redactUsername(username string) string {
	username = strings.TrimSpace(username)
	if len(username) <= 6 {
		return username
	}
	at := strings.Index(username, "@")
	if at > 1 {
		return username[:2] + "..." + username[at-1:]
	}
	return username[:2] + "..." + username[len(username)-2:]
}

func envOr(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envIntOr(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "imap-probe: "+format+"\n", args...)
	os.Exit(1)
}
