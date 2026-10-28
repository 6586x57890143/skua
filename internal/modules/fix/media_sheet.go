package fix

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	sheetWidth     = 1200
	sheetHeight    = 900
	sheetGap       = 6
	sheetMaxBytes  = 6 << 20
	sheetMaxPixels = 20 << 20
)

type sheetPayload struct {
	Platform string   `json:"p"`
	URLs     []string `json:"u"`
}

func (a *API) prepareGalleries(ctx context.Context, p Post, platform string) Post {
	if a.media == nil {
		return p
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	probes := 10
	p.Media = measureGallery(ctx, platform, p.Media, &probes)
	if link := a.media.galleryLink(platform, p.Media); link != "" {
		p.Media = []Media{galleryMedia(link, p.Media)}
	}
	for i := range p.Sections {
		p.Sections[i].Media = measureGallery(ctx, platform, p.Sections[i].Media, &probes)
		if link := a.media.galleryLink(platform, p.Sections[i].Media); link != "" {
			p.Sections[i].Media = []Media{galleryMedia(link, p.Sections[i].Media)}
		}
	}
	return p
}

func galleryMedia(link string, source []Media) Media {
	plan, _ := planGallery(source)
	var alt []string
	sensitive := false
	for i, item := range source {
		sensitive = sensitive || item.Sensitive
		if item.Alt != "" {
			alt = append(alt, "image "+strconv.Itoa(i+1)+": "+clip(item.Alt, max(20, 900/len(source))))
		}
	}
	return Media{URL: link, Type: "photo", Alt: clip(strings.Join(alt, " · "), 1000), Width: plan.Width, Height: plan.Height, Sensitive: sensitive}
}

// galleryLink carries the source URLs in an authenticated, encrypted token.
// The Discord-visible URL reveals neither upstream URLs nor their query tokens.
func (m *mediaRelay) galleryLink(platform string, media []Media) string {
	if m == nil || !compositeGallery(media) {
		return ""
	}
	payload := sheetPayload{Platform: platform, URLs: make([]string, 0, len(media))}
	for _, item := range media {
		raw := remoteURL(item.URL)
		if raw == "" || !trustedGalleryURL(platform, raw) {
			return ""
		}
		payload.URLs = append(payload.URLs, raw)
	}
	plain, err := json.Marshal(payload)
	if err != nil || len(plain) > 16384 {
		return ""
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		return ""
	}
	if err := writer.Close(); err != nil || compressed.Len() > 1300 {
		return ""
	}
	plain = compressed.Bytes()
	key := sha256.Sum256([]byte("skua/fix/gallery/v1:" + m.secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return ""
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte("skua/fix/gallery/v1")))
	link := m.base + "/fix/media/gallery/" + token + "/" + m.signature("gallery", token) + ".png"
	if len(link) > 2048 {
		return ""
	}
	return link
}

func (m *mediaRelay) openGallery(token string) (sheetPayload, error) {
	if len(token) > 1800 {
		return sheetPayload{}, errMetadata
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return sheetPayload{}, errMetadata
	}
	key := sha256.Sum256([]byte("skua/fix/gallery/v1:" + m.secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return sheetPayload{}, errMetadata
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return sheetPayload{}, errMetadata
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("skua/fix/gallery/v1"))
	if err != nil {
		return sheetPayload{}, errMetadata
	}
	// Older signed URLs contain uncompressed JSON; preserve those links.
	if len(plain) > 0 && plain[0] != '{' {
		reader, err := zlib.NewReader(bytes.NewReader(plain))
		if err != nil {
			return sheetPayload{}, errMetadata
		}
		decoded, readErr := io.ReadAll(io.LimitReader(reader, 16385))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(decoded) > 16384 {
			return sheetPayload{}, errMetadata
		}
		plain = decoded
	}
	var payload sheetPayload
	if json.Unmarshal(plain, &payload) != nil || len(payload.URLs) < 2 || len(payload.URLs) > 10 {
		return sheetPayload{}, errMetadata
	}
	for _, rawURL := range payload.URLs {
		if !trustedGalleryURL(payload.Platform, rawURL) {
			return sheetPayload{}, errMetadata
		}
	}
	return payload, nil
}

func trustedGalleryURL(platform, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") || remoteURL(raw) == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch platform {
	case "twitter":
		return host == "pbs.twimg.com"
	case "bluesky":
		return host == "cdn.bsky.app"
	case "reddit":
		return host == "i.redd.it" || host == "preview.redd.it" || host == "i.reddituploads.com"
	case "instagram", "threads":
		return host == "cdninstagram.com" || strings.HasSuffix(host, ".cdninstagram.com") || host == "fbcdn.net" || strings.HasSuffix(host, ".fbcdn.net")
	case "pixiv":
		return host == "i.pximg.net"
	case "pinterest":
		return host == "i.pinimg.com"
	case "tumblr":
		return host == "media.tumblr.com" || strings.HasSuffix(host, ".media.tumblr.com")
	case "tiktok":
		return hostSuffix(host, "tiktokcdn.com", "tiktokv.com", "byteoversea.com", "ibytedtos.com")
	case "bilibili":
		return host == "i0.hdslb.com" || host == "i1.hdslb.com" || host == "i2.hdslb.com"
	case "deviantart":
		return hostSuffix(host, "wixmp.com")
	default:
		return false
	}
}

func hostSuffix(host string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (m *mediaRelay) prepareGallery(ctx context.Context, token string) ([]byte, error) {
	payload, err := m.openGallery(token)
	if err != nil {
		return nil, err
	}
	images := make([]image.Image, len(payload.URLs))
	var wg sync.WaitGroup
	var pixels atomic.Int64
	slots := make(chan struct{}, 2)
	errs := make(chan error, len(payload.URLs))
	for i, raw := range payload.URLs {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
			defer func() { <-slots }()
			img, err := fetchGalleryImage(ctx, payload.Platform, raw, &pixels)
			if err != nil {
				errs <- err
				return
			}
			images[i] = img
		}(i, raw)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return composeGallery(images)
}

func openGalleryImage(ctx context.Context, platform, raw string, headerOnly bool) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil || !trustedGalleryURL(platform, raw) {
		return nil, errMetadata
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, errMetadata
	}
	var public net.IP
	for _, candidate := range ips {
		ip := candidate.IP
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast() {
			public = ip
			break
		}
	}
	if public == nil {
		return nil, errMetadata
	}
	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil || port != "443" {
				return nil, errMetadata
			}
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(public.String(), port))
		},
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errMetadata
	}
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	req.Header.Set("User-Agent", "skua/fix")
	if headerOnly {
		req.Header.Set("Range", "bytes=0-65535")
	}
	if platform == "pixiv" {
		req.Header.Set("Referer", "https://www.pixiv.net/")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errMetadata
	}
	return resp, nil
}

func fetchGalleryImage(ctx context.Context, platform, raw string, pixels *atomic.Int64) (image.Image, error) {
	resp, err := openGalleryImage(ctx, platform, raw, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > sheetMaxBytes {
		return nil, errMetadata
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, sheetMaxBytes+1))
	if err != nil || len(data) > sheetMaxBytes {
		return nil, errMetadata
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") || config.Width < 1 || config.Height < 1 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 8<<20 {
		return nil, errMetadata
	}
	if pixels.Add(int64(config.Width)*int64(config.Height)) > sheetMaxPixels {
		return nil, errMetadata
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errMetadata
	}
	return img, nil
}

func composeGallery(images []image.Image) ([]byte, error) {
	if len(images) < 2 || len(images) > 10 {
		return nil, errMetadata
	}
	var pixels int64
	var dimensions []Media
	for _, img := range images {
		if img == nil {
			return nil, errMetadata
		}
		b := img.Bounds()
		if b.Dx() <= 0 || b.Dy() <= 0 {
			return nil, errMetadata
		}
		dimensions = append(dimensions, Media{Width: b.Dx(), Height: b.Dy()})
		pixels += int64(b.Dx()) * int64(b.Dy())
	}
	if pixels > sheetMaxPixels {
		return nil, errMetadata
	}
	plan, ok := planGallery(dimensions)
	if !ok {
		return nil, errMetadata
	}
	canvas := image.NewRGBA(image.Rect(0, 0, plan.Width, plan.Height))
	for i, img := range images {
		xdraw.CatmullRom.Scale(canvas, plan.Tiles[i], img, img.Bounds(), draw.Over, nil)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil || out.Len() > sheetMaxBytes {
		return nil, errMetadata
	}
	return out.Bytes(), nil
}
