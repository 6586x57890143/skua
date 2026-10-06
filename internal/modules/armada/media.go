// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	// maxFile is the ceiling on one attachment either way, Discord's upload
	// limit for a server without boosts.
	maxFile = 10 << 20
	// maxHeld is what one rumor's attachments may hold in memory at once.
	// The per-file cap and the attempt cap multiply, so this is the bound
	// that matters; it sits above what Discord takes in one message.
	maxHeld = 25 << 20
	// maxAttempts counts download attempts, not successes: a cap on
	// successes is no cap against URLs chosen to fail.
	maxAttempts = 10
)

// imeta is one NIP-92 tag's fields.
type imeta map[string]string

func imetaOf(tag []string) imeta {
	m := imeta{}
	for _, part := range tag[1:] {
		if k, v, ok := strings.Cut(part, " "); ok {
			m[k] = v
		}
	}
	return m
}

var hexRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

func isHex(s string, n int) bool {
	return s != "" && len(s)%2 == 0 && hexRe.MatchString(s) && (n == 0 || len(s) == n)
}

// keyMaterial reads hex, or base64 of either alphabet, as lowercase hex.
func keyMaterial(v string) string {
	v = strings.TrimSpace(v)
	if isHex(v, 0) {
		return strings.ToLower(v)
	}
	b, err := base64.StdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(v))
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(v, "=")))
	}
	if err != nil || len(b) == 0 {
		return v
	}
	return hex.EncodeToString(b)
}

// sealedWith is how an attachment is encrypted: "" for not at all, "bad"
// for a scheme skua can't apply, else "aes-gcm" with key and nonce in hex.
func (m imeta) sealedWith() (algo, key, nonce, ox string) {
	algo = strings.ToLower(m["encryption-algorithm"])
	if algo == "" {
		return "", "", "", ""
	}
	key, nonce = keyMaterial(m["decryption-key"]), keyMaterial(m["decryption-nonce"])
	if algo != "aes-gcm" || !isHex(key, 64) || !isHex(nonce, 0) {
		return "bad", "", "", ""
	}
	if o := m["ox"]; isHex(o, 64) {
		ox = strings.ToLower(o)
	}
	return algo, key, nonce, ox
}

// decrypt opens an AES-256-GCM attachment and checks it against ox when
// the tag carries one. Armada encrypts every attachment, so skipping this
// would hand Discord ciphertext under the sender's name.
func decrypt(data []byte, key, nonce, ox string) ([]byte, error) {
	k, _ := hex.DecodeString(key)
	n, _ := hex.DecodeString(nonce)
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(n))
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, n, data, nil)
	if err != nil {
		return nil, err
	}
	if ox != "" {
		if sum := sha256.Sum256(plain); hex.EncodeToString(sum[:]) != ox {
			return nil, errors.New("armada: decrypted attachment does not match its ox hash")
		}
	}
	return plain, nil
}

var extByMime = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "image/avif": "avif",
	"image/svg+xml": "svg", "video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov",
	"audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/ogg": "ogg", "audio/wav": "wav", "application/pdf": "pdf",
}

var mimeByExt = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp",
	"avif": "image/avif", "mp4": "video/mp4", "webm": "video/webm", "mov": "video/quicktime",
	"mp3": "audio/mpeg", "m4a": "audio/mp4", "ogg": "audio/ogg", "wav": "audio/wav",
}

var (
	extRe     = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)
	subtypeRe = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
	unsafeRe  = regexp.MustCompile(`[^\w.\- ]+`)
)

func extFor(mimeType string) string {
	t := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if e, ok := extByMime[t]; ok {
		return e
	}
	if _, sub, ok := strings.Cut(t, "/"); ok {
		if sub, _, _ = strings.Cut(sub, "+"); subtypeRe.MatchString(sub) {
			return sub
		}
	}
	return ""
}

func lastSegment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if b := path.Base(u.Path); b != "/" && b != "." {
		return b
	}
	return ""
}

func mimeFromURL(raw string) string {
	e := extRe.FindString(lastSegment(raw))
	if e == "" {
		return ""
	}
	return mimeByExt[strings.ToLower(e[1:])]
}

// withExt puts the type's extension on a content-addressed URL that has
// none: Blossom serves the blob either way, and the extension is what
// Discord's unfurl and Armada's sniffing read.
func withExt(raw, mimeType string) string {
	e := extFor(mimeType)
	u, err := url.Parse(raw)
	if e == "" || err != nil {
		return raw
	}
	if last := path.Base(u.Path); last == "/" || last == "." || u.Path == "" || extRe.MatchString(last) {
		return raw
	}
	u.Path += "." + e
	return u.String()
}

// filename is a safe upload name: the declared one, else the URL's, with
// the extension the type implies when it has none.
func filename(raw, mimeType, declared string) string {
	name := strings.TrimSpace(declared)
	if name == "" {
		name = lastSegment(raw)
	}
	name = strings.TrimSpace(strings.TrimLeft(unsafeRe.ReplaceAllString(name, "_"), ". \t"))
	if name == "" {
		name = "file"
	}
	ext := extRe.FindString(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == "" {
		if e := extFor(mimeType); e != "" {
			ext = "." + e
		} else {
			ext = ".bin"
		}
	}
	if r := []rune(stem); len(r) > 96-len(ext) {
		stem = string(r[:96-len(ext)])
	}
	if stem == "" {
		stem = "file"
	}
	return stem + ext
}

// blossom rehosts Discord attachments, whose signed CDN links expire in a
// day, on Blossom servers tried in order. The upload is signed by the
// member's puppet (BUD-02), so it is attributed to them.
type blossom struct {
	servers []string
	http    *http.Client
}

func (b *blossom) upload(ctx context.Context, data []byte, mimeType, puppetSK string) (string, string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	now := nostr.Now()
	auth := nostr.Event{
		Kind: 24242, Content: "Upload bridged Discord attachment", CreatedAt: now,
		// One event covers every server: BUD-02 scopes it by x, not host.
		Tags: nostr.Tags{{"t", "upload"}, {"x", hash}, {"expiration", fmt.Sprint(now + 300)}},
	}
	if err := auth.Sign(puppetSK); err != nil {
		return "", "", err
	}
	header := "Nostr " + base64.StdEncoding.EncodeToString([]byte(auth.String()))
	var errs []error
	for _, base := range b.servers {
		got, err := b.put(ctx, base, data, mimeType, header)
		if err == nil {
			if got == "" {
				got = base + "/" + hash
			}
			return withExt(got, mimeType), hash, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", base, err))
	}
	return "", "", errors.Join(append([]error{errors.New("armada: every blossom server refused the upload")}, errs...)...)
}

func (b *blossom) put(ctx context.Context, base string, data []byte, mimeType, header string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/upload", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", header)
	req.Header.Set("Content-Type", mimeType)
	res, err := b.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("answered %s", res.Status)
	}
	var desc struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(res.Body).Decode(&desc)
	if u, err := url.Parse(desc.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", nil
	}
	return desc.URL, nil
}
