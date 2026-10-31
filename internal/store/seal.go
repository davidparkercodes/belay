package store

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const hkdfInfoSeal = "belay spool seal v1"

// sealKeys let hooks hand content to the daemon without loading the store key.
type sealKeys struct {
	public  [32]byte
	private [32]byte
}

func deriveSealKeys(master []byte) (*sealKeys, error) {
	priv, err := hkdf.Key(sha256.New, master, nil, hkdfInfoSeal, 32)
	if err != nil {
		return nil, fmt.Errorf("derive seal key: %w", err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive seal public key: %w", err)
	}
	k := &sealKeys{}
	copy(k.private[:], priv)
	copy(k.public[:], pub)
	return k, nil
}

// SealRecipient returns the public key content is sealed to for this store's key.
// ok is false when the store does not encrypt.
func (s *Store) SealRecipient() (public [32]byte, ok bool) {
	if !s.Encrypting() || s.seal == nil {
		return public, false
	}
	return s.seal.public, true
}

// SealFingerprint names a seal public key so a published recipient can be checked
// against the store manifest before anything is sealed to it.
func SealFingerprint(public [32]byte) string {
	sum := sha256.Sum256(append([]byte("belay seal key v1:"), public[:]...))
	return hex.EncodeToString(sum[:16])
}

// OpenSealed decrypts content produced by Seal for this store's recipient key.
func (s *Store) OpenSealed(sealed []byte) ([]byte, error) {
	if s.seal == nil {
		return nil, errors.New("open sealed content: no store key loaded")
	}
	out, ok := box.OpenAnonymous(nil, sealed, &s.seal.public, &s.seal.private)
	if !ok {
		return nil, errors.New("open sealed content: not sealed to this store's key")
	}
	return out, nil
}

// Seal encrypts data to a recipient public key. It needs no secret, so it is safe
// to call from short-lived processes that must not load the store key.
func Seal(recipient [32]byte, data []byte) ([]byte, error) {
	return box.SealAnonymous(nil, data, &recipient, rand.Reader)
}
