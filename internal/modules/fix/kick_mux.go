package fix

import (
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
	r := &mpegts.Reader{R: src}
	if err := r.Initialize(); err != nil {
		return nil, fmt.Errorf("fix mux initialize: %w", err)
	}
	var video, audio []timedSample
	vc := &mp4codecs.H264{}
	var ac *mp4codecs.MPEG4Audio
	payload := 0
	for _, track := range r.Tracks() {
		switch codec := track.Codec.(type) {
		case *tscodecs.H264:
			r.OnDataH264(track, func(pts, dts int64, au [][]byte) error {
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
			})
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

// PES timestamps use a 33-bit clock; a clip can cross the wrap boundary.
func timestampDelta(value, origin int64) int64 {
	const half = int64(1) << 32
	return ((value - origin + half) & ((int64(1) << 33) - 1)) - half
}
