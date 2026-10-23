package tray

import (
	"encoding/binary"
	"math"
)

// Chime returns a short "ding-dong" doorbell sound as a WAV file (8-bit
// mono PCM), synthesized so the repository needs no audio file. It is
// about 20 KB.
func Chime() []byte {
	const (
		rate  = 16000
		noteS = 0.6 // length of each note, seconds
	)
	// E5 then C5, each with a quieter octave for a bell-like timbre and an
	// exponential decay.
	notes := []float64{659.25, 523.25}
	n := int(rate * noteS)
	data := make([]byte, 0, n*len(notes))
	for _, f := range notes {
		for i := range n {
			t := float64(i) / rate
			v := math.Sin(2*math.Pi*f*t) + 0.35*math.Sin(4*math.Pi*f*t)
			v *= math.Exp(-4*t) * 0.6
			if i < 80 { // a short fade-in avoids a click
				v *= float64(i) / 80
			}
			data = append(data, byte(128+int(math.Round(v*127/1.35))))
		}
	}

	wav := make([]byte, 44, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+len(data)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)   // fmt chunk size
	binary.LittleEndian.PutUint16(wav[20:], 1)    // PCM
	binary.LittleEndian.PutUint16(wav[22:], 1)    // mono
	binary.LittleEndian.PutUint32(wav[24:], rate) // sample rate
	binary.LittleEndian.PutUint32(wav[28:], rate) // bytes per second
	binary.LittleEndian.PutUint16(wav[32:], 1)    // block align
	binary.LittleEndian.PutUint16(wav[34:], 8)    // bits per sample
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	return append(wav, data...)
}
