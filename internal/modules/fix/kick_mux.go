package fix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	mp4codecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	tscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
)

type timedSample struct {
	dts    int64
	sample *fmp4.Sample
}

// Repackage H.264/AAC without decoding or changing either track's quality.
func remuxClip(ctx context.Context, src io.Reader) ([]byte, error) {
	ts, err := io.ReadAll(io.LimitReader(src, mediaLimit+1))
	if err != nil || len(ts) > mediaLimit {
		return nil, errMetadata
	}
	r := &mpegts.Reader{R: bytes.NewReader(ts)}
	if err := r.Initialize(); err != nil {
		return nil, fmt.Errorf("fix mux initialize: %w", err)
	}
	var video, audio []timedSample
	vc := &mp4codecs.H264{}
	var ac *mp4codecs.MPEG4Audio
	payload := 0
	videoPID := -1
	var onVideo func(pts, dts int64, au [][]byte) error
	for _, track := range r.Tracks() {
		switch codec := track.Codec.(type) {
		case *tscodecs.H264:
			videoPID = int(track.PID)
			onVideo = func(pts, dts int64, au [][]byte) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				for _, n := range au {
					if len(n) > 0 {
						switch n[0] & 31 {
						case 7:
							vc.SPS = append([]byte(nil), n...)
						case 8:
							vc.PPS = append([]byte(nil), n...)
						}
					}
				}
				sync := h264.IsRandomAccess(au)
				if len(video) == 0 && !sync {
					return nil
				}
				data, err := h264.AVCC(au).Marshal()
				if err != nil {
					return err
				}
				payload += len(data)
				if payload > mediaLimit || len(video) > 12000 {
					return errMetadata
				}
				if len(video) > 0 {
					dts = video[0].dts + timestampDelta(dts, video[0].dts)
				}
				video = append(video, timedSample{dts, &fmp4.Sample{PTSOffset: int32(timestampDelta(pts, dts)), IsNonSyncSample: !sync, Payload: data}})
				return nil
			}
		case *tscodecs.MPEG4Audio:
			ac = &mp4codecs.MPEG4Audio{Config: codec.Config}
			r.OnDataMPEG4Audio(track, func(pts int64, aus [][]byte) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if ac.Config.SampleRate <= 0 {
					return errMetadata
				}
				for i, au := range aus {
					payload += len(au)
					if payload > mediaLimit || len(audio) > 12000 {
						return errMetadata
					}
					audio = append(audio, timedSample{pts + int64(i)*1024*90000/int64(ac.Config.SampleRate), &fmp4.Sample{Payload: append([]byte(nil), au...)}})
				}
				return nil
			})
		default:
			continue // timed metadata tracks are not audio or video
		}
	}
	for {
		err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fix mux read: %w", err)
		}
	}
	if onVideo != nil {
		if err := videoAUs(ts, videoPID, onVideo); err != nil {
			return nil, fmt.Errorf("fix mux video: %w", err)
		}
	}
	if len(video) < 2 || len(audio) < 2 || len(vc.SPS) == 0 || len(vc.PPS) == 0 || ac == nil {
		return nil, fmt.Errorf("fix mux tracks: video=%d audio=%d sps=%d pps=%d", len(video), len(audio), len(vc.SPS), len(vc.PPS))
	}
	origin := video[0].dts
	for i := range audio {
		audio[i].dts = origin + timestampDelta(audio[i].dts, origin)
	}
	for len(audio) > 0 && audio[0].dts < origin {
		audio = audio[1:]
	}
	if len(audio) < 2 {
		return nil, errMetadata
	}
	tracks := make([]*fmp4.PartTrack, 0, 2)
	for i, samples := range [][]timedSample{video, audio} {
		pt := &fmp4.PartTrack{ID: i + 1, BaseTime: uint64(samples[0].dts - origin)}
		for j, s := range samples {
			next := int64(0)
			if j+1 < len(samples) {
				next = samples[j+1].dts - s.dts
			} else {
				next = s.dts - samples[j-1].dts
			}
			if next <= 0 || next > 90000 || s.dts-origin > 181*90000 || s.dts < origin {
				return nil, fmt.Errorf("fix mux timing: track=%d next=%d dts=%d origin=%d", i, next, s.dts, origin)
			}
			s.sample.Duration = uint32(next)
			pt.Samples = append(pt.Samples, s.sample)
		}
		tracks = append(tracks, pt)
	}
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: vc}, {ID: 2, TimeScale: 90000, Codec: ac}}}
	var dst seekablebuffer.Buffer
	if err := init.Marshal(&dst); err != nil {
		return nil, errMetadata
	}
	part := fmp4.Part{SequenceNumber: 1, Tracks: tracks}
	if err := part.Marshal(&dst); err != nil || dst.Len() > mediaLimit {
		return nil, errMetadata
	}
	return dst.Bytes(), nil
}

// videoAUs reads the H.264 track as one continuous stream, like ffmpeg does.
// Kick's muxer spills the last bytes of a frame into the next PES, ahead of
// its first start code. mediacommon drops such a PES whole ("initial delimiter
// not found") and leaves the frame before it short, so every frame that
// references either one smears. Here those bytes go back to their frame.
func videoAUs(ts []byte, pid int, emit func(pts, dts int64, au [][]byte) error) error {
	type pes struct {
		off      int
		pts, dts int64
	}
	var es []byte
	var frames []pes
	for i := 0; i+188 <= len(ts); i += 188 {
		p := ts[i : i+188]
		if p[0] != 0x47 || int(p[1]&0x1f)<<8|int(p[2]) != pid || p[3]&0x10 == 0 {
			continue
		}
		off := 4
		if p[3]&0x20 != 0 {
			off += 1 + int(p[4])
		}
		if off >= len(p) {
			continue
		}
		data := p[off:]
		if p[1]&0x40 == 0 {
			if len(frames) > 0 {
				es = append(es, data...)
			}
			continue
		}
		if len(data) < 14 || data[0] != 0 || data[1] != 0 || data[2] != 1 || data[7]&0x80 == 0 || 9+int(data[8]) > len(data) {
			return errMetadata
		}
		f := pes{off: len(es), pts: pesTime(data[9:14])}
		f.dts = f.pts
		if data[7]&0x40 != 0 && len(data) >= 19 {
			f.dts = pesTime(data[14:19])
		}
		frames = append(frames, f)
		es = append(es, data[9+int(data[8]):]...)
	}
	// A frame starts at the first start code at or after its PES; anything
	// before that is the previous frame's tail (or stuffing it ignores).
	start := func(from int) int {
		j := bytes.Index(es[from:], []byte{0, 0, 1})
		if j < 0 {
			return len(es)
		}
		if j > 0 && es[from+j-1] == 0 {
			j--
		}
		return from + j
	}
	next := 0
	if len(frames) > 0 {
		next = start(frames[0].off)
	}
	for i, f := range frames {
		begin, end := next, len(es)
		if i+1 < len(frames) {
			end = start(frames[i+1].off)
		}
		next = end
		var au h264.AnnexB
		if begin >= end || au.Unmarshal(es[begin:end]) != nil {
			continue // a PES with no NAL unit of its own is skipped, as before
		}
		if len(au[0]) > 0 && h264.NALUType(au[0][0]&31) == h264.NALUTypeAccessUnitDelimiter {
			au = au[1:]
		}
		if len(au) == 0 {
			continue
		}
		if err := emit(f.pts, f.dts, au); err != nil {
			return err
		}
	}
	return nil
}

func pesTime(b []byte) int64 {
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

// PES timestamps use a 33-bit clock; a clip can cross the wrap boundary.
func timestampDelta(value, origin int64) int64 {
	const half = int64(1) << 32
	return ((value - origin + half) & ((int64(1) << 33) - 1)) - half
}
