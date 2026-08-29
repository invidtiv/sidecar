package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
)

const sampleRate = 22050

type note struct {
	frequency float64
	duration  float64
	amplitude float64
}

func main() {
	cues := map[string][]note{
		"attention.wav": {{frequency: 587.33, duration: .10, amplitude: .18}, {frequency: 783.99, duration: .16, amplitude: .16}},
		"done.wav":      {{frequency: 523.25, duration: .09, amplitude: .13}, {frequency: 659.25, duration: .13, amplitude: .14}},
		"failure.wav":   {{frequency: 392.00, duration: .12, amplitude: .16}, {frequency: 293.66, duration: .20, amplitude: .17}},
	}
	for name, sequence := range cues {
		data := wav(sequence)
		if err := os.WriteFile(filepath.Join("internal", "notifydelivery", "assets", name), data, 0o644); err != nil {
			panic(err)
		}
	}
}

func wav(sequence []note) []byte {
	var samples []int16
	for noteIndex, n := range sequence {
		count := int(n.duration * sampleRate)
		fade := sampleRate / 40
		for i := 0; i < count; i++ {
			envelope := 1.0
			if i < fade {
				envelope = float64(i) / float64(fade)
			}
			if left := count - i - 1; left < fade {
				envelope = math.Min(envelope, float64(left)/float64(fade))
			}
			value := math.Sin(2*math.Pi*n.frequency*float64(i)/sampleRate) * n.amplitude * envelope
			samples = append(samples, int16(value*32767))
		}
		if noteIndex < len(sequence)-1 {
			samples = append(samples, make([]int16, sampleRate/40)...)
		}
	}
	dataSize := uint32(len(samples) * 2)
	out := make([]byte, 44+dataSize)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], 36+dataSize)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], 1)
	binary.LittleEndian.PutUint32(out[24:28], sampleRate)
	binary.LittleEndian.PutUint32(out[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(out[32:34], 2)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], dataSize)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(out[44+i*2:46+i*2], uint16(sample))
	}
	return out
}
