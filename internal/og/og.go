// Package og renders the dynamic Open Graph banner images (1360×712 PNG)
// showing a title and the site name, like og-image.php.
package og

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

// Options configure a banner.
type Options struct {
	Title       string
	SiteTitle   string
	Background  string
	Text        string
	Font        []byte // TrueType font data
	FaviconPath string // optional local image path
}

const (
	width        = 1360
	height       = 712
	marginLeft   = 80
	marginRight  = 80
	marginTop    = 160
	maxY         = 480
	lineHeight   = 76
	titleSize    = 56
	siteSize     = 42
	faviconSize  = 64
	footerBottom = 80
)

// Render draws the banner and returns PNG bytes.
func Render(o Options) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{parseHex(o.Background, color.RGBA{0xf2, 0xf2, 0xf2, 0xff})}, image.Point{}, draw.Src)
	textColor := parseHex(o.Text, color.RGBA{0x1a, 0x1a, 0x1a, 0xff})

	f, err := opentype.Parse(o.Font)
	if err != nil {
		return nil, err
	}
	titleFace, err := opentype.NewFace(f, &opentype.FaceOptions{Size: titleSize, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	siteFace, err := opentype.NewFace(f, &opentype.FaceOptions{Size: siteSize, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer siteFace.Close()

	d := &font.Drawer{Dst: img, Src: &image.Uniform{textColor}, Face: titleFace}
	maxWidth := fixed.I(width - marginLeft - marginRight)

	// Word-wrap the title.
	var lines []string
	for _, raw := range strings.Split(o.Title, "\n") {
		current := ""
		for _, word := range strings.Fields(raw) {
			test := word
			if current != "" {
				test = current + " " + word
			}
			if d.MeasureString(test) > maxWidth && current != "" {
				lines = append(lines, current)
				current = word
			} else {
				current = test
			}
		}
		if current != "" {
			lines = append(lines, current)
		}
	}

	y := marginTop
	for i, line := range lines {
		if y+lineHeight > maxY && i < len(lines)-1 {
			for utf8.RuneCountInString(line) > 0 {
				if d.MeasureString(line+"…") <= maxWidth {
					line += "…"
					break
				}
				_, size := utf8.DecodeLastRuneInString(line)
				line = line[:len(line)-size]
			}
			d.Dot = fixed.P(marginLeft, y)
			d.DrawString(line)
			break
		}
		d.Dot = fixed.P(marginLeft, y)
		d.DrawString(line)
		y += lineHeight
	}

	// Footer: favicon + site title.
	favY := height - footerBottom - faviconSize
	textX := marginLeft
	if fav := loadImage(o.FaviconPath); fav != nil {
		rect := image.Rect(marginLeft, favY, marginLeft+faviconSize, favY+faviconSize)
		xdraw.CatmullRom.Scale(img, rect, fav, fav.Bounds(), xdraw.Over, nil)
		textX = marginLeft + faviconSize + 24
	}
	d.Face = siteFace
	d.Dot = fixed.P(textX, favY+49)
	d.DrawString(o.SiteTitle)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func loadImage(path string) image.Image {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

func parseHex(s string, def color.RGBA) color.RGBA {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return def
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return def
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}
