package notification

import "testing"

func TestSecretBoxRoundTrip(t *testing.T) {
	t.Parallel()

	box, err := newSecretBox([]byte("a sufficiently long application secret"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.seal("device-key-123")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "device-key-123" {
		t.Fatal("sealed secret must not contain plaintext")
	}
	opened, err := box.open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if opened != "device-key-123" {
		t.Fatalf("opened secret = %q", opened)
	}
}

func TestSecretBoxRejectsDifferentKey(t *testing.T) {
	t.Parallel()

	first, _ := newSecretBox([]byte("first application secret"))
	second, _ := newSecretBox([]byte("second application secret"))
	sealed, err := first.seal("device-key-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.open(sealed); err == nil {
		t.Fatal("expected decryption with a different key to fail")
	}
}
