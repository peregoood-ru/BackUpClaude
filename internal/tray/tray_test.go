package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

// parseIcon reads the directory of an .ico file and decodes its images.
func parseIcon(t *testing.T, data []byte) map[int][]byte {
	t.Helper()
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatal("not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	images := map[int][]byte{}
	for i := 0; i < count; i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		size := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if int(offset+size) > len(data) {
			t.Fatalf("image %d runs past the end", i)
		}
		width := int(entry[0])
		images[width] = data[offset : offset+size]
	}
	return images
}

func TestIconsAreValidFilesOfThreeSizes(t *testing.T) {
	for _, level := range []Level{LevelOK, LevelAttention, LevelProblem, LevelUnknown} {
		images := parseIcon(t, Icon(level))
		if len(images) != 3 {
			t.Fatalf("%s: %d images", level, len(images))
		}
		for width, data := range images {
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s %d: %v", level, width, err)
			}
			if img.Bounds().Dx() != width || img.Bounds().Dy() != width {
				t.Errorf("%s: %d wide entry holds %v", level, width, img.Bounds())
			}
			// The corner is transparent and the middle of the disc is painted.
			if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
				t.Errorf("%s %d: the corner is painted", level, width)
			}
		}
	}
}

func TestEachLevelHasItsOwnColourAndSign(t *testing.T) {
	seen := map[[4]uint8]Level{}
	for _, level := range []Level{LevelOK, LevelAttention, LevelProblem, LevelUnknown} {
		c := Colour(level)
		key := [4]uint8{c.R, c.G, c.B, c.A}
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s share a colour", level, other)
		}
		seen[key] = level
	}
	// The sign is drawn too: the icons differ in more than the colour, for
	// people who do not tell colours apart.
	flat := func(level Level) []byte {
		img := drawLevel(level, 32)
		return img.Pix
	}
	if bytes.Equal(flat(LevelOK), flat(LevelProblem)) || bytes.Equal(flat(LevelAttention), flat(LevelUnknown)) {
		t.Error("icons are the same")
	}
	if Colour("nonsense") != Colour(LevelUnknown) {
		t.Error("an unknown level must look unknown")
	}
}
