// Package tray shows a small icon in the notification area whose colour tells
// the state of the copies. It is optional and off by default: the icon is a
// process that stays, which the rest of the application avoids. The menu opens
// the browser page, so the icon holds no logic of its own.
package tray

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"time"
)

// Level is the state the icon shows.
type Level string

// The levels, from good to bad. Unknown is shown while the state is being read
// and when it could not be read.
const (
	LevelOK        Level = "ok"
	LevelAttention Level = "attention"
	LevelProblem   Level = "problem"
	LevelUnknown   Level = "unknown"
)

// State is what the icon shows: a colour and a line for the tooltip and the
// first item of the menu.
type State struct {
	Level Level
	Text  string
}

// Labels are the texts of the menu, already translated.
type Labels struct {
	Title, Open, Refresh, Quit, Checking string
}

// Options configure Run.
type Options struct {
	// Source reads the state. It may take a while.
	Source func(ctx context.Context) State
	// Open shows the page.
	Open func() error
	// Interval is how often the state is read again.
	Interval time.Duration
	Labels   Labels
	// Instance names the single running copy; a sandbox gets its own.
	Instance string
}

// DefaultInterval is how often the state is read: the copies change once an
// hour at most.
const DefaultInterval = 10 * time.Minute

var palette = map[Level]color.NRGBA{
	LevelOK:        {R: 0x2e, G: 0x9e, B: 0x5b, A: 0xff},
	LevelAttention: {R: 0xe0, G: 0xa1, B: 0x00, A: 0xff},
	LevelProblem:   {R: 0xd6, G: 0x45, B: 0x45, A: 0xff},
	LevelUnknown:   {R: 0x8a, G: 0x8f, B: 0x98, A: 0xff},
}

// Colour returns the colour of a level.
func Colour(level Level) color.NRGBA {
	if c, ok := palette[level]; ok {
		return c
	}
	return palette[LevelUnknown]
}

// Icon draws the icon of a level as an .ico file with three sizes, each a PNG:
// a coloured disc with a white sign, a tick, an exclamation mark, a cross or a
// dot. The colour alone is not enough for people who tell colours apart badly.
func Icon(level Level) []byte {
	sizes := []int{16, 32, 48}
	images := make([][]byte, len(sizes))
	for i, size := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, drawLevel(level, size)); err != nil {
			return nil
		}
		images[i] = buf.Bytes()
	}
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, size := range sizes {
		out.Write([]byte{byte(size), byte(size), 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, []uint16{1, 32})
		_ = binary.Write(&out, binary.LittleEndian, []uint32{uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, data := range images {
		out.Write(data)
	}
	return out.Bytes()
}

func drawLevel(level Level, size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	fill := Colour(level)
	centre := s / 2
	radius := s/2 - 0.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-centre, float64(y)+0.5-centre)
			if a := clamp(radius - d + 0.5); a > 0 {
				c := fill
				c.A = uint8(a * 255)
				img.SetNRGBA(x, y, c)
			}
		}
	}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	width := math.Max(1.6, s/9)
	line := func(x0, y0, x1, y1 float64) { stroke(img, x0*s, y0*s, x1*s, y1*s, width, white) }
	switch level {
	case LevelOK:
		line(0.27, 0.53, 0.43, 0.68)
		line(0.43, 0.68, 0.74, 0.34)
	case LevelAttention:
		line(0.5, 0.26, 0.5, 0.58)
		stroke(img, 0.5*s, 0.76*s, 0.5*s, 0.76*s, width*1.1, white)
	case LevelProblem:
		line(0.32, 0.32, 0.68, 0.68)
		line(0.68, 0.32, 0.32, 0.68)
	default:
		stroke(img, 0.5*s, 0.5*s, 0.5*s, 0.5*s, width*1.3, white)
	}
	return img
}

// stroke paints a line with round ends, anti-aliased by its distance.
func stroke(img *image.NRGBA, x0, y0, x1, y1, width float64, c color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := distance(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1)
			a := clamp(width/2 - d + 0.5)
			if a <= 0 {
				continue
			}
			under := img.NRGBAAt(x, y)
			img.SetNRGBA(x, y, color.NRGBA{
				R: mix(under.R, c.R, a), G: mix(under.G, c.G, a), B: mix(under.B, c.B, a),
				A: uint8(math.Max(float64(under.A), a*255)),
			})
		}
	}
}

func mix(under, over uint8, alpha float64) uint8 {
	return uint8(float64(under)*(1-alpha) + float64(over)*alpha)
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// distance is how far the point is from the segment.
func distance(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	length := dx*dx + dy*dy
	if length == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := math.Max(0, math.Min(1, ((px-x0)*dx+(py-y0)*dy)/length))
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}
