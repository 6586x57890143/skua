package fix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	mp4codecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

const (
	cmafID     = "mm3u5whkp6xh1"
	cmafMaster = `#EXTM3U
#EXT-X-MEDIA:URI="CMAF_AUDIO_64.m3u8",TYPE=AUDIO,GROUP-ID="4",NAME="audio 0"
#EXT-X-MEDIA:URI="CMAF_AUDIO_128.m3u8",TYPE=AUDIO,GROUP-ID="5",NAME="audio 0"
#EXT-X-STREAM-INF:BANDWIDTH=1220733,RESOLUTION=480x846,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="5"
CMAF_480.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=288606,RESOLUTION=220x388,AUDIO="4"
CMAF_220.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1156616,RESOLUTION=480x846,AUDIO="4"
CMAF_480.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=9000000,RESOLUTION=1920x1080,AUDIO="5"
CMAF_1080.m3u8
`
)

// cmafTrack is a single-track fragmented MP4 split over two fragments, like
// Reddit's CMAF files. Payloads are tiny; live canaries verify real media.
func cmafTrack(t *testing.T, codec mp4codecs.Codec) []byte {
	t.Helper()
	var b seekablebuffer.Buffer
	if err := (fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: codec}}}).Marshal(&b); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		samples := []*fmp4.Sample{{Duration: 3000, Payload: []byte{0, 0, 0, 1, 0x65}}, {Duration: 3000, Payload: []byte{0, 0, 0, 1, 0x41}, IsNonSyncSample: true}}
		p := fmp4.Part{SequenceNumber: uint32(i + 1), Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: uint64(i) * 6000, Samples: samples}}}
		if err := p.Marshal(&b); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func cmafFiles(t *testing.T) (video, audio []byte) {
	sps := []byte{0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20}
	video = cmafTrack(t, &mp4codecs.H264{SPS: sps, PPS: []byte{0x68, 0xce, 0x06, 0xe2}})
	audio = cmafTrack(t, &mp4codecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{Type: mpeg4audio.ObjectTypeAACLC, SampleRate: 48000, ChannelConfig: 2}})
	return video, audio
}

func TestRedditRenditions(t *testing.T) {
	master := cmafMaster
	a := apiFor(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://v.redd.it/"+cmafID+"/HLSPlaylist.m3u8" {
			t.Fatal(r.URL)
		}
		return response(master), nil
	})
	v, s, err := a.redditRenditions(context.Background(), cmafID)
	if err != nil || v != "https://v.redd.it/"+cmafID+"/CMAF_480.mp4" || s != "https://v.redd.it/"+cmafID+"/CMAF_AUDIO_128.mp4" {
		t.Fatal(v, s, err)
	}
	master = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=640x360\nCMAF_360.m3u8\n"
	if v, s, err = a.redditRenditions(context.Background(), cmafID); err != nil || !strings.HasSuffix(v, "/CMAF_360.mp4") || s != "" {
		t.Fatal("silent upload", v, s, err)
	}
	for _, bad := range []string{"not a playlist", "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=1920x1080\nCMAF_1080.m3u8\n", "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=640x360\n../../x.m3u8\n"} {
		master = bad
		if _, _, err = a.redditRenditions(context.Background(), cmafID); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, _, err = a.redditRenditions(context.Background(), "../x"); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestMuxTracks(t *testing.T) {
	v, s := cmafFiles(t)
	raw, err := muxTracks(v, s)
	if err != nil || !hasTrack(raw, "vide") || !hasTrack(raw, "soun") {
		t.Fatal(len(raw), err)
	}
	for _, bad := range [][]byte{nil, []byte("junkjunkjunk"), v[:firstMoof(v)]} {
		if _, err := muxTracks(bad, s); err == nil {
			t.Fatal("bad track accepted")
		}
	}
}

func cmafTransport(t *testing.T, video, audio []byte, calls *int) transport {
	return func(r *http.Request) (*http.Response, error) {
		*calls++
		switch {
		case r.URL.Host == "fixembed.app":
			return response(`{"success":true,"platform":"reddit","data":{"title":"A video post","url":"https://www.reddit.com/r/test/comments/abc123/","authorName":"u/steve"}}`), nil
		case r.URL.Host == "embed.reddit.com":
			res := response(`<shreddit-player src="https://v.redd.it/` + cmafID + `/HLSPlaylist.m3u8?f=sd"></shreddit-player>`)
			res.Header.Set("Content-Type", "text/html")
			return res, nil
		case strings.HasSuffix(r.URL.Path, "HLSPlaylist.m3u8"):
			return response(cmafMaster), nil
		case strings.HasSuffix(r.URL.Path, "/CMAF_480.mp4"):
			return response(string(video)), nil
		case strings.HasSuffix(r.URL.Path, "/CMAF_AUDIO_128.mp4"):
			return response(string(audio)), nil
		}
		t.Fatal(r.URL)
		return nil, nil
	}
}

func TestRedditCMAFThroughRelay(t *testing.T) {
	v, s := cmafFiles(t)
	l, _ := Parse("https://www.reddit.com/r/test/comments/abc123/")
	calls := 0
	a := mediaAPI(t, cmafTransport(t, v, s, &calls))
	p, err := a.Resolve(context.Background(), l)
	if err != nil || p.Incomplete || len(p.Media) != 1 || p.Media[0].URL != a.media.link("reddit", cmafID) || p.Context != "" {
		t.Fatalf("%+v %v", p, err)
	}
	before := calls
	w := httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, p.Media[0].URL, nil))
	if w.Code != http.StatusOK || !hasTrack(w.Body.Bytes(), "soun") || calls != before {
		t.Fatal("relay", w.Code, calls-before)
	}
	// Another kind's signature never opens a reddit ID.
	w = httptest.NewRecorder()
	forged := strings.Replace(a.media.link("kick", cmafID), "/kick/", "/reddit/", 1)
	a.MediaHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, forged, nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("cross-kind signature", w.Code)
	}

	// Without the relay, or when muxing fails, the silent video still plays.
	for _, a := range []*API{apiFor(t, cmafTransport(t, v, s, &calls)), mediaAPI(t, cmafTransport(t, v, []byte("bad"), &calls))} {
		p, err = a.Resolve(context.Background(), l)
		if err != nil || p.Incomplete || len(p.Media) != 1 || !strings.HasSuffix(p.Media[0].URL, "/CMAF_480.mp4") || !strings.Contains(p.Context, "silent") {
			t.Fatalf("%+v %v", p, err)
		}
	}
}

func TestRedditVideoIn(t *testing.T) {
	d := &fixData{}
	d.Video = &struct {
		URL       string `json:"url"`
		MediaType string `json:"mediaType"`
	}{URL: "https://v.redd.it/" + cmafID + "/CMAF_720.mp4"}
	if redditVideoIn(d, "") != cmafID {
		t.Fatal("metadata video")
	}
	d.Video.URL = "https://v.redd.it/abc/CMAF_720.mp4"
	if redditVideoIn(d, "") != "" || redditVideoIn(&fixData{}, "https://v.redd.it/x/HLSPlaylist.m3u8") != "" {
		t.Fatal("short id accepted")
	}
}
