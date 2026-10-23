package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestRender(t *testing.T) {
	for _, size := range []int{16, 32, 256} {
		img := Render(size, LightBusy)
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("size %d: bounds %v", size, b)
		}
		// Corners are transparent (rounded square); the badge centre has the
		// light colour; the door is light.
		if a := img.NRGBAAt(0, 0).A; a != 0 {
			t.Errorf("size %d: corner alpha %d", size, a)
		}
		badge := img.NRGBAAt(int(0.70*float64(size)), int(0.30*float64(size)))
		if badge != LightBusy {
			t.Errorf("size %d: badge %v, want %v", size, badge, LightBusy)
		}
		door := img.NRGBAAt(int(0.35*float64(size)), int(0.75*float64(size)))
		if door != Door {
			t.Errorf("size %d: door %v, want %v", size, door, Door)
		}
	}
	green := color.NRGBA{0x00, 0xb0, 0x50, 0xff}
	if got := Render(64, green).NRGBAAt(45, 19); got != green {
		t.Errorf("light colour not applied: %v", got)
	}
}

func TestAppICO(t *testing.T) {
	data, err := AppICO()
	if err != nil {
		t.Fatal(err)
	}
	var hdr struct{ Reserved, Type, Count uint16 }
	binary.Read(bytes.NewReader(data), binary.LittleEndian, &hdr)
	if hdr.Type != 1 || int(hdr.Count) != len(AppSizes) {
		t.Fatalf("header %+v", hdr)
	}
	for i, want := range AppSizes {
		var e struct {
			Width, Height, Colors, Reserved uint8
			Planes, BitCount                uint16
			Size, Offset                    uint32
		}
		binary.Read(bytes.NewReader(data[6+16*i:]), binary.LittleEndian, &e)
		got := int(e.Width)
		if got == 0 {
			got = 256
		}
		if got != want || e.BitCount != 32 {
			t.Errorf("entry %d: %+v, want %d px", i, e, want)
		}
		img, err := png.Decode(bytes.NewReader(data[e.Offset : e.Offset+e.Size]))
		if err != nil || img.Bounds().Dx() != want {
			t.Errorf("entry %d: PNG %v, %v", i, err, img)
		}
	}
}

func TestEncodeICORejectsBadSizes(t *testing.T) {
	if err := EncodeICO(&bytes.Buffer{}, []*image.NRGBA{Render(300, LightBusy)}); err == nil {
		t.Error("300 px accepted")
	}
}
