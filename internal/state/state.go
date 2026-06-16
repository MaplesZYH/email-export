package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

type Store struct {
	LastUID    uint32              `json:"last_uid"`
	Deliveries map[string]Delivery `json:"deliveries"`
}

type Delivery struct {
	MessageID    string    `json:"message_id"`
	UID          uint32    `json:"uid"`
	Recipient    string    `json:"recipient"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	RetryCount   int       `json:"retry_count"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{Deliveries: make(map[string]Delivery)}, nil
	}
	if err != nil {
		return nil, err
	}
	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Deliveries == nil {
		store.Deliveries = make(map[string]Delivery)
	}
	return &store, nil
}

func (s *Store) Save(path string) error {
	if s.Deliveries == nil {
		s.Deliveries = make(map[string]Delivery)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) IsSent(key string) bool {
	delivery, ok := s.Deliveries[key]
	return ok && delivery.Status == StatusSuccess
}

func (s *Store) MarkSuccess(key string, messageID string, uid uint32, recipient string) {
	s.Deliveries[key] = Delivery{
		MessageID: messageID,
		UID:       uid,
		Recipient: recipient,
		Status:    StatusSuccess,
		UpdatedAt: time.Now().UTC(),
	}
}

func (s *Store) MarkFailed(key string, messageID string, uid uint32, recipient string, err error) {
	previous := s.Deliveries[key]
	message := ""
	if err != nil {
		message = err.Error()
	}
	s.Deliveries[key] = Delivery{
		MessageID:    messageID,
		UID:          uid,
		Recipient:    recipient,
		Status:       StatusFailed,
		ErrorMessage: message,
		RetryCount:   previous.RetryCount + 1,
		UpdatedAt:    time.Now().UTC(),
	}
}
