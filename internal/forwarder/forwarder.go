package forwarder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"mail-forwarder/internal/config"
	"mail-forwarder/internal/mailbox"
	"mail-forwarder/internal/state"
)

type Service struct {
	cfg        config.Config
	recipients []string
	logger     *zap.Logger
}

type Result struct {
	Planned int
	Sent    int
	Skipped int
	Failed  int
}

func New(cfg config.Config, recipients []string, logger *zap.Logger) *Service {
	return &Service{cfg: cfg, recipients: recipients, logger: logger}
}

func (s *Service) Forward(ctx context.Context, messages []mailbox.Message, store *state.Store) (Result, error) {
	var result Result
	for _, message := range messages {
		for _, recipient := range s.recipients {
			result.Planned++
			key := DedupKey(s.cfg.SourceMailbox.Username, message, recipient)
			if store.IsSent(key) {
				result.Skipped++
				continue
			}
			if s.cfg.Forward.DryRun {
				s.logger.Info(
					"dry-run 邮件转发计划",
					zap.String("subject", message.Subject),
					zap.Uint32("uid", message.UID),
					zap.String("message_id", message.MessageID),
					zap.String("recipient", recipient),
					zap.Int("attachments", len(message.Attachments)),
				)
				continue
			}
			if err := s.send(ctx, message, recipient); err != nil {
				result.Failed++
				store.MarkFailed(key, message.MessageID, message.UID, recipient, err)
				s.logger.Warn("邮件转发失败", zap.String("recipient", recipient), zap.String("subject", message.Subject), zap.Error(err))
				continue
			}
			result.Sent++
			store.MarkSuccess(key, message.MessageID, message.UID, recipient)
			s.logger.Info("邮件转发成功", zap.String("recipient", recipient), zap.String("subject", message.Subject))
		}
	}
	return result, ctx.Err()
}

func DedupKey(source string, message mailbox.Message, recipient string) string {
	messageKey := strings.TrimSpace(message.MessageID)
	if messageKey == "" {
		messageKey = fmt.Sprintf("uid-%d", message.UID)
	}
	raw := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(source)),
		messageKey,
		mailbox.AttachmentHashes(message.Attachments),
		strings.ToLower(strings.TrimSpace(recipient)),
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) send(ctx context.Context, message mailbox.Message, recipient string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	raw, err := s.buildMessage(message, recipient)
	if err != nil {
		return err
	}
	return s.sendSMTP(recipient, raw)
}

func (s *Service) buildMessage(message mailbox.Message, recipient string) ([]byte, error) {
	from := s.cfg.SMTP.From
	if s.cfg.SMTP.FromName != "" {
		from = (&mail.Address{Name: s.cfg.SMTP.FromName, Address: s.cfg.SMTP.From}).String()
	}
	subject := message.Subject
	if s.cfg.SMTP.SubjectPrefix != "" && !strings.HasPrefix(subject, s.cfg.SMTP.SubjectPrefix) {
		subject = s.cfg.SMTP.SubjectPrefix + " " + subject
	}
	boundary := fmt.Sprintf("mail-forwarder-%d", time.Now().UnixNano())
	var b bytes.Buffer
	writeHeader(&b, "From", from)
	writeHeader(&b, "To", recipient)
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", boundary))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	writeHeader(&b, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	body := "以下邮件由邮件分发转发器自动转发。\n\n" + mailbox.BodyText(message)
	writeBase64(&b, []byte(body))

	for _, attachment := range message.Attachments {
		fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		writeHeader(&b, "Content-Type", fmt.Sprintf("%s; name=%q", contentType, attachment.Filename))
		writeHeader(&b, "Content-Disposition", fmt.Sprintf("attachment; filename=%q", attachment.Filename))
		writeHeader(&b, "Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		writeBase64(&b, attachment.Data)
	}
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func (s *Service) sendSMTP(recipient string, raw []byte) error {
	addr := net.JoinHostPort(s.cfg.SMTP.Host, strconv.Itoa(s.cfg.SMTP.Port))
	var conn net.Conn
	var err error
	if s.cfg.SMTP.UseSSL {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: s.cfg.SMTP.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 30*time.Second)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.cfg.SMTP.Host)
	if err != nil {
		return err
	}
	defer client.Quit()

	if s.cfg.SMTP.StartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.SMTP.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.cfg.SMTP.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.SMTP.Username, s.cfg.SMTP.Password, s.cfg.SMTP.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(s.cfg.SMTP.From); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func writeHeader(b *bytes.Buffer, key string, value string) {
	fmt.Fprintf(b, "%s: %s\r\n", key, value)
}

func writeBase64(b *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		b.WriteString(encoded)
		b.WriteString("\r\n")
	}
}
