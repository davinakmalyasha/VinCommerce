package mail

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
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

	// Mailpit dev server accepts any TLS mode; try STARTTLS, fall back to plain.
	err := smtp.SendMail(addr, auth, c.from, []string{to}, []byte(msg))
	if err != nil {
		if strings.Contains(err.Error(), "tls") || strings.Contains(err.Error(), "handshake") {
			return smtp.SendMail(addr, nil, c.from, []string{to}, []byte(msg))
		}
		return err
	}
	return nil
}

func buildMessage(from, to, subject, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nDate: %s\r\n\r\n%s",
		from, to, subject, time.Now().Format(time.RFC1123Z), body)
}

// IsConfigured reports whether the client has a reachable config.
func (c *Client) IsConfigured() bool { return c.host != "" }
