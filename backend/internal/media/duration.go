package media

import (
	"encoding/binary"
	"math"
	"strings"
)

// ContainerDuration reads a duration the container already declares.
// Missing or zero declarations return false. This does not decode samples
// and does not invent a duration from bitrate.
func ContainerDuration(mediaType string, b []byte) (int64, bool) {
	switch strings.ToLower(mediaType) {
	case "audio/wave", "audio/wav", "audio/x-wav":
		return wavDuration(b)
	case "audio/mp4", "video/mp4":
		return mp4Duration(b)
	case "audio/mpeg":
		return id3TLEN(b)
	case "audio/webm", "video/webm":
		return webmDuration(b)
	default:
		return 0, false
	}
}

func wavDuration(b []byte) (int64, bool) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, false
	}
	var byteRate uint32
	var dataSize uint32
	haveFmt, haveData := false, false
	off := 12
	for off+8 <= len(b) {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		off += 8
		if size < 0 || off+size > len(b) {
			break
		}
		chunk := b[off : off+size]
		if id == "fmt " && len(chunk) >= 16 {
			byteRate = binary.LittleEndian.Uint32(chunk[8:12])
			haveFmt = byteRate > 0
		}
		if id == "data" {
			dataSize = uint32(size)
			haveData = true
		}
		off += size
		if size%2 == 1 {
			off++
		}
	}
	if !haveFmt || !haveData || dataSize == 0 {
		return 0, false
	}
	ms := int64(dataSize) * 1000 / int64(byteRate)
	if ms < 0 {
		return 0, false
	}
	return ms, true
}

func mp4Duration(b []byte) (int64, bool) {
	ms, ok := walkBoxes(b, false)
	return ms, ok
}

func walkBoxes(b []byte, inMovie bool) (int64, bool) {
	for off := 0; off+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[off : off+4]))
		typ := string(b[off+4 : off+8])
		header := 8
		if size == 1 {
			if off+16 > len(b) {
				return 0, false
			}
			wide := binary.BigEndian.Uint64(b[off+8 : off+16])
			if wide > uint64(len(b)-off) {
				return 0, false
			}
			size = int(wide)
			header = 16
		}
		if size < header || off+size > len(b) {
			return 0, false
		}
		body := b[off+header : off+size]
		if typ == "moov" {
			if ms, ok := walkBoxes(body, true); ok {
				return ms, true
			}
		} else if typ == "mvhd" && inMovie {
			if ms, ok := mvhdDuration(body); ok {
				return ms, true
			}
		}
		off += size
	}
	return 0, false
}

func mvhdDuration(b []byte) (int64, bool) {
	if len(b) < 20 {
		return 0, false
	}
	version := b[0]
	var timescale, duration uint64
	switch version {
	case 0:
		if len(b) < 20 {
			return 0, false
		}
		timescale = uint64(binary.BigEndian.Uint32(b[12:16]))
		duration = uint64(binary.BigEndian.Uint32(b[16:20]))
	case 1:
		if len(b) < 32 {
			return 0, false
		}
		timescale = uint64(binary.BigEndian.Uint32(b[20:24]))
		duration = binary.BigEndian.Uint64(b[24:32])
	default:
		return 0, false
	}
	if timescale == 0 || duration == 0 {
		return 0, false
	}
	ms := duration * 1000 / timescale
	if ms > math.MaxInt64 {
		return 0, false
	}
	return int64(ms), true
}

func id3TLEN(b []byte) (int64, bool) {
	if len(b) < 10 || string(b[0:3]) != "ID3" {
		return 0, false
	}
	size := synchsafe(b[6:10])
	end := 10 + size
	if end > len(b) {
		end = len(b)
	}
	off := 10
	for off+10 <= end {
		if b[off] == 0 {
			break
		}
		id := string(b[off : off+4])
		frameSize := synchsafe(b[off+4 : off+8])
		off += 10
		if frameSize < 0 || off+frameSize > end {
			break
		}
		frame := b[off : off+frameSize]
		off += frameSize
		if id != "TLEN" || len(frame) < 2 {
			continue
		}
		text := strings.Trim(string(frame[1:]), "\x00 ")
		n, ok := parseMillis(text)
		if ok && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func synchsafe(b []byte) int {
	if len(b) != 4 {
		return 0
	}
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

func parseMillis(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, true
}

const (
	ebmlSegment       = 0x18538067
	ebmlInfo          = 0x1549A966
	ebmlTimecodeScale = 0x2AD7B1
	ebmlDuration      = 0x4489
)

func webmDuration(b []byte) (int64, bool) {
	scale, duration, ok := walkEBML(b, false)
	if !ok || duration <= 0 {
		return 0, false
	}
	if scale == 0 {
		scale = 1000000
	}
	ms := duration * float64(scale) / 1e6
	if ms < 0 || ms > float64(math.MaxInt64) {
		return 0, false
	}
	return int64(ms), true
}

func walkEBML(b []byte, inInfo bool) (scale uint64, duration float64, ok bool) {
	off := 0
	for off < len(b) {
		id, idn, idok := readID(b[off:])
		if !idok {
			return 0, 0, false
		}
		off += idn
		size, sn, sok := readDataSize(b[off:])
		if !sok {
			return 0, 0, false
		}
		off += sn
		if size > uint64(len(b)-off) {
			return 0, 0, false
		}
		body := b[off : off+int(size)]
		off += int(size)
		switch id {
		case ebmlSegment:
			return walkEBML(body, false)
		case ebmlInfo:
			return walkEBML(body, true)
		case ebmlTimecodeScale:
			if inInfo && len(body) > 0 && len(body) <= 8 {
				var n uint64
				for _, by := range body {
					n = n<<8 | uint64(by)
				}
				scale = n
				ok = true
			}
		case ebmlDuration:
			if inInfo && (len(body) == 4 || len(body) == 8) {
				if len(body) == 4 {
					duration = float64(math.Float32frombits(binary.BigEndian.Uint32(body)))
				} else {
					duration = math.Float64frombits(binary.BigEndian.Uint64(body))
				}
				ok = duration > 0
			}
		}
		if duration > 0 && scale > 0 {
			return scale, duration, true
		}
	}
	if duration > 0 {
		return scale, duration, true
	}
	return 0, 0, false
}

func readID(b []byte) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	width := vintWidth(b[0])
	if width == 0 || len(b) < width {
		return 0, 0, false
	}
	var n uint64
	for i := 0; i < width; i++ {
		n = n<<8 | uint64(b[i])
	}
	return n, width, true
}

func readDataSize(b []byte) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	width := vintWidth(b[0])
	if width == 0 || len(b) < width {
		return 0, 0, false
	}
	mask := byte(0x80 >> (width - 1))
	var n uint64 = uint64(b[0] & (mask - 1))
	allOnes := n == uint64(mask-1)
	for i := 1; i < width; i++ {
		n = n<<8 | uint64(b[i])
		if b[i] != 0xff {
			allOnes = false
		}
	}
	if allOnes {
		return 0, 0, false
	}
	return n, width, true
}

func vintWidth(first byte) int {
	for width := 1; width <= 8; width++ {
		mask := byte(0x80 >> (width - 1))
		if first&mask != 0 {
			return width
		}
	}
	return 0
}
