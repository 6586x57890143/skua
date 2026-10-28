package fix

import (
	"bytes"
	"context"
	"image"
	"io"
	"net/http"
	"sync"
)

// Probe only bounded headers, never whole images, to recover missing provider
// dimensions. One shared deadline and request budget covers all post sections.
func measureGallery(ctx context.Context, platform string, media []Media, probes *int) []Media {
	if len(media) < 2 || len(media) > 10 {
		return media
	}
	for _, item := range media {
		if item.Type != "photo" || !trustedGalleryURL(platform, item.URL) {
			return media
		}
	}
	result := append([]Media(nil), media...)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, item := range result {
		if item.Width > 0 && item.Height > 0 {
			continue
		}
		if *probes == 0 {
			break
		}
		*probes--
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			resp, err := openGalleryImage(ctx, platform, result[i].URL, true)
			if err != nil {
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
				return
			}
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
			if err != nil {
				return
			}
			config, format, err := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil || (format != "jpeg" && format != "png" && format != "webp") || config.Width < 1 || config.Height < 1 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 8<<20 {
				return
			}
			result[i].Width, result[i].Height = config.Width, config.Height
		}(i)
	}
	wg.Wait()
	return result
}
