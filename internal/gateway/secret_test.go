package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, masterKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != masterKeySize {
		t.Errorf("master key = %d bytes, want %d", info.Size(), masterKeySize)
	}
	if err := store.Set("p-00000001", "sk-secret-value"); err != nil {
		t.Fatal(err)
	}
	if got := store.Secret("p-00000001"); got != "sk-secret-value" {
		t.Errorf("Secret = %q", got)
	}

	reloaded := NewSecretStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Secret("p-00000001"); got != "sk-secret-value" {
		t.Errorf("reloaded Secret = %q", got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, credentialsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret-value") {
		t.Error("credentials file holds the plaintext key")
	}
	if !strings.HasPrefix(string(raw), credentialsMagic) {
		t.Error("credentials file does not start with the magic")
	}

	if err := reloaded.Delete("p-00000001"); err != nil {
		t.Fatal(err)
	}
	third := NewSecretStore(dir)
	if err := third.Load(); err != nil {
		t.Fatal(err)
	}
	if got := third.Secret("p-00000001"); got != "" {
		t.Errorf("deleted Secret = %q, want empty", got)
	}
}

func TestSecretStoreRejectsTamperedFile(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("p-00000001", "sk-secret-value"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, credentialsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	tampered := NewSecretStore(dir)
	if err := tampered.Load(); err == nil {
		t.Error("tampered credentials loaded without an error")
	}
	if got := tampered.Secret("p-00000001"); got != "" {
		t.Errorf("tampered Secret = %q, want empty", got)
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "未配置"},
		{"short", "已配置"},
		{"12345678", "••••5678"},
		{"sk-abcdefgh", "••••efgh"},
	}
	for _, tc := range tests {
		if got := Mask(tc.in); got != tc.want {
			t.Errorf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
