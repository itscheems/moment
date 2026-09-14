package devtools

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
)

// showcaseImage describes one picture the site-showcase scenario draws. The
// pictures are flat-colour compositions rather than photographs, so a grid
// can be judged on aspect ratio, cropping, and order: every picture has a
// distinct hue, a shape that shows where its edges are, and a large number
// that says which one it is.
//
// Bytes is the exact encoded size after padding. It is part of the recipe
// counts, so a change to the drawing never moves the catalog totals: a
// picture whose flat encoding outgrows its budget fails the build instead of
// silently changing the numbers help and JSON publish.
type showcaseImage struct {
	Width  int
	Height int
	Hue    int
	Label  int
	Bytes  int64
	Name   string
}

// Picture shapes the showcase uses, named by how a grid treats them.
const (
	showcasePictureBytes = int64(48 << 10)
	showcaseTinyBytes    = int64(8 << 10)
)

func landscape(hue, label int) showcaseImage {
	return showcaseImage{Width: 600, Height: 400, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func portrait(hue, label int) showcaseImage {
	return showcaseImage{Width: 400, Height: 600, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func square(hue, label int) showcaseImage {
	return showcaseImage{Width: 500, Height: 500, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func panorama(hue, label int) showcaseImage {
	return showcaseImage{Width: 900, Height: 300, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func tall(hue, label int) showcaseImage {
	return showcaseImage{Width: 360, Height: 800, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func tiny(hue, label int) showcaseImage {
	return showcaseImage{Width: 160, Height: 120, Hue: hue, Label: label, Bytes: showcaseTinyBytes}
}

func large(hue, label int) showcaseImage {
	return showcaseImage{Width: 1800, Height: 1200, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func favicon(hue int) showcaseImage {
	return showcaseImage{Width: 64, Height: 64, Hue: hue, Bytes: showcaseTinyBytes}
}

func thumbnail(hue, label int) showcaseImage {
	return showcaseImage{Width: 600, Height: 315, Hue: hue, Label: label, Bytes: showcasePictureBytes}
}

func (spec showcaseImage) fileName(index int) string {
	if spec.Name != "" {
		return spec.Name
	}
	return fmt.Sprintf("showcase-%02d.png", index)
}

// showcasePNG draws one picture and pads it to its exact budget with the same
// private ancillary chunk the other recipes use, so the file stays a valid
// PNG that every decoder ignores the padding of.
func showcasePNG(spec showcaseImage, seed int64, index int) ([]byte, error) {
	if spec.Width <= 0 || spec.Height <= 0 {
		return nil, fmt.Errorf("showcase picture %d has no size", index)
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, spec.Width, spec.Height))
	base := hsl(spec.Hue, 0.55, 0.60)
	shade := hsl(spec.Hue, 0.55, 0.46)
	light := hsl((spec.Hue+40)%360, 0.50, 0.80)
	ink := color.NRGBA{R: 0x1f, G: 0x24, B: 0x2b, A: 0xff}
	paper := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	// A diagonal split says where the picture's true edges are once a grid
	// crops it, and a circle on the golden point says how it was scaled.
	for y := 0; y < spec.Height; y++ {
		limit := spec.Width * (spec.Height - y) / (spec.Height + spec.Width)
		fillRow(canvas, y, 0, limit, shade)
		fillRow(canvas, y, limit, spec.Width, base)
	}
	radius := min(spec.Width, spec.Height) / 5
	disc(canvas, spec.Width*62/100, spec.Height*38/100, radius, light)
	// A footer strip is the one place a crop is expected to lose.
	strip := spec.Height / 14
	if strip < 4 {
		strip = 4
	}
	fill(canvas, image.Rect(0, spec.Height-strip, spec.Width, spec.Height), ink)
	if spec.Label > 0 {
		digits := fmt.Sprintf("%d", spec.Label)
		cell := min(spec.Width/(4*len(digits)+2), spec.Height/8)
		if cell < 2 {
			cell = 2
		}
		totalWidth := len(digits)*4*cell - cell
		left := (spec.Width - totalWidth) / 2
		top := (spec.Height - 5*cell) / 2
		for position, digit := range digits {
			drawDigit(canvas, left+position*4*cell, top, cell, int(digit-'0'), paper)
		}
	}
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&encoded, canvas); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|site-showcase|%d|picture|%d", RecipeVersion, seed, index)))
	return padPNG(encoded.Bytes(), spec.Bytes, digest[:], fmt.Sprintf("showcase picture %d", index))
}

// padPNG appends one private ancillary chunk so the file is exactly size
// bytes long. The chunk type is lowercase in both critical positions, which
// tells a decoder it is neither required nor copy-safe, so every reader
// skips it.
func padPNG(base []byte, size int64, streamKey []byte, what string) ([]byte, error) {
	const chunkOverhead = int64(12)
	if size < int64(len(base))+chunkOverhead {
		return nil, fmt.Errorf("%s encodes to %d bytes, above its %d byte budget", what, len(base), size)
	}
	if size > math.MaxInt32 {
		return nil, fmt.Errorf("%s budget %d does not fit one chunk", what, size)
	}
	payloadSize := size - int64(len(base)) - chunkOverhead
	result := make([]byte, 0, size)
	result = append(result, base[:len(base)-12]...)
	payload := make([]byte, payloadSize)
	stream := &hashStream{key: streamKey}
	if _, err := io.ReadFull(stream, payload); err != nil {
		return nil, err
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(payloadSize))
	result = append(result, length[:]...)
	chunkType := []byte("moMe")
	result = append(result, chunkType...)
	result = append(result, payload...)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write(chunkType)
	_, _ = checksum.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], checksum.Sum32())
	result = append(result, sum[:]...)
	result = append(result, base[len(base)-12:]...)
	if int64(len(result)) != size {
		return nil, errors.New("padded PNG length drifted")
	}
	return result, nil
}

func fill(canvas *image.NRGBA, area image.Rectangle, value color.NRGBA) {
	area = area.Intersect(canvas.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		fillRow(canvas, y, area.Min.X, area.Max.X, value)
	}
}

// fillRow paints [x0, x1) of one row by doubling a single pixel, which is
// what makes a large flat picture cheap to draw.
func fillRow(canvas *image.NRGBA, y, x0, x1 int, value color.NRGBA) {
	bounds := canvas.Bounds()
	if y < bounds.Min.Y || y >= bounds.Max.Y {
		return
	}
	x0, x1 = max(x0, bounds.Min.X), min(x1, bounds.Max.X)
	if x0 >= x1 {
		return
	}
	row := canvas.Pix[canvas.PixOffset(x0, y):canvas.PixOffset(x1, y)]
	row[0], row[1], row[2], row[3] = value.R, value.G, value.B, value.A
	for filled := 4; filled < len(row); filled *= 2 {
		copy(row[filled:], row[:filled])
	}
}

func disc(canvas *image.NRGBA, centerX, centerY, radius int, value color.NRGBA) {
	for dy := -radius; dy <= radius; dy++ {
		half := int(math.Sqrt(float64(radius*radius - dy*dy)))
		fillRow(canvas, centerY+dy, centerX-half, centerX+half+1, value)
	}
}

// digitRows is a 3x5 bitmap font, one row per line, most significant bit on
// the left. It exists so a picture can carry its own number without a font
// file, which the binary does not ship.
var digitRows = [10][5]uint8{
	{0b111, 0b101, 0b101, 0b101, 0b111},
	{0b010, 0b110, 0b010, 0b010, 0b111},
	{0b111, 0b001, 0b111, 0b100, 0b111},
	{0b111, 0b001, 0b111, 0b001, 0b111},
	{0b101, 0b101, 0b111, 0b001, 0b001},
	{0b111, 0b100, 0b111, 0b001, 0b111},
	{0b111, 0b100, 0b111, 0b101, 0b111},
	{0b111, 0b001, 0b001, 0b001, 0b001},
	{0b111, 0b101, 0b111, 0b101, 0b111},
	{0b111, 0b101, 0b111, 0b001, 0b111},
}

func drawDigit(canvas *image.NRGBA, left, top, cell, digit int, value color.NRGBA) {
	if digit < 0 || digit > 9 {
		return
	}
	for row, bits := range digitRows[digit] {
		for column := 0; column < 3; column++ {
			if bits&(0b100>>column) == 0 {
				continue
			}
			fill(canvas, image.Rect(left+column*cell, top+row*cell, left+(column+1)*cell, top+(row+1)*cell), value)
		}
	}
}

// hsl converts a hue in degrees with saturation and lightness in [0, 1] to an
// opaque NRGBA colour.
func hsl(hue int, saturation, lightness float64) color.NRGBA {
	h := float64(((hue % 360) + 360) % 360)
	c := (1 - math.Abs(2*lightness-1)) * saturation
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := lightness - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.NRGBA{
		R: uint8(math.Round((r + m) * 255)),
		G: uint8(math.Round((g + m) * 255)),
		B: uint8(math.Round((b + m) * 255)),
		A: 0xff,
	}
}
