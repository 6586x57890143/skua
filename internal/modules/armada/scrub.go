// SPDX-License-Identifier: AGPL-3.0-only

package armada

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
)

// scrub takes out what a file says about who made it, where and on what:
// EXIF (GPS, camera, times, thumbnails), XMP, IPTC and comments in JPEG,
// PNG, WebP and GIF, and the user data and metadata boxes of MP4 and
// QuickTime. A JPEG keeps its orientation so a phone photo stays upright.
// The type is read from the bytes, never the sender's word. Any other
// type, or a file that doesn't parse as its own, goes as it came.
func scrub(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return scrubJPEG(b)
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return scrubPNG(b)
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return scrubWebP(b)
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		return scrubGIF(b)
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		out := bytes.Clone(b)
		if !blankBoxes(out, 0, len(out), false) {
			return b
		}
		return out
	}
	return b
}

// randomName is a short name that says nothing about the file or its sender.
func randomName() string {
	return string(bytes.ToLower([]byte(rand.Text()[:8])))
}

func scrubJPEG(b []byte) []byte {
	var kept []byte
	orient := byte(0)
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return b
		}
		mk := b[i+1]
		switch {
		case mk == 0xFF: // fill
			i++
			continue
		case mk == 0x01 || mk >= 0xD0 && mk <= 0xD8:
			kept = append(kept, b[i:i+2]...)
			i += 2
			continue
		case mk == 0xDA:
			// Scan data never holds FF D9 but at the end, so this is EOI.
			// What trails it (a phone's extra images) goes with the rest.
			end := bytes.Index(b[i:], []byte{0xFF, 0xD9})
			if end < 0 {
				return b
			}
			out := []byte{0xFF, 0xD8}
			if orient > 1 {
				out = append(out, 0xFF, 0xE1, 0, 34, 'E', 'x', 'i', 'f', 0, 0,
					'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1,
					0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orient, 0, 0,
					0, 0, 0, 0)
			}
			return append(append(out, kept...), b[i:i+end+2]...)
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return b
		}
		seg, body := b[i:i+2+n], b[i+4:i+2+n]
		switch {
		case mk == 0xE1:
			if o := exifOrientation(body); o != 0 {
				orient = o
			}
		case mk == 0xE2 && bytes.HasPrefix(body, []byte("ICC_PROFILE\x00")), mk == 0xEE: // colour, Adobe transform
			kept = append(kept, seg...)
		case mk >= 0xE0 && mk <= 0xEF, mk == 0xFE: // APPn, COM
		default:
			kept = append(kept, seg...)
		}
		i += 2 + n
	}
	return b
}

// exifOrientation reads tag 0x0112 from an APP1 Exif body's first IFD.
func exifOrientation(b []byte) byte {
	if !bytes.HasPrefix(b, []byte("Exif\x00\x00")) || len(b) < 14 {
		return 0
	}
	t := b[6:]
	var o binary.ByteOrder = binary.BigEndian
	if string(t[:2]) == "II" {
		o = binary.LittleEndian
	}
	ifd := int(o.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	for e, n := ifd+2, int(o.Uint16(t[ifd:])); n > 0 && e+12 <= len(t); e, n = e+12, n-1 {
		if o.Uint16(t[e:]) == 0x0112 {
			if v := o.Uint16(t[e+8:]); v >= 1 && v <= 8 {
				return byte(v)
			}
		}
	}
	return 0
}

func scrubPNG(b []byte) []byte {
	out := append([]byte(nil), b[:8]...)
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if i+12+n > len(b) {
			return b
		}
		switch string(b[i+4 : i+8]) {
		case "tEXt", "zTXt", "iTXt", "eXIf", "tIME":
		default:
			out = append(out, b[i:i+12+n]...)
		}
		if string(b[i+4:i+8]) == "IEND" {
			return out
		}
		i += 12 + n
	}
	return b
}

func scrubWebP(b []byte) []byte {
	out := append([]byte(nil), b[:12]...)
	end := min(len(b), 8+int(binary.LittleEndian.Uint32(b[4:])))
	for i := 12; i+8 <= end; {
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if i+8+n > end {
			return b
		}
		next := min(i+8+n+n&1, end)
		switch string(b[i : i+4]) {
		case "EXIF", "XMP ":
		case "VP8X":
			at := len(out)
			out = append(out, b[i:next]...)
			if n > 0 {
				out[at+8] &^= 0x0C // the EXIF and XMP flags
			}
		default:
			out = append(out, b[i:next]...)
		}
		i = next
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func scrubGIF(b []byte) []byte {
	i := 13
	if len(b) < i {
		return b
	}
	if b[10]&0x80 != 0 {
		i += 3 << (b[10]&7 + 1)
	}
	if i > len(b) {
		return b
	}
	out := append([]byte(nil), b[:i]...)
	for i < len(b) {
		switch b[i] {
		case 0x3B:
			return append(out, 0x3B)
		case 0x2C: // image: descriptor, local colours, LZW size, data
			j := i + 10
			if j > len(b) {
				return b
			}
			if b[i+9]&0x80 != 0 {
				j += 3 << (b[i+9]&7 + 1)
			}
			j = gifBlocks(b, j+1)
			if j < 0 {
				return b
			}
			out, i = append(out, b[i:j]...), j
		case 0x21:
			if i+2 > len(b) {
				return b
			}
			j := gifBlocks(b, i+2)
			if j < 0 {
				return b
			}
			// Keep timing, plain text and the loop count; comments and
			// other applications' blocks (XMP among them) go.
			app := b[i+1] == 0xFF && i+14 <= len(b) &&
				(string(b[i+3:i+14]) == "NETSCAPE2.0" || string(b[i+3:i+14]) == "ANIMEXTS1.0")
			if b[i+1] == 0xF9 || b[i+1] == 0x01 || app {
				out = append(out, b[i:j]...)
			}
			i = j
		default:
			return b
		}
	}
	return b
}

// gifBlocks returns the index past the data sub-blocks starting at i.
func gifBlocks(b []byte, i int) int {
	for i < len(b) {
		n := int(b[i])
		i += 1 + n
		if n == 0 {
			return i
		}
	}
	return -1
}

var xmpUUID = []byte{0xBE, 0x7A, 0xCF, 0xCB, 0x97, 0xA9, 0x42, 0xE8, 0x9C, 0x71, 0x99, 0x94, 0x91, 0xE3, 0xAF, 0xAC}

// blankBoxes turns a movie's metadata boxes, and an XMP box anywhere, into
// zeroed free boxes in place. Nothing moves, so the sample offsets the
// file points at still hold. Top-level meta is left: in HEIF it is the image.
func blankBoxes(b []byte, i, end int, movie bool) bool {
	for i+8 <= end {
		n, hdr := int(binary.BigEndian.Uint32(b[i:])), 8
		switch n {
		case 0:
			n = end - i
		case 1:
			if i+16 > end {
				return false
			}
			big := binary.BigEndian.Uint64(b[i+8:])
			if big > uint64(end-i) {
				return false
			}
			n, hdr = int(big), 16
		}
		if n < hdr || n > end-i {
			return false
		}
		typ, body := string(b[i+4:i+8]), b[i+hdr:i+n]
		switch {
		case movie && (typ == "udta" || typ == "meta"), typ == "uuid" && bytes.HasPrefix(body, xmpUUID):
			copy(b[i+4:], "free")
			clear(body)
		case typ == "moov" || typ == "trak":
			if !blankBoxes(b, i+hdr, i+n, true) {
				return false
			}
		}
		i += n
	}
	return true
}
