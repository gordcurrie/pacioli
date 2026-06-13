package sqlite_test

import (
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func key32() []byte { return []byte("12345678901234567890123456789012") }

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	plaintext := "secret-token-value"
	enc, err := sqlite.Encrypt(key32(), plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := sqlite.Decrypt(key32(), enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Errorf("got %q want %q", got, plaintext)
	}
}

func TestEncryptDecrypt_UniqueNonce(t *testing.T) {
	key := key32()
	a, _ := sqlite.Encrypt(key, "same")
	b, _ := sqlite.Encrypt(key, "same")
	if a == b {
		t.Error("two encryptions of same plaintext must differ (nonce reuse)")
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	enc, err := sqlite.Encrypt(key32(), "")
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	got, err := sqlite.Decrypt(key32(), enc)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if got != "" {
		t.Errorf("got %q want empty", got)
	}
}

func TestEncrypt_WrongKeyLength(t *testing.T) {
	_, err := sqlite.Encrypt([]byte("short"), "x")
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("expected 32-bytes error, got %v", err)
	}
}

func TestDecrypt_WrongKeyLength(t *testing.T) {
	_, err := sqlite.Decrypt([]byte("short"), "anything")
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("expected 32-bytes error, got %v", err)
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	_, err := sqlite.Decrypt(key32(), "not-valid-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode error, got %v", err)
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	// valid base64 but fewer bytes than GCM nonce size (12)
	import64 := "dG9vc2hvcnQ=" // "tooshort" (8 bytes)
	_, err := sqlite.Decrypt(key32(), import64)
	if err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected too-short error, got %v", err)
	}
}

func TestDecrypt_Tampered(t *testing.T) {
	enc, _ := sqlite.Encrypt(key32(), "hello")
	// flip last byte
	decoded := []byte(enc)
	decoded[len(decoded)-1] ^= 0xFF
	_, err := sqlite.Decrypt(key32(), string(decoded))
	if err == nil {
		t.Error("expected error decrypting tampered ciphertext")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc, _ := sqlite.Encrypt(key32(), "hello")
	wrongKey := []byte("99999999999999999999999999999999")
	_, err := sqlite.Decrypt(wrongKey, enc)
	if err == nil {
		t.Error("expected error decrypting with wrong key")
	}
}
