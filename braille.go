package main

import (
	"bytes"
)

// BrailleCanvas represents a canvas for drawing with braille characters.
type BrailleCanvas struct {
	Width  int
	Height int
	buffer []bool
}

// NewBrailleCanvas creates a new BrailleCanvas.
func NewBrailleCanvas(width, height int) *BrailleCanvas {
	return &BrailleCanvas{
		Width:  width,
		Height: height,
		buffer: make([]bool, width*height),
	}
}

// Set sets a pixel on the canvas.
func (c *BrailleCanvas) Set(x, y int) {
	if x < 0 || x >= c.Width || y < 0 || y >= c.Height {
		return
	}
	c.buffer[y*c.Width+x] = true
}

// String returns the canvas as a string of braille characters.
func (c *BrailleCanvas) String() string {
	var buf bytes.Buffer

	for y := 0; y < c.Height; y += 4 {
		for x := 0; x < c.Width; x += 2 {
			r := 0x2800
			if c.get(x, y) {
				r |= 1
			}
			if c.get(x, y+1) {
				r |= 2
			}
			if c.get(x, y+2) {
				r |= 4
			}
			if c.get(x+1, y) {
				r |= 8
			}
			if c.get(x+1, y+1) {
				r |= 16
			}
			if c.get(x+1, y+2) {
				r |= 32
			}
			if c.get(x, y+3) {
				r |= 64
			}
			if c.get(x+1, y+3) {
				r |= 128
			}
			buf.WriteRune(rune(r))
		}
		buf.WriteByte('\n')
	}

	return buf.String()
}

func (c *BrailleCanvas) get(x, y int) bool {
	if x < 0 || x >= c.Width || y < 0 || y >= c.Height {
		return false
	}
	return c.buffer[y*c.Width+x]
}
