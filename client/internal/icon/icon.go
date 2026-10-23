// Package icon draws the KnockBlock icon, a door with a status light, at
// any size and in any light colour, and encodes it as a Windows .ico. The
// app icon is generated from it at build time (cmd/genicon), and the tray
// will draw one per status (CLAUDE.md §5.4), so no image files are stored
// in the repository. Standard library only.
package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
)

// Colours of the app icon. LightBusy is the "do not knock" red of the
// firmware's `call` status (firmware/common/board.yaml).
var (
	Background = color.NRGBA{0x26, 0x30, 0x42, 0xff}
	Door       = color.NRGBA{0xee, 0xf1, 0xf5, 0xff}
	LightBusy  = color.NRGBA{0xe0, 0x00, 0x00, 0xff}
)

// AppSizes are the sizes stored in the app .ico, covering Explorer, the
// taskbar, the Start menu and high-DPI displays.
var AppSizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

// supersample is the number of samples per pixel along each axis.
const supersample = 4

// Render draws the icon at size×size pixels with the given light colour.
// Shapes are defined on a unit square and anti-aliased by supersampling.
func Render(size int, light color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	n := float64(supersample * supersample)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/supersample) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/supersample) / float64(size)
					if c, ok := sample(x, y, light); ok {
						// Premultiply so edges blend correctly.
						fa := float64(c.A) / 255
						r += float64(c.R) * fa
						g += float64(c.G) * fa
						b += float64(c.B) * fa
						a += fa
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(r / a)),
				G: uint8(math.Round(g / a)),
				B: uint8(math.Round(b / a)),
				A: uint8(math.Round(a / n * 255)),
			})
		}
	}
	return img
}

// sample returns the colour at (x, y) in the unit square, or false where
// the icon is transparent. Later shapes are drawn on top.
func sample(x, y float64, light color.NRGBA) (color.NRGBA, bool) {
	// The badge sits over the door's top-right corner, cut out of it by a
	// ring in the background colour.
	const (
		badgeX, badgeY, badgeR = 0.70, 0.30, 0.17
		ring                   = 0.055
	)
	inBadge := dist(x, y, badgeX, badgeY) <= badgeR
	inRing := dist(x, y, badgeX, badgeY) <= badgeR+ring
	switch {
	case !inRoundedRect(x, y, 0, 0, 1, 1, 0.22):
		return color.NRGBA{}, false
	case inBadge:
		return light, true
	case inRing:
		return Background, true
	case dist(x, y, 0.53, 0.58) <= 0.035: // door knob
		return Background, true
	case inRoundedRect(x, y, 0.25, 0.20, 0.62, 0.84, 0.05): // door
		return Door, true
	default:
		return Background, true
	}
}

func dist(x, y, cx, cy float64) float64 { return math.Hypot(x-cx, y-cy) }

// inRoundedRect reports whether (x, y) is inside the rectangle
// [x0,x1]×[y0,y1] with corner radius r.
func inRoundedRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return dist(x, y, cx, cy) <= r
}

// EncodeICO writes images as a .ico file with PNG-compressed entries, which
// Windows supports since Vista. Each image must be square and at most
// 256×256.
func EncodeICO(w io.Writer, images []*image.NRGBA) error {
	pngs := make([][]byte, len(images))
	for i, img := range images {
		b := img.Bounds()
		if b.Dx() != b.Dy() || b.Dx() < 1 || b.Dx() > 256 {
			return fmt.Errorf("icon: image %d is %dx%d; want square, 1 to 256 pixels", i, b.Dx(), b.Dy())
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return fmt.Errorf("icon: encode %dx%d: %w", b.Dx(), b.Dy(), err)
		}
		pngs[i] = buf.Bytes()
	}

	// ICONDIR, then one ICONDIRENTRY per image, then the image data.
	var out bytes.Buffer
	header := struct{ Reserved, Type, Count uint16 }{0, 1, uint16(len(images))}
	binary.Write(&out, binary.LittleEndian, header)
	offset := uint32(6 + 16*len(images))
	for i, img := range images {
		side := img.Bounds().Dx()
		dim := uint8(side) // 0 means 256
		if side == 256 {
			dim = 0
		}
		entry := struct {
			Width, Height, Colors, Reserved uint8
			Planes, BitCount                uint16
			Size, Offset                    uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(pngs[i])), offset}
		binary.Write(&out, binary.LittleEndian, entry)
		offset += uint32(len(pngs[i]))
	}
	for _, p := range pngs {
		out.Write(p)
	}
	_, err := w.Write(out.Bytes())
	return err
}

// AppICO returns the app icon as .ico data, in all AppSizes.
func AppICO() ([]byte, error) {
	images := make([]*image.NRGBA, len(AppSizes))
	for i, size := range AppSizes {
		images[i] = Render(size, LightBusy)
	}
	var buf bytes.Buffer
	if err := EncodeICO(&buf, images); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
