package mailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	_ "github.com/emersion/go-message/charset"
	msgmail "github.com/emersion/go-message/mail"

	"mail-forwarder/internal/config"
)

type Client struct {
	mailbox config.MailboxConfig
	forward config.ForwardConfig
}

type FetchResult struct {
	Messages    []Message
	NextUID     uint32
	Initialized bool
}

type Message struct {
	UID         uint32
	MessageID   string
	Subject     string
	From        string
	Date        *time.Time
	TextBody    string
	HTMLBody    string
	Attachments []Attachment
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
	Hash        string
}

func NewClient(mailboxCfg config.MailboxConfig, forwardCfg config.ForwardConfig) *Client {
	return &Client{mailbox: mailboxCfg, forward: forwardCfg}
}

func (c *Client) FetchNew(ctx context.Context, lastUID uint32) (FetchResult, error) {
	imapClient, err := c.open(ctx)
	if err != nil {
		return FetchResult{}, err
	}
	defer imapClient.Logout()

	status, err := imapClient.Select(c.mailbox.Folder, false)
	if err != nil {
		return FetchResult{}, err
	}
	maxUID, err := c.resolveMaxUID(imapClient, status)
	if err != nil {
		return FetchResult{}, err
	}
	if lastUID == 0 {
		return FetchResult{NextUID: maxUID, Initialized: true}, nil
	}
	if maxUID <= lastUID {
		return FetchResult{NextUID: lastUID}, nil
	}

	endUID := maxUID
	if limitEnd := lastUID + uint32(c.forward.MaxMessagesPerRun); limitEnd < endUID {
		endUID = limitEnd
	}
	messages, err := c.fetchMessages(ctx, imapClient, lastUID+1, endUID)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Messages: messages, NextUID: endUID}, nil
}

func (c *Client) open(ctx context.Context) (*client.Client, error) {
	addr := net.JoinHostPort(c.mailbox.Host, strconv.Itoa(c.mailbox.Port))
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	if c.mailbox.UseSSL {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: c.mailbox.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	imapClient, err := client.New(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	imapClient.Timeout = 30 * time.Second
	go func() {
		<-ctx.Done()
		_ = imapClient.Terminate()
	}()
	if err := imapClient.Login(c.mailbox.Username, c.mailbox.Password); err != nil {
		_ = imapClient.Logout()
		return nil, err
	}
	sendIMAPID(imapClient, c.mailbox.Username)
	return imapClient, nil
}

func (c *Client) resolveMaxUID(imapClient *client.Client, status *imap.MailboxStatus) (uint32, error) {
	if status != nil && status.UidNext > 0 {
		return status.UidNext - 1, nil
	}
	if status == nil || status.Messages == 0 {
		return 0, nil
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(1, 0)
	criteria := imap.NewSearchCriteria()
	criteria.Uid = seqSet
	uids, err := imapClient.UidSearch(criteria)
	if err != nil {
		return 0, err
	}
	maxUID := uint32(0)
	for _, uid := range uids {
		if uid > maxUID {
			maxUID = uid
		}
	}
	return maxUID, nil
}

func sendIMAPID(imapClient *client.Client, username string) {
	if imapClient == nil {
		return
	}
	supported, err := imapClient.Support("ID")
	if err != nil || !supported {
		return
	}
	values := []string{
		"name", "MailForwarder",
		"version", "1.0.0",
		"vendor", "Chaitin",
	}
	if contact := strings.TrimSpace(username); contact != "" {
		values = append(values, "contact", contact)
	}
	payload := make([]interface{}, len(values))
	for i, v := range values {
		payload[i] = v
	}
	cmd := &imap.Command{
		Name:      "ID",
		Arguments: []interface{}{payload},
	}
	_, _ = imapClient.Execute(cmd, nil)
}

func (c *Client) fetchMessages(ctx context.Context, imapClient *client.Client, fromUID uint32, toUID uint32) ([]Message, error) {
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(fromUID, toUID)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, section.FetchItem()}
	ch := make(chan *imap.Message, c.forward.MaxMessagesPerRun)
	done := make(chan error, 1)
	go func() { done <- imapClient.UidFetch(seqSet, items, ch) }()

	var messages []Message
	for {
		select {
		case <-ctx.Done():
			_ = imapClient.Terminate()
			return nil, ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				if err := <-done; err != nil {
					return nil, err
				}
				sort.Slice(messages, func(i, j int) bool { return messages[i].UID < messages[j].UID })
				return messages, nil
			}
			if msg == nil {
				continue
			}
			parsed := Message{UID: msg.Uid}
			if msg.Envelope != nil {
				parsed.MessageID = strings.TrimSpace(msg.Envelope.MessageId)
				parsed.Subject = decodeHeader(msg.Envelope.Subject)
				parsed.From = formatAddressList(msg.Envelope.From)
				if !msg.Envelope.Date.IsZero() {
					t := msg.Envelope.Date
					parsed.Date = &t
				}
			}
			if body := msg.GetBody(section); body != nil {
				textBody, htmlBody, attachments, err := parseBody(body, c.allowedExts())
				if err != nil {
					return nil, err
				}
				parsed.TextBody = textBody
				parsed.HTMLBody = htmlBody
				parsed.Attachments = attachments
			}
			if c.forward.RequireAttachments && len(parsed.Attachments) == 0 {
				continue
			}
			messages = append(messages, parsed)
		}
	}
}

func parseBody(r io.Reader, allowedExts map[string]struct{}) (string, string, []Attachment, error) {
	mr, err := msgmail.CreateReader(r)
	if err != nil {
		return "", "", nil, err
	}
	var textBody string
	var htmlBody string
	var attachments []Attachment
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return textBody, htmlBody, attachments, err
		}
		switch header := part.Header.(type) {
		case *msgmail.InlineHeader:
			contentType, _, _ := header.ContentType()
			data, err := io.ReadAll(part.Body)
			if err != nil {
				return textBody, htmlBody, attachments, err
			}
			switch strings.ToLower(contentType) {
			case "text/plain":
				if textBody == "" {
					textBody = string(data)
				}
			case "text/html":
				if htmlBody == "" {
					htmlBody = string(data)
				}
			}
		case *msgmail.AttachmentHeader:
			filename, _ := header.Filename()
			filename = strings.TrimSpace(decodeHeader(filename))
			if filename == "" {
				continue
			}
			if len(allowedExts) > 0 {
				if _, ok := allowedExts[strings.ToLower(filepath.Ext(filename))]; !ok {
					continue
				}
			}
			contentType, _, _ := header.ContentType()
			data, err := io.ReadAll(part.Body)
			if err != nil {
				return textBody, htmlBody, attachments, err
			}
			sum := sha256.Sum256(data)
			attachments = append(attachments, Attachment{
				Filename:    filename,
				ContentType: contentType,
				Data:        data,
				Hash:        hex.EncodeToString(sum[:]),
			})
		}
	}
	return textBody, htmlBody, attachments, nil
}

func (c *Client) allowedExts() map[string]struct{} {
	out := make(map[string]struct{})
	for _, ext := range c.forward.AllowedAttachmentExtensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out[ext] = struct{}{}
	}
	return out
}

func decodeHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func formatAddressList(addresses []*imap.Address) string {
	if len(addresses) == 0 {
		return ""
	}
	var out []string
	for _, addr := range addresses {
		if addr == nil {
			continue
		}
		email := addr.MailboxName + "@" + addr.HostName
		name := decodeHeader(addr.PersonalName)
		if name != "" {
			out = append(out, fmt.Sprintf("%s <%s>", name, email))
		} else {
			out = append(out, email)
		}
	}
	return strings.Join(out, ", ")
}

func AttachmentHashes(attachments []Attachment) string {
	if len(attachments) == 0 {
		return "no-attachment"
	}
	hashes := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		hashes = append(hashes, attachment.Hash)
	}
	sort.Strings(hashes)
	return strings.Join(hashes, ",")
}

func BodyText(message Message) string {
	if strings.TrimSpace(message.TextBody) != "" {
		return message.TextBody
	}
	if strings.TrimSpace(message.HTMLBody) != "" {
		return message.HTMLBody
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "原始发件人：%s\n", message.From)
	fmt.Fprintf(&b, "原始主题：%s\n", message.Subject)
	if message.Date != nil {
		fmt.Fprintf(&b, "原始时间：%s\n", message.Date.Format(time.RFC3339))
	}
	return b.String()
}
