package ali

import (
	"encoding/binary"
	"errors"
)

const ttsMaxWAVHeaderBytes = 64 << 10

// Only the WAV header is buffered; raw PCM and subsequent samples pass through.
type ttsPCMStream struct {
	header    []byte
	offset    int
	wav       bool
	hasFormat bool
	ready     bool
}

func (s *ttsPCMStream) fill(data *[]byte, size int) bool {
	if len(s.header) >= size {
		return true
	}
	n := min(size-len(s.header), len(*data))
	s.header = append(s.header, (*data)[:n]...)
	*data = (*data)[n:]
	return len(s.header) >= size
}

func (s *ttsPCMStream) push(data []byte) ([]byte, error) {
	if s.ready {
		return data, nil
	}
	if !s.wav {
		if len(s.header) == 0 && len(data) >= 4 && string(data[:4]) != "RIFF" {
			s.ready = true
			return data, nil
		}
		if !s.fill(&data, 4) {
			return nil, nil
		}
		if string(s.header[:4]) != "RIFF" {
			pcm := append(s.header, data...)
			s.header = nil
			s.ready = true
			return pcm, nil
		}
		if !s.fill(&data, 12) {
			return nil, nil
		}
		if string(s.header[8:12]) != "WAVE" {
			return nil, errors.New("Qwen TTS RIFF audio is not WAV")
		}
		s.wav = true
		s.offset = 12
	}
	for {
		if s.offset+8 > ttsMaxWAVHeaderBytes {
			return nil, errors.New("Qwen TTS WAV header exceeds the size limit")
		}
		if !s.fill(&data, s.offset+8) {
			return nil, nil
		}
		id := string(s.header[s.offset : s.offset+4])
		size := int64(binary.LittleEndian.Uint32(s.header[s.offset+4 : s.offset+8]))
		if id == "data" {
			if !s.hasFormat {
				return nil, errors.New("Qwen TTS WAV contains no PCM format header")
			}
			// Streaming WAV sizes may be placeholders: rely on SSE completion instead.
			s.header = nil
			s.ready = true
			return data, nil
		}
		end := int64(s.offset+8) + size + size%2
		if end > ttsMaxWAVHeaderBytes {
			return nil, errors.New("Qwen TTS WAV header exceeds the size limit")
		}
		if !s.fill(&data, int(end)) {
			return nil, nil
		}
		if id == "fmt " {
			if size < 16 {
				return nil, errors.New("Qwen TTS WAV format header is too short")
			}
			format := s.header[s.offset+8:]
			if binary.LittleEndian.Uint16(format[:2]) != 1 ||
				binary.LittleEndian.Uint16(format[2:4]) != 1 ||
				binary.LittleEndian.Uint32(format[4:8]) != 24000 ||
				binary.LittleEndian.Uint16(format[14:16]) != 16 {
				return nil, errors.New("Qwen TTS WAV must contain 24 kHz 16-bit mono PCM")
			}
			s.hasFormat = true
		}
		s.offset = int(end)
	}
}

func (s *ttsPCMStream) finish() ([]byte, error) {
	if s.ready {
		return nil, nil
	}
	if len(s.header) >= 4 && string(s.header[:4]) == "RIFF" {
		return nil, errors.New("Qwen TTS stream ended with an incomplete WAV header")
	}
	pcm := s.header
	s.header = nil
	s.ready = true
	return pcm, nil
}
