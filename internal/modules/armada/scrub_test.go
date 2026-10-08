// SPDX-License-Identifier: AGPL-3.0-only

package armada

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

const secret = "51.5007N 0.1246W canon eos"

func seg(marker byte, body string) []byte {
	return append([]byte{0xFF, marker, byte((len(body) + 2) >> 8), byte(len(body) + 2)}, body...)
}

func chunk(typ, data string) []byte {
	c := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	c = append(c, typ+data...)
	return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE([]byte(typ+data)))
}

func clean(t *testing.T, name string, got []byte) {
	t.Helper()
	if bytes.Contains(got, []byte(secret)) {
		t.Errorf("%s kept its metadata", name)
	}
}

func TestScrubImages(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.RGBA{255, 0, 0, 255}})

	var j bytes.Buffer
	_ = jpeg.Encode(&j, img, nil)
	// A little-endian EXIF with orientation 6 and a GPS-ish string, a
	// comment, XMP, and a trailing image after EOI.
	exif := "Exif\x00\x00II*\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00" + secret
	in := append([]byte{0xFF, 0xD8}, seg(0xE1, exif)...)
	in = append(in, seg(0xFE, secret)...)
	in = append(in, seg(0xE1, "http://ns.adobe.com/xap/1.0/\x00"+secret)...)
	in = append(in, seg(0xE2, "ICC_PROFILE\x00\x01\x01icc")...)
	in = append(append(in, j.Bytes()[2:]...), "\xFF\xD8"+secret+"\xFF\xD9"...)
	got := scrub(in)
	clean(t, "jpeg", got)
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Errorf("jpeg: %v", err)
	}
	if exifOrientation(got[6:38]) != 6 || !bytes.Contains(got, []byte("ICC_PROFILE")) {
		t.Errorf("jpeg lost its orientation or colour: % x", got[:40])
	}
	if exifOrientation([]byte("Exif\x00\x00MM\x00*\x00\x00\x00\x02")) != 0 || exifOrientation([]byte("nope")) != 0 {
		t.Error("orientation from nothing")
	}

	var p bytes.Buffer
	_ = png.Encode(&p, img)
	pb := p.Bytes()
	in = append(append([]byte(nil), pb[:33]...), chunk("tEXt", "Comment\x00"+secret)...)
	in = append(append(in, chunk("eXIf", secret)...), pb[33:]...)
	got = scrub(append(in, secret...))
	clean(t, "png", got)
	if _, err := png.Decode(bytes.NewReader(got)); err != nil {
		t.Errorf("png: %v", err)
	}

	var g bytes.Buffer
	_ = gif.EncodeAll(&g, &gif.GIF{Image: []*image.Paletted{img, img}, Delay: []int{5, 5}})
	gb := g.Bytes()
	at := bytes.IndexByte(gb[13+6:], 0x21) + 13 + 6 // after the global colours
	ins := append([]byte{0x21, 0xFE, byte(len(secret))}, secret+"\x00"...)
	ins = append(ins, "\x21\xFF\x0bXMP DataXMP"+string(rune(len(secret)))+secret+"\x00"...)
	in = append(append(append([]byte(nil), gb[:at]...), ins...), gb[at:]...)
	got = scrub(in)
	clean(t, "gif", got)
	if a, err := gif.DecodeAll(bytes.NewReader(got)); err != nil || len(a.Image) != 2 || a.LoopCount != 0 {
		t.Errorf("gif: %v", err)
	}

	vp8x := "VP8X\x0a\x00\x00\x00\x0c\x00\x00\x00\x03\x00\x00\x03\x00\x00"
	body := "WEBP" + vp8x + "VP8L\x02\x00\x00\x00ab" + "EXIF\x1a\x00\x00\x00" + secret + "XMP \x1a\x00\x00\x00" + secret
	in = binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
	got = scrub(append(in, body...))
	clean(t, "webp", got)
	if int(binary.LittleEndian.Uint32(got[4:])) != len(got)-8 || got[20]&0x0C != 0 || !bytes.Contains(got, []byte("VP8L\x02\x00\x00\x00ab")) {
		t.Errorf("webp % x", got)
	}

	// Broken files go as they came.
	for _, b := range [][]byte{
		[]byte("\xFF\xD8\xFF\xE1\x00"), []byte("\xFF\xD8\xFF\xE1\x00\x01"), append([]byte("\xFF\xD8"), seg(0xC0, "x")[:4]...),
		[]byte("\x89PNG\r\n\x1a\n\x00\x00\xff\xff"), []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00"), []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x07"),
		[]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x2c\x00"), []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x21\xFE\x05"), []byte("GIF89a\x01\x00\x01"),
		[]byte("RIFF\x10\x00\x00\x00WEBPVP8 \xff\x00\x00\x00"), []byte("just text"),
	} {
		if !bytes.Equal(scrub(b), b) {
			t.Errorf("changed %q", b)
		}
	}
}

func box(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	return append(binary.BigEndian.AppendUint32(nil, uint32(8+len(b))), append([]byte(typ), b...)...)
}

func TestScrubMovie(t *testing.T) {
	mdat := box("mdat", []byte("samples"))
	xmp := box("uuid", xmpUUID, []byte(secret))
	moov := box("moov",
		box("mvhd", []byte("hdr")),
		box("udta", box("\xa9xyz", []byte(secret))),
		box("meta", []byte(secret)),
		box("trak", box("tkhd", []byte("t")), box("udta", []byte(secret))),
	)
	heif := box("meta", []byte("the picture"))
	in := bytes.Join([][]byte{box("ftyp", []byte("qt  ")), moov, xmp, heif, mdat}, nil)
	got := scrub(in)
	clean(t, "mov", got)
	if len(got) != len(in) || !bytes.Contains(got, []byte("samples")) || !bytes.Contains(got, []byte("the picture")) || !bytes.Contains(got, []byte("mvhd")) {
		t.Errorf("mov %q", got)
	}
	// A 64-bit size, and one past the end.
	big := append([]byte("\x00\x00\x00\x01udta"), binary.BigEndian.AppendUint64(nil, 16+uint64(len(secret)))...)
	in = bytes.Join([][]byte{box("ftyp"), box("moov", append(big, secret...))}, nil)
	clean(t, "mov64", scrub(in))
	for _, b := range [][]byte{
		append(box("ftyp"), "\x00\x00\x00\x01moov\x00\x00"...),
		append(box("ftyp"), "\x00\x00\x00\x01moov\xff\x00\x00\x00\x00\x00\x00\x00"...),
		append(box("ftyp"), "\x00\x00\x00\x04moov"...),
		append(box("ftyp"), box("moov", []byte("\x00\x00\x00\xffudta"))...),
	} {
		if !bytes.Equal(scrub(b), b) {
			t.Errorf("changed %q", b)
		}
	}
	if got := scrub(append(box("ftyp"), "\x00\x00\x00\x00free"...)); len(got) != 16 {
		t.Errorf("to the end %q", got)
	}
}
