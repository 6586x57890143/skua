package fix

import (
	"bytes"
	"context"
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
)

var (
	redditVideoID   = regexp.MustCompile(`^[a-z0-9]{6,20}$`)
	redditRendition = regexp.MustCompile(`^(?:CMAF|DASH)_[A-Za-z0-9_]{1,24}\.m3u8$`)
	redditAttr      = regexp.MustCompile(`([A-Z-]+)=("[^"]*"|[^,]*)`)
)

// Newer Reddit uploads have no packaged MP4, only CMAF video and audio files
// listed by the HLS master playlist. Pick a rendition no larger than 720p and
// the audio group that rendition names; an empty audio means a silent upload.
func (a *API) redditRenditions(ctx context.Context, id string) (video, audio string, err error) {
	if !redditVideoID.MatchString(id) {
		return "", "", errMetadata
	}
	base := "https://v.redd.it/" + id + "/"
	raw, err := a.clipBytes(ctx, base+"HLSPlaylist.m3u8", 0, 0, 64<<10)
	if err != nil || !bytes.HasPrefix(raw, []byte("#EXTM3U")) {
		return "", "", errMetadata
	}
	groups := map[string]string{}
	var group string
	best, rate := -1, 0
	var stream map[string]string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		attrs := map[string]string{}
		for _, m := range redditAttr.FindAllStringSubmatch(line, -1) {
			attrs[m[1]] = strings.Trim(m[2], `"`)
		}
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			if attrs["TYPE"] == "AUDIO" && redditRendition.MatchString(attrs["URI"]) {
				groups[attrs["GROUP-ID"]] = attrs["URI"]
			}
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			stream = attrs
		case stream != nil && !strings.HasPrefix(line, "#") && line != "":
			w, h, _ := strings.Cut(stream["RESOLUTION"], "x")
			width, _ := strconv.Atoi(w)
			height, _ := strconv.Atoi(h)
			bandwidth, _ := strconv.Atoi(stream["BANDWIDTH"])
			g := stream["AUDIO"]
			stream = nil
			short := min(width, height)
			if !redditRendition.MatchString(line) || short <= 0 || short > 720 || short < best || (short == best && bandwidth <= rate) {
				continue
			}
			// Equal renditions differ only by audio; the higher bandwidth has the richer group.
			best, rate, video, group = short, bandwidth, line, g
		}
	}
	if video == "" {
		return "", "", errMetadata
	}
	video = base + strings.TrimSuffix(video, ".m3u8") + ".mp4"
	if group != "" {
		if uri := groups[group]; uri != "" {
			audio = base + strings.TrimSuffix(uri, ".m3u8") + ".mp4"
		}
	}
	return video, audio, nil
}

func (a *API) redditMux(ctx context.Context, id string) ([]byte, error) {
	video, audio, err := a.redditRenditions(ctx, id)
	if err != nil || audio == "" {
		return nil, errMetadata
	}
	v, err := a.clipBytes(ctx, video, 0, 0, mediaLimit)
	if err != nil {
		return nil, err
	}
	s, err := a.clipBytes(ctx, audio, 0, 0, int64(mediaLimit-len(v)))
	if err != nil {
		return nil, err
	}
	return muxTracks(v, s)
}

// muxTracks joins two single-track fragmented MP4 files into one file with a
// video and an audio track. Samples are copied, never re-encoded.
func muxTracks(files ...[]byte) ([]byte, error) {
	var init fmp4.Init
	part := fmp4.Part{SequenceNumber: 1}
	for i, raw := range files {
		split := firstMoof(raw)
		if split <= 0 {
			return nil, errMetadata
		}
		var in fmp4.Init
		var parts fmp4.Parts
		if in.Unmarshal(bytes.NewReader(raw[:split])) != nil || len(in.Tracks) != 1 || parts.Unmarshal(raw[split:]) != nil || len(parts) == 0 {
			return nil, errMetadata
		}
		t := *in.Tracks[0]
		t.ID = i + 1
		init.Tracks = append(init.Tracks, &t)
		track := &fmp4.PartTrack{ID: i + 1}
		for j, p := range parts {
			if len(p.Tracks) != 1 {
				return nil, errMetadata
			}
			if j == 0 {
				track.BaseTime = p.Tracks[0].BaseTime
			}
			track.Samples = append(track.Samples, p.Tracks[0].Samples...)
		}
		part.Tracks = append(part.Tracks, track)
	}
	var dst seekablebuffer.Buffer
	if init.Marshal(&dst) != nil || part.Marshal(&dst) != nil || dst.Len() > mediaLimit {
		return nil, errMetadata
	}
	return dst.Bytes(), nil
}

// firstMoof walks top-level boxes; everything before the first fragment is init.
func firstMoof(raw []byte) int {
	for at := 0; at+8 <= len(raw); {
		size := int(binary.BigEndian.Uint32(raw[at:]))
		if string(raw[at+4:at+8]) == "moof" {
			return at
		}
		if size < 8 || at+size > len(raw) {
			return -1
		}
		at += size
	}
	return -1
}
