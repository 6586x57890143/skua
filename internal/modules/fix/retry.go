package fix

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	metadataBudget  = 9 * time.Second
	metadataAttempt = 4 * time.Second
	metadataReserve = 500 * time.Millisecond
)

// Only bounded, content-free failure details leave the HTTP boundary. In
// particular, URL errors can contain credentials or post text from an endpoint.
type metadataFailure struct {
	reason string
	status int
	retry  bool
	after  time.Duration
}

func (e *metadataFailure) Error() string {
	if e.status != 0 {
		return "fix: metadata " + e.reason + " (http " + strconv.Itoa(e.status) + ")"
	}
	return "fix: metadata " + e.reason
}

func (*metadataFailure) Unwrap() error { return errMetadata }

func transientTransport(err error) bool {
	var u *url.Error
	if errors.As(err, &u) {
		err = u.Err
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func retryAfter(raw string) time.Duration {
	if seconds, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil {
		// Larger values cannot fit this lookup; clamp before converting to avoid
		// overflow turning a long rate limit into an immediate retry.
		if seconds > uint64(metadataBudget/time.Second) {
			return metadataBudget + time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		return max(0, time.Until(when))
	}
	return 0
}

func waitMetadataRetry(ctx context.Context, after time.Duration) bool {
	delay := max(after, 100*time.Millisecond+time.Duration(rand.Int64N(int64(100*time.Millisecond))))
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < metadataReserve || delay > remaining-metadataReserve {
			return false
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

type fixResponse struct {
	Success  bool     `json:"success"`
	Platform string   `json:"platform"`
	Data     *fixData `json:"data"`
}

// Retries live at the metadata boundary, before Reddit linked-media recovery.
// Successful lookups make one request; neither retries nor enrichment create
// goroutines, Discord edits or an additional worker admission.
func (a *API) fixMetadata(ctx context.Context, l Link) (*fixData, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataBudget)
	defer cancel()
	query := url.Values{"url": {l.URL}, "renderer": {"components-v2"}}
	endpoint := a.cfg.FixEmbed + "/api/embed?" + query.Encode()
	var best *fixData
	last := errMetadata
attempts:
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			break
		}
		var body fixResponse // never reuse fields from an earlier JSON response
		requestCtx, stop := context.WithTimeout(ctx, metadataAttempt)
		err := a.get(requestCtx, endpoint, &body)
		stop()
		after := time.Duration(0)
		if err != nil {
			last = err
			var failure *metadataFailure
			if !errors.As(err, &failure) || !failure.retry {
				break
			}
			after = failure.after
		} else {
			switch {
			case body.Platform != "" && body.Platform != l.Platform:
				last = &metadataFailure{reason: "platform mismatch"}
				break attempts
			case !body.Success || body.Data == nil:
				last = &metadataFailure{reason: "provider extraction failed", retry: true}
			case body.Platform == "":
				last = &metadataFailure{reason: "missing platform"}
				break attempts
			case body.Data.Title == "" && body.Data.Caption == "" && body.Data.Description == "" && !body.Data.hasMedia():
				last = &metadataFailure{reason: "empty response", retry: true}
			default:
				if best != nil && l.Platform == "instagram" {
					body.Data = mergeInstagram(best, body.Data)
				}
				best = body.Data
				if l.Platform != "instagram" || !best.partialInstagram() {
					return best, nil
				}
			}
		}
		if attempt == 1 || !waitMetadataRetry(ctx, after) {
			break
		}
		// FixEmbed caches successful responses even when extraction is partial.
		// Its supported cache bypass is needed for an actual recovery attempt;
		// custom provider endpoints retain their own query contract.
		if a.cfg.FixEmbed == "https://fixembed.app" {
			query.Set("_conformance", "1")
			endpoint = a.cfg.FixEmbed + "/api/embed?" + query.Encode()
		}
	}
	if best != nil {
		if l.Platform == "instagram" && best.partialInstagram() {
			best = a.instagramDirect(ctx, l, best)
		}
		return best, nil // recovery must never discard already usable media
	}
	return nil, last
}

func (d *fixData) hasMedia() bool {
	if d.Video != nil && remoteURL(d.Video.URL) != "" || remoteURL(d.Image) != "" {
		return true
	}
	for _, image := range d.Images {
		if remoteURL(image) != "" {
			return true
		}
	}
	return false
}

func (d *fixData) partialInstagram() bool {
	return !d.hasMedia() || strings.TrimSpace(d.Author) == "" && strings.TrimSpace(d.Handle) == ""
}

func mergeInstagram(first, next *fixData) *fixData {
	for _, pair := range [][2]*string{
		{&next.Author, &first.Author}, {&next.AuthorURL, &first.AuthorURL},
		{&next.Handle, &first.Handle}, {&next.Avatar, &first.Avatar},
		{&next.Caption, &first.Caption}, {&next.Description, &first.Description},
		{&next.Stats, &first.Stats}, {&next.Timestamp, &first.Timestamp},
		{&next.Context, &first.Context},
	} {
		if strings.TrimSpace(*pair[0]) == "" {
			*pair[0] = *pair[1]
		}
	}
	if next.Title == "" || strings.EqualFold(next.Title, "post") || strings.EqualFold(next.Title, "reel") {
		next.Title = first.Title
	}
	if !next.hasMedia() {
		next.Video, next.Image, next.Images, next.Descriptions = first.Video, first.Image, first.Images, first.Descriptions
	} else if first.Video != nil && remoteURL(first.Video.URL) != "" && (next.Video == nil || remoteURL(next.Video.URL) == "") {
		next.Video = first.Video // a recovered poster must not replace playable video
	}
	next.Sensitive = next.Sensitive || first.Sensitive
	if len(first.Sensitivity) > 0 {
		next.Sensitive = true
	}
	return next
}
