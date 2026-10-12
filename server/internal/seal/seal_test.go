package seal

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSealAndOpen(t *testing.T) {
	key, err := ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := key.Seal([]byte("token"), "import:1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("token")) {
		t.Error("the sealed value holds the secret in the clear")
	}
	if again, _ := key.Seal([]byte("token"), "import:1"); bytes.Equal(sealed, again) {
		t.Error("sealing the same thing twice gave the same bytes")
	}
	if plain, err := key.Open(sealed, "import:1"); err != nil || string(plain) != "token" {
		t.Errorf("Open = %q, %v", plain, err)
	}
	// It opens only for what it was sealed for, and only untouched.
	if _, err := key.Open(sealed, "import:2"); err == nil {
		t.Error("opened for another row")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := key.Open(sealed, "import:1"); err == nil {
		t.Error("opened after being changed")
	}
	other, _ := ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	sealed[len(sealed)-1] ^= 1
	if _, err := other.Open(sealed, "import:1"); err == nil {
		t.Error("opened with another key")
	}
}

func TestParseKeyRefusesWeakKeys(t *testing.T) {
	for _, bad := range []string{"", "hunter2", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
}
