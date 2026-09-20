package sieve

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// TLS modes understood by Dial. They mirror config.TLSMode without creating
// an import cycle.
const (
	TLSImplicit = "tls"
	TLSStart    = "starttls"
	TLSNone     = "none"
)

// Client is a minimal ManageSieve client (RFC 5804) sufficient to install
// and read back scripts. Purelymail runs Apache James ManageSieve on
// mailserver.purelymail.com:4190 with STARTTLS.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
	caps map[string]string
}

// ScriptInfo is an entry of LISTSCRIPTS.
type ScriptInfo struct {
	Name   string
	Active bool
}

// Dial connects and, for STARTTLS mode, upgrades the connection.
func Dial(ctx context.Context, addr, mode string, tlsConfig *tls.Config) (*Client, error) {
	host, _, _ := net.SplitHostPort(addr)
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	} else if tlsConfig.ServerName == "" {
		tlsConfig = tlsConfig.Clone()
		tlsConfig.ServerName = host
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if mode == TLSImplicit {
		td := &tls.Dialer{NetDialer: dialer, Config: tlsConfig}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, r: bufio.NewReader(conn)}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(60 * time.Second))
	}
	if err := c.readGreeting(); err != nil {
		conn.Close()
		return nil, err
	}
	if mode == TLSStart {
		if _, err := c.cmd("STARTTLS"); err != nil {
			conn.Close()
			return nil, fmt.Errorf("managesieve: STARTTLS: %w", err)
		}
		tconn := tls.Client(conn, tlsConfig)
		if err := tconn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		c.conn = tconn
		c.r = bufio.NewReader(tconn)
		if err := c.readGreeting(); err != nil {
			tconn.Close()
			return nil, err
		}
	}
	return c, nil
}

// Capabilities returns the server capability map (e.g. "SIEVE" -> extensions).
func (c *Client) Capabilities() map[string]string { return c.caps }

func (c *Client) readGreeting() error {
	caps := map[string]string{}
	for {
		line, err := c.readLine()
		if err != nil {
			return err
		}
		if strings.HasPrefix(line, "OK") {
			c.caps = caps
			return nil
		}
		if strings.HasPrefix(line, "NO") || strings.HasPrefix(line, "BYE") {
			return errors.New("managesieve: " + line)
		}
		parts := parseQuoted(line)
		if len(parts) >= 1 {
			v := ""
			if len(parts) > 1 {
				v = parts[1]
			}
			caps[strings.ToUpper(parts[0])] = v
		}
	}
}

func (c *Client) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// response reads lines until a tagged OK/NO/BYE line. Literals ({n}) that
// appear at the end of a line are read in full and inlined.
func (c *Client) response() ([]string, error) {
	var lines []string
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, err
		}
		if strings.HasSuffix(line, "}") {
			if i := strings.LastIndex(line, "{"); i >= 0 {
				n, perr := strconv.Atoi(strings.TrimSuffix(line[i+1:len(line)-1], "+"))
				if perr == nil {
					buf := make([]byte, n)
					if _, err := io.ReadFull(c.r, buf); err != nil {
						return lines, err
					}
					// consume the CRLF that follows the literal
					c.readLine()
					lines = append(lines, line[:i]+string(buf))
					continue
				}
			}
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "OK"):
			return lines, nil
		case strings.HasPrefix(up, "NO"), strings.HasPrefix(up, "BYE"):
			return lines, errors.New("managesieve: " + line)
		}
		lines = append(lines, line)
	}
}

func (c *Client) cmd(line string) ([]string, error) {
	if _, err := io.WriteString(c.conn, line+"\r\n"); err != nil {
		return nil, err
	}
	return c.response()
}

func (c *Client) cmdLiteral(prefix, literal string) ([]string, error) {
	if _, err := fmt.Fprintf(c.conn, "%s {%d+}\r\n%s\r\n", prefix, len(literal), literal); err != nil {
		return nil, err
	}
	return c.response()
}

func quoteArg(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Authenticate performs SASL PLAIN.
func (c *Client) Authenticate(user, password string) error {
	raw := base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + password))
	_, err := c.cmd(`AUTHENTICATE "PLAIN" ` + quoteArg(raw))
	if err != nil {
		return fmt.Errorf("managesieve: authentication failed: %w", err)
	}
	return nil
}

// PutScript uploads (and validates) a script under name.
func (c *Client) PutScript(name, script string) error {
	_, err := c.cmdLiteral("PUTSCRIPT "+quoteArg(name), script)
	return err
}

// CheckScript validates a script without storing it.
func (c *Client) CheckScript(script string) error {
	_, err := c.cmdLiteral("CHECKSCRIPT", script)
	return err
}

// SetActive activates name (empty string deactivates all scripts).
func (c *Client) SetActive(name string) error {
	_, err := c.cmd("SETACTIVE " + quoteArg(name))
	return err
}

// GetScript returns the script body.
func (c *Client) GetScript(name string) (string, error) {
	lines, err := c.cmd("GETSCRIPT " + quoteArg(name))
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// ListScripts lists stored scripts.
func (c *Client) ListScripts() ([]ScriptInfo, error) {
	lines, err := c.cmd("LISTSCRIPTS")
	if err != nil {
		return nil, err
	}
	var out []ScriptInfo
	for _, l := range lines {
		parts := parseQuoted(l)
		if len(parts) == 0 {
			continue
		}
		out = append(out, ScriptInfo{Name: parts[0], Active: strings.Contains(strings.ToUpper(l), "ACTIVE")})
	}
	return out, nil
}

// DeleteScript removes a script.
func (c *Client) DeleteScript(name string) error {
	_, err := c.cmd("DELETESCRIPT " + quoteArg(name))
	return err
}

// Logout ends the session politely and closes the connection.
func (c *Client) Logout() error {
	c.cmd("LOGOUT")
	return c.conn.Close()
}

// Close closes the connection without LOGOUT.
func (c *Client) Close() error { return c.conn.Close() }

// parseQuoted extracts quoted strings and bare atoms from a response line.
func parseQuoted(line string) []string {
	var out []string
	i := 0
	for i < len(line) {
		switch {
		case line[i] == ' ':
			i++
		case line[i] == '"':
			var b strings.Builder
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				b.WriteByte(line[i])
				i++
			}
			i++
			out = append(out, b.String())
		default:
			j := i
			for j < len(line) && line[j] != ' ' {
				j++
			}
			out = append(out, line[i:j])
			i = j
		}
	}
	return out
}
