package sieve

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	managesieve "github.com/hstern/go-managesieve"
)

const (
	TLSImplicit = "tls"
	TLSStart    = "starttls"
	TLSNone     = "none"
)

type Client struct {
	client     *managesieve.Client
	stopCancel func() bool
}

type ScriptInfo = managesieve.ScriptInfo

func Dial(ctx context.Context, addr, mode string, tlsConfig *tls.Config) (*Client, error) {
	return DialWithDialer(ctx, addr, mode, tlsConfig, &net.Dialer{Timeout: 10 * time.Second})
}

func DialWithDialer(ctx context.Context, addr, mode string, tlsConfig *tls.Config, dialer *net.Dialer) (*Client, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if mode != TLSImplicit && mode != TLSStart && mode != TLSNone {
		return nil, fmt.Errorf("ManageSieve TLSMode 无效")
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("ManageSieve 必须验证 TLS 证书")
	}
	tlsConfig.ServerName = host
	if tlsConfig.MinVersion < tls.VersionTLS12 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	rawConn := conn
	stopCancel := context.AfterFunc(ctx, func() { rawConn.Close() })
	deadline := time.Now().Add(60 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetDeadline(deadline); err != nil {
		stopCancel()
		conn.Close()
		return nil, err
	}
	if mode == TLSImplicit {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			stopCancel()
			conn.Close()
			return nil, err
		}
		conn = secure
	}
	client, err := managesieve.NewClient(conn)
	if err != nil {
		stopCancel()
		conn.Close()
		return nil, err
	}
	if mode == TLSStart {
		if !client.Capabilities().StartTLS {
			stopCancel()
			client.Close()
			return nil, fmt.Errorf("ManageSieve 未提供 STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			stopCancel()
			client.Close()
			return nil, err
		}
	}
	return &Client{client: client, stopCancel: stopCancel}, nil
}

func (c *Client) Authenticate(username, password string) error {
	supported := false
	for _, mechanism := range c.client.Capabilities().SASL {
		if strings.EqualFold(mechanism, "PLAIN") {
			supported = true
		}
	}
	if !supported {
		return &AuthError{Code: "unsupported_auth_mechanism"}
	}
	return classifyAuthenticationError(c.client.Authenticate(managesieve.PlainAuth("", username, password)))
}

func (c *Client) Capabilities() map[string]string {
	caps := c.client.Capabilities()
	result := map[string]string{"IMPLEMENTATION": caps.Implementation, "SASL": strings.Join(caps.SASL, " "), "SIEVE": strings.Join(caps.Sieve, " "), "VERSION": caps.Version}
	for key, value := range caps.Extra {
		result[key] = value
	}
	return result
}

func (c *Client) PutScript(name, script string) error {
	_, err := c.client.PutScript(name, script)
	return err
}
func (c *Client) CheckScript(script string) error       { _, err := c.client.CheckScript(script); return err }
func (c *Client) SetActive(name string) error           { return c.client.SetActive(name) }
func (c *Client) GetScript(name string) (string, error) { return c.client.GetScript(name) }
func (c *Client) ListScripts() ([]ScriptInfo, error)    { return c.client.ListScripts() }
func (c *Client) DeleteScript(name string) error        { return c.client.DeleteScript(name) }
func (c *Client) Logout() error                         { defer c.stopCancel(); return c.client.Logout() }
func (c *Client) Close() error                          { c.stopCancel(); return c.client.Close() }
