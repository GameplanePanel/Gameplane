package themepkg

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// pngSignature is the 8-byte PNG file signature.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// errImageFormat is returned by PreviewInfo for input that is not a PNG or
// WebP image, or that ends before its header is complete.
var errImageFormat = errors.New("not a PNG or WebP image")

// PreviewInfo reads the content type and pixel dimensions of a PNG or WebP
// image from its header only. Pixels are never decoded. Any input that is not
// one of the two formats, or is too short to hold the header it claims,
// returns an error and never panics.
func PreviewInfo(data []byte) (contentType string, width, height int, err error) {
	switch {
	case len(data) >= len(pngSignature) && string(data[:len(pngSignature)]) == string(pngSignature):
		width, height, err = pngSize(data)
		if err != nil {
			return "", 0, 0, err
		}
		return "image/png", width, height, nil
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		width, height, err = webpSize(data)
		if err != nil {
			return "", 0, 0, err
		}
		return "image/webp", width, height, nil
	default:
		return "", 0, 0, errImageFormat
	}
}

// pngSize reads the IHDR width and height. The first chunk must be IHDR with
// the fixed 13-byte length, so the dimensions sit at offsets 16 and 20.
func pngSize(data []byte) (int, int, error) {
	const ihdrEnd = 24 // signature (8) + chunk length (4) + type (4) + width (4) + height (4)
	if len(data) < ihdrEnd {
		return 0, 0, fmt.Errorf("png header truncated: %w", errImageFormat)
	}
	if binary.BigEndian.Uint32(data[8:12]) != 13 || string(data[12:16]) != "IHDR" {
		return 0, 0, fmt.Errorf("png first chunk is not a 13-byte IHDR: %w", errImageFormat)
	}
	w := binary.BigEndian.Uint32(data[16:20])
	h := binary.BigEndian.Uint32(data[20:24])
	return int(w), int(h), nil
}

// webpSize reads the dimensions from the first chunk of a RIFF/WEBP file. The
// three WebP bitstream variants keep them at different offsets: VP8 (lossy),
// VP8L (lossless) and VP8X (extended canvas).
func webpSize(data []byte) (int, int, error) {
	if len(data) < 20 {
		return 0, 0, fmt.Errorf("webp chunk header truncated: %w", errImageFormat)
	}
	switch string(data[12:16]) {
	case "VP8 ":
		// Frame tag (3 bytes), start code 0x9d 0x01 0x2a, then 14-bit LE16 width and height.
		if len(data) < 30 {
			return 0, 0, fmt.Errorf("webp VP8 header truncated: %w", errImageFormat)
		}
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, fmt.Errorf("webp VP8 start code missing: %w", errImageFormat)
		}
		w := binary.LittleEndian.Uint16(data[26:28]) & 0x3fff
		h := binary.LittleEndian.Uint16(data[28:30]) & 0x3fff
		return int(w), int(h), nil
	case "VP8L":
		// Signature byte 0x2f, then width-1 and height-1 as 14-bit fields packed into 32 bits.
		if len(data) < 25 {
			return 0, 0, fmt.Errorf("webp VP8L header truncated: %w", errImageFormat)
		}
		if data[20] != 0x2f {
			return 0, 0, fmt.Errorf("webp VP8L signature missing: %w", errImageFormat)
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		w := int(bits&0x3fff) + 1
		h := int((bits>>14)&0x3fff) + 1
		return w, h, nil
	case "VP8X":
		// Flags (4 bytes), then canvas width-1 and height-1 as LE24 fields.
		if len(data) < 30 {
			return 0, 0, fmt.Errorf("webp VP8X header truncated: %w", errImageFormat)
		}
		w := le24(data[24:27]) + 1
		h := le24(data[27:30]) + 1
		return w, h, nil
	default:
		return 0, 0, fmt.Errorf("webp first chunk %q is not VP8, VP8L or VP8X: %w", data[12:16], errImageFormat)
	}
}

// le24 decodes a little-endian 24-bit unsigned integer.
func le24(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
}
