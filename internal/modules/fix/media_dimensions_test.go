package fix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestLegacyGalleryTokenStillOpens(t *testing.T) {
	relay := newMediaRelay(&API{}, "https://hooks.example", "a-secret-long-enough-for-tests")
	payload := sheetPayload{Platform: "twitter", URLs: []string{"https://pbs.twimg.com/a.jpg", "https://pbs.twimg.com/b.jpg", "https://pbs.twimg.com/c.jpg", "https://pbs.twimg.com/d.jpg"}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("skua/fix/gallery/v1:" + relay.secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	token := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte("skua/fix/gallery/v1")))
	got, err := relay.openGallery(token)
	if err != nil || len(got.URLs) != 4 || got.URLs[0] != payload.URLs[0] {
		t.Fatal(got, err)
	}
}
func TestGalleryDimensionProbeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	media := []Media{{Type: "photo", URL: "https://pbs.twimg.com/a.jpg"}, {Type: "photo", URL: "https://pbs.twimg.com/b.jpg"}}
	probes := 1
	got := measureGallery(ctx, "twitter", media, &probes)
	if probes != 0 || got[0].Width != 0 || got[1].Width != 0 {
		t.Fatal("cancelled or unmeasured sources must retain native layout", got, probes)
	}
	if compositeGallery(got) {
		t.Fatal("missing dimensions should not select a composite")
	}
}
