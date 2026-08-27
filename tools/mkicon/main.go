// Command mkicon renders an SVG into a Windows .ico.
//
// It exists because this machine has no image tooling — no ImageMagick, no
// rsvg — and adding one would put a system dependency in the way of building
// a release. The rasteriser it uses is already an (indirect) dependency of
// the project, pulled in by Fyne, so the icon is drawn by exactly the same
// code that draws it inside the running app.
//
// Usage: go run ./tools/mkicon icon.svg icon.ico
package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

// sizes are the resolutions Windows picks between: the small one for the
// taskbar and lists, the large one for the desktop and the Alt-Tab switcher.
// Shipping only 256 would leave Windows to downscale it badly at 16px, which
// is where a tray icon actually lives.
var sizes = []int{16, 24, 32, 48, 64, 128, 256}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkicon <input.svg> <output.ico>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(svgPath, icoPath string) error {
	var frames [][]byte
	for _, size := range sizes {
		encoded, err := renderPNG(svgPath, size)
		if err != nil {
			return fmt.Errorf("rendering at %dpx: %w", size, err)
		}
		frames = append(frames, encoded)
	}

	out, err := os.Create(icoPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return writeICO(out, frames)
}

// renderPNG rasterises the SVG at one size.
func renderPNG(svgPath string, size int) ([]byte, error) {
	file, err := os.Open(svgPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	icon, err := oksvg.ReadIconStream(file)
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(size), float64(size))

	rgba := image.NewRGBA(image.Rect(0, 0, size, size))
	// Transparent background: an icon with a painted background would show
	// a coloured square on every dark taskbar.
	draw.Draw(rgba, rgba.Bounds(), image.Transparent, image.Point{}, draw.Src)

	scanner := rasterx.NewScannerGV(size, size, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(size, size, scanner), 1)

	var buffer writerTo
	if err := png.Encode(&buffer, rgba); err != nil {
		return nil, err
	}
	return buffer.bytes, nil
}

// writeICO assembles the frames into an .ico.
//
// Every frame is stored as PNG rather than as a BMP: Windows has accepted
// PNG-in-ICO since Vista, and the BMP form needs a hand-built mask that is
// pure opportunity for error.
func writeICO(out *os.File, frames [][]byte) error {
	// ICONDIR: reserved, type 1 (icon), count.
	header := []any{uint16(0), uint16(1), uint16(len(frames))}
	for _, field := range header {
		if err := binary.Write(out, binary.LittleEndian, field); err != nil {
			return err
		}
	}

	// Each directory entry is 16 bytes and they all precede the data.
	offset := 6 + 16*len(frames)
	for i, frame := range frames {
		size := sizes[i]
		// 256 is stored as 0: the field is a single byte.
		dimension := byte(size)
		if size >= 256 {
			dimension = 0
		}
		entry := []any{
			dimension, dimension, // width, height
			byte(0), byte(0), // palette size, reserved
			uint16(1), uint16(32), // colour planes, bits per pixel
			uint32(len(frame)), uint32(offset),
		}
		for _, field := range entry {
			if err := binary.Write(out, binary.LittleEndian, field); err != nil {
				return err
			}
		}
		offset += len(frame)
	}

	for _, frame := range frames {
		if _, err := out.Write(frame); err != nil {
			return err
		}
	}
	return nil
}

// writerTo collects the encoder's output.
type writerTo struct{ bytes []byte }

func (w *writerTo) Write(p []byte) (int, error) {
	w.bytes = append(w.bytes, p...)
	return len(p), nil
}
