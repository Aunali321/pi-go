package tools

import (
	"bytes"
	"encoding/base64"
)

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType sniffs image formats the read tool can attach:
// jpeg, png (still), gif, webp and bmp. Returns "" for anything else.
func DetectSupportedImageMimeType(buffer []byte) string {
	if bytes.HasPrefix(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return "" // JPEG-LS
		}
		return "image/jpeg"
	}
	if bytes.HasPrefix(buffer, pngSignature) {
		if isPNG(buffer) && !isAnimatedPNG(buffer) {
			return "image/png"
		}
		return ""
	}
	if bytes.HasPrefix(buffer, []byte("GIF")) {
		return "image/gif"
	}
	if bytes.HasPrefix(buffer, []byte("RIFF")) && asciiAt(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if bytes.HasPrefix(buffer, []byte("BM")) && isBMP(buffer) {
		return "image/bmp"
	}
	return ""
}

// EncodeBase64 encodes bytes as standard base64.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func asciiAt(buffer []byte, offset int, text string) bool {
	if len(buffer) < offset+len(text) {
		return false
	}
	return string(buffer[offset:offset+len(text)]) == text
}

func readUint32BE(buffer []byte, offset int) int {
	if len(buffer) < offset+4 {
		return 0
	}
	return int(buffer[offset])<<24 | int(buffer[offset+1])<<16 | int(buffer[offset+2])<<8 | int(buffer[offset+3])
}

func readUint16LE(buffer []byte, offset int) int {
	if len(buffer) < offset+2 {
		return 0
	}
	return int(buffer[offset]) | int(buffer[offset+1])<<8
}

func readUint32LE(buffer []byte, offset int) int {
	if len(buffer) < offset+4 {
		return 0
	}
	return int(buffer[offset]) | int(buffer[offset+1])<<8 | int(buffer[offset+2])<<16 | int(buffer[offset+3])<<24
}

func isPNG(buffer []byte) bool {
	return len(buffer) >= 16 && readUint32BE(buffer, len(pngSignature)) == 13 && asciiAt(buffer, 12, "IHDR")
}

func isAnimatedPNG(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := readUint32BE(buffer, offset)
		chunkTypeOffset := offset + 4
		if asciiAt(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if asciiAt(buffer, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + chunkLength + 4
		if nextOffset <= offset || nextOffset > len(buffer) {
			return false
		}
		offset = nextOffset
	}
	return false
}

func isBMP(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}
	declaredFileSize := readUint32LE(buffer, 2)
	pixelDataOffset := readUint32LE(buffer, 10)
	dibHeaderSize := readUint32LE(buffer, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}

	var colorPlanes, bitsPerPixel int
	switch {
	case dibHeaderSize == 12:
		colorPlanes = readUint16LE(buffer, 22)
		bitsPerPixel = readUint16LE(buffer, 24)
	case dibHeaderSize >= 40 && dibHeaderSize <= 124:
		if len(buffer) < 30 {
			return false
		}
		colorPlanes = readUint16LE(buffer, 26)
		bitsPerPixel = readUint16LE(buffer, 28)
	default:
		return false
	}
	switch bitsPerPixel {
	case 1, 4, 8, 16, 24, 32:
		return colorPlanes == 1
	}
	return false
}
