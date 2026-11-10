// Package mailer sends transactional mail (sign-in codes, reset links) through an SMTP server such as Gmail.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"api/internal/core/port"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	// "Name <address>"
	From string
}

// SMTP delivers with STARTTLS (587) or implicit TLS (465). Without a host, development logs every mail instead
// and production refuses to send
type SMTP struct {
	cfg        Config
	from       *mail.Address
	production bool
	logger     zerolog.Logger
}

func New(cfg Config, production bool, logger zerolog.Logger) (*SMTP, error) {
	s := &SMTP{cfg: cfg, production: production, logger: logger}
	if cfg.Host == "" {
		if production {
			logger.Error().Msg("APP__GOOGLE__SMTP__HOST is empty: sign-in codes and reset links cannot be sent")
		} else {
			logger.Warn().Msg("APP__GOOGLE__SMTP__HOST is empty: mail is written to the log in development")
		}
		return s, nil
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("APP__GOOGLE__SMTP__FROM is not a valid address: %w", err)
	}
	s.from = from
	return s, nil
}

func (s *SMTP) Send(ctx context.Context, m port.Mail) error {
	if s.cfg.Host == "" {
		if s.production {
			return errors.New("smtp not configured")
		}
		s.logger.Info().Str("to", m.To).Str("subject", m.Subject).Str("body", m.Text).Msg("mail (not sent: no SMTP in development)")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Mail(s.from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(m.To); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.message(m)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *SMTP) connect(ctx context.Context) (*smtp.Client, error) {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsConfig := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	if s.cfg.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if s.cfg.Port != 465 {
		if err := client.StartTLS(tlsConfig); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if err := client.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// message builds a multipart/alternative mail (plain text + HTML), both quoted-printable
func (s *SMTP) message(m port.Mail) []byte {
	boundary := randomBoundary()
	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", s.from.String())
	header("To", m.To)
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", randomBoundary(), domainOf(s.from.Address)))
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ kind, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		if part.body == "" {
			continue
		}
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.kind)
		qp := quotedprintable.NewWriter(&b)
		_, _ = qp.Write([]byte(part.body))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func randomBoundary() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func domainOf(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 {
		return address[at+1:]
	}
	return "localhost"
}
