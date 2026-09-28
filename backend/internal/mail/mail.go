package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Client sends transactional email via SMTP (Mailpit in dev).
type Client struct {
	host      string
	port      int
	from      string
	username  string
	password  string
	useTLS    bool
	templates *template.Template
}

// Config for the SMTP client.
type Config struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
	UseTLS   bool
}

// NewClient creates an SMTP email client.
func NewClient(cfg Config) *Client {
	tpl := template.Must(template.New("").Funcs(template.FuncMap{"dict": dict}).ParseFS(templatesFS, "templates/*.html"))
	return &Client{
		host:      cfg.Host,
		port:      cfg.Port,
		from:      cfg.From,
		username:  cfg.Username,
		password:  cfg.Password,
		useTLS:    cfg.UseTLS,
		templates: tpl,
	}
}

func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", values[i])
		}
		m[key] = values[i+1]
	}
	return m, nil
}

// Send renders a named template and delivers the message.
func (c *Client) Send(ctx context.Context, to, subject, templateName string, data map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var body bytes.Buffer
	if err := c.templates.ExecuteTemplate(&body, templateName, data); err != nil {
		return fmt.Errorf("render %s: %w", templateName, err)
	}

	msg := buildMessage(c.from, to, subject, body.String())

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	var auth smtp.Auth
	if c.username != "" {
		auth = smtp.PlainAuth("", c.username, c.password, c.host)
	}

	// net/smtp.SendMail dials with NO timeout and sets no socket deadline. A
	// single blackholed SMTP host (a firewall DROP, a provider that stopped
	// accepting port 25) pins the goroutine forever; repeated across tasks it
	// consumes every worker slot and stalls the whole queue. Dial and speak
	// through a context so the caller's deadline always wins.
	if err := c.deliver(ctx, addr, auth, to, []byte(msg)); err != nil {
		// Mailpit dev server accepts any TLS mode; try STARTTLS, fall back to plain.
		if strings.Contains(err.Error(), "tls") || strings.Contains(err.Error(), "handshake") {
			return c.deliver(ctx, addr, nil, to, []byte(msg))
		}
		return err
	}
	return nil
}

// dialTimeout bounds establishing the TCP connection and the SMTP handshake.
const dialTimeout = 10 * time.Second

// deliver performs one SMTP delivery attempt under a bounded deadline.
// net/smtp.SendMail dials with NO timeout and sets no socket deadline, so it
// is unusable in a worker: one blackholed host pins a goroutine forever and
// repeated across tasks it consumes every worker slot and stalls the queue.
func (c *Client) deliver(ctx context.Context, addr string, auth smtp.Auth, to string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Absolute deadline for the whole conversation, never exceeding the
	// caller's own deadline when it has one.
	deadline := time.Now().Add(dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp set deadline: %w", err)
	}

	// If the caller cancels mid-send, collapse the deadline so the blocked
	// read/write returns immediately instead of waiting it out.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()

	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer cl.Close()

	if ok, _ := cl.Extension("STARTTLS"); ok {
		if err := cl.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := cl.Extension("AUTH"); ok {
			if err := cl.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := cl.Mail(c.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := cl.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return cl.Quit()
}

func buildMessage(from, to, subject, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nDate: %s\r\n\r\n%s",
		from, to, subject, time.Now().Format(time.RFC1123Z), body)
}

// IsConfigured reports whether the client has a reachable config.
func (c *Client) IsConfigured() bool { return c.host != "" }
