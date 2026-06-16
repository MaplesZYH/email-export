package forwarder

import (
	"testing"

	"mail-forwarder/internal/mailbox"
)

func TestDedupKeyDependsOnRecipient(t *testing.T) {
	message := mailbox.Message{
		UID:       100,
		MessageID: "<message@example.com>",
		Attachments: []mailbox.Attachment{
			{Hash: "hash-a"},
		},
	}
	a := DedupKey("boss@example.com", message, "a@example.com")
	b := DedupKey("boss@example.com", message, "b@example.com")
	if a == b {
		t.Fatal("dedup key should differ by recipient")
	}
}

func TestDedupKeyFallsBackToUID(t *testing.T) {
	message := mailbox.Message{
		UID: 100,
		Attachments: []mailbox.Attachment{
			{Hash: "hash-a"},
		},
	}
	first := DedupKey("boss@example.com", message, "a@example.com")
	second := DedupKey("boss@example.com", message, "a@example.com")
	if first == "" || first != second {
		t.Fatalf("dedup key unstable: %q %q", first, second)
	}
}
