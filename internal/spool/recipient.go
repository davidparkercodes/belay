package spool

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const recipientFile = ".recipient"

// Recipient is the public key the daemon publishes so hooks can queue file content
// without loading the store key.
type Recipient struct {
	KeyID     string   `json:"key_id"`
	PublicKey [32]byte `json:"-"`
}

type recipientJSON struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

func RecipientPath(dir string) string {
	return filepath.Join(dir, recipientFile)
}

// LoadRecipient reads the published recipient. ok is false when none is published.
func LoadRecipient(dir string) (Recipient, bool, error) {
	data, err := os.ReadFile(RecipientPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return Recipient{}, false, nil
		}
		return Recipient{}, false, err
	}
	var rj recipientJSON
	if err := json.Unmarshal(data, &rj); err != nil {
		return Recipient{}, false, fmt.Errorf("parse spool recipient: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(rj.PublicKey)
	if err != nil || len(pub) != 32 || rj.KeyID == "" {
		return Recipient{}, false, errors.New("parse spool recipient: malformed key")
	}
	r := Recipient{KeyID: rj.KeyID}
	copy(r.PublicKey[:], pub)
	return r, true, nil
}

// PublishRecipient writes the recipient atomically, skipping the write when unchanged.
func PublishRecipient(dir string, r Recipient) error {
	if cur, ok, err := LoadRecipient(dir); err == nil && ok && cur == r {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create spool dir: %w", err)
	}
	data, err := json.Marshal(recipientJSON{KeyID: r.KeyID, PublicKey: base64.StdEncoding.EncodeToString(r.PublicKey[:])})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-recipient-*")
	if err != nil {
		return fmt.Errorf("write spool recipient: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write spool recipient: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write spool recipient: %w", err)
	}
	if err := os.Rename(tmp.Name(), RecipientPath(dir)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("install spool recipient: %w", err)
	}
	return nil
}

func RemoveRecipient(dir string) error {
	err := os.Remove(RecipientPath(dir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
