package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

const (
	masterKeySize     = 32
	credentialsMagic  = "BKG1"
	credentialsFile   = "credentials.bin"
	masterKeyFile     = "master.key"
	gatewayDirMode    = 0o700
	gatewayFileMode   = 0o600
	credentialFileLen = len(credentialsMagic)
)

// SecretStore keeps provider API keys encrypted at rest: a random master key
// in master.key and AES-256-GCM ciphertext in credentials.bin. It is not a
// system credential store; it protects against reading the files by accident,
// not against a user who can read the whole directory.
type SecretStore struct {
	dir      string
	keyPath  string
	dataPath string

	mu   sync.Mutex
	key  []byte
	keys map[string]string
}

// NewSecretStore returns a store reading and writing in dir.
func NewSecretStore(dir string) *SecretStore {
	return &SecretStore{
		dir:      dir,
		keyPath:  filepath.Join(dir, masterKeyFile),
		dataPath: filepath.Join(dir, credentialsFile),
		keys:     map[string]string{},
	}
}

// Load reads the master key, creating it when missing, and decrypts the
// credentials. A missing credentials file is empty; a damaged one leaves the
// keys empty so that the user can re-enter them.
func (s *SecretStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, gatewayDirMode); err != nil {
		return fmt.Errorf("gateway: create gateway directory: %w", err)
	}
	if err := s.loadKey(); err != nil {
		return err
	}
	data, err := os.ReadFile(s.dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.keys = map[string]string{}
			return nil
		}
		return fmt.Errorf("gateway: read credentials: %w", err)
	}
	plain, err := s.decrypt(data)
	if err != nil {
		s.keys = map[string]string{}
		return err
	}
	keys := map[string]string{}
	if len(plain) > 0 {
		if err := json.Unmarshal(plain, &keys); err != nil {
			s.keys = map[string]string{}
			return fmt.Errorf("gateway: parse credentials: %w", err)
		}
	}
	s.keys = keys
	return nil
}

func (s *SecretStore) loadKey() error {
	key, err := os.ReadFile(s.keyPath)
	if err == nil {
		if len(key) != masterKeySize {
			return fmt.Errorf("gateway: master key has %d bytes, want %d", len(key), masterKeySize)
		}
		s.key = key
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("gateway: read master key: %w", err)
	}
	key = make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("gateway: generate master key: %w", err)
	}
	if err := os.WriteFile(s.keyPath, key, gatewayFileMode); err != nil {
		return fmt.Errorf("gateway: write master key: %w", err)
	}
	s.key = key
	return nil
}

// Secret returns the API key of a provider, empty when there is none.
func (s *SecretStore) Secret(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[id]
}

// Masked returns the label the settings page shows for a provider key.
func (s *SecretStore) Masked(id string) string {
	return Mask(s.Secret(id))
}

// Set stores or replaces the API key of a provider and writes the credentials
// file. An empty key deletes the entry.
func (s *SecretStore) Set(id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		delete(s.keys, id)
	} else {
		s.keys[id] = key
	}
	return s.saveLocked()
}

// Delete removes the API key of a provider and writes the credentials file.
func (s *SecretStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[id]; !ok {
		return nil
	}
	delete(s.keys, id)
	return s.saveLocked()
}

func (s *SecretStore) saveLocked() error {
	plain, err := json.Marshal(s.keys)
	if err != nil {
		return fmt.Errorf("gateway: encode credentials: %w", err)
	}
	data, err := s.encrypt(plain)
	if err != nil {
		return err
	}
	tmp := s.dataPath + ".tmp"
	if err := os.WriteFile(tmp, data, gatewayFileMode); err != nil {
		return fmt.Errorf("gateway: write credentials: %w", err)
	}
	if err := os.Rename(tmp, s.dataPath); err != nil {
		if retry := os.Rename(tmp, s.dataPath); retry != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("gateway: replace credentials: %w", retry)
		}
	}
	return nil
}

func (s *SecretStore) encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("gateway: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gateway: cipher: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("gateway: nonce: %w", err)
	}
	out := make([]byte, 0, credentialFileLen+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, credentialsMagic...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, nil), nil
}

func (s *SecretStore) decrypt(data []byte) ([]byte, error) {
	if len(data) < credentialFileLen {
		return nil, fmt.Errorf("gateway: credentials file is damaged")
	}
	if string(data[:credentialFileLen]) != credentialsMagic {
		return nil, fmt.Errorf("gateway: credentials file has an unknown format")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("gateway: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gateway: cipher: %w", err)
	}
	rest := data[credentialFileLen:]
	if len(rest) < gcm.NonceSize() {
		return nil, fmt.Errorf("gateway: credentials file is damaged")
	}
	nonce, ciphertext := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gateway: decrypt credentials: %w", err)
	}
	return plain, nil
}

// Mask returns the label of an API key: the last four characters behind dots
// when it is long enough, whether it is configured when it is short, and 未配置
// when it is empty.
func Mask(key string) string {
	if key == "" {
		return "未配置"
	}
	runes := []rune(key)
	if utf8.RuneCountInString(key) < 8 {
		return "已配置"
	}
	return "••••" + string(runes[len(runes)-4:])
}
