package main

import (
    "bytes"
    "compress/gzip"
    _ "embed"
    "encoding/binary"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "unicode/utf8"
)

// Glyph is one character bitmap of a console font.
// Rows[y] holds the pixels of line y, bit x set means pixel x is "ink"
// (bit 0 is the leftmost pixel).
type Glyph struct {
    Rune rune
    Rows []uint32
}

// Font is a bitmap console font (PSF1 or PSF2).
type Font struct {
    Name   string
    Width  int
    Height int
    Glyphs []Glyph
}

// vgaFont is the 8x16 font of SeaVGABIOS, the VGA BIOS shipped with QEMU:
// the font QEMU draws in VGA text mode until the guest loads another one.
// See fonts/README.md for its origin (public domain).
//
//go:embed fonts/vga8x16.psf
var vgaFont []byte

// defaultFontPaths lists console fonts commonly installed on Linux
// workstations (console-setup and kbd packages), covering the fonts
// usually loaded by distributions on their console.
var defaultFontPaths = []string{
    "/usr/share/consolefonts/Uni2-VGA16.psf.gz",
    "/usr/share/consolefonts/Lat15-VGA16.psf.gz",
    "/usr/share/consolefonts/Uni2-Fixed16.psf.gz",
    "/usr/share/consolefonts/Uni2-Terminus16.psf.gz",
    "/usr/share/consolefonts/Uni2-TerminusBold16.psf.gz",
    "/usr/share/consolefonts/Uni2-TerminusBoldVGA16.psf.gz",
    "/usr/share/kbd/consolefonts/default8x16.psfu.gz",
    "/usr/share/kbd/consolefonts/default8x16.psf.gz",
    "/usr/share/kbd/consolefonts/eurlatgr.psfu.gz",
    "/usr/share/kbd/consolefonts/lat0-16.psfu.gz",
    "/usr/share/kbd/consolefonts/Lat2-Terminus16.psfu.gz",
}

// LoadFonts returns the embedded VGA font plus every font matching the
// given paths or glob patterns. When no pattern is given,
// defaultFontPaths are used and missing files are silently ignored.
func LoadFonts(patterns []string) ([]*Font, error) {
    explicit := len(patterns) > 0
    if !explicit {
        patterns = defaultFontPaths
    }

    vga, err := ParsePSF("vga8x16 (embedded)", vgaFont)
    if err != nil {
        return nil, err
    }
    fonts := []*Font{vga}
    for _, p := range patterns {
        matches, err := filepath.Glob(p)
        if err != nil {
            return nil, err
        }
        if len(matches) == 0 && explicit {
            return nil, fmt.Errorf("font not found: %s", p)
        }
        for _, m := range matches {
            f, err := LoadPSF(m)
            if err != nil {
                if explicit {
                    return nil, err
                }
                continue
            }
            fonts = append(fonts, f)
        }
    }
    return fonts, nil
}

// LoadPSF reads a PSF1 or PSF2 font, optionally gzip compressed.
func LoadPSF(path string) (*Font, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    return ParsePSF(filepath.Base(path), data)
}

// ParsePSF decodes a PSF1 or PSF2 font, optionally gzip compressed.
func ParsePSF(name string, data []byte) (*Font, error) {
    path := name
    var err error
    if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
        zr, err := gzip.NewReader(bytes.NewReader(data))
        if err != nil {
            return nil, fmt.Errorf("%s: %v", path, err)
        }
        data, err = io.ReadAll(zr)
        if err != nil {
            return nil, fmt.Errorf("%s: %v", path, err)
        }
    }

    var f *Font
    switch {
    case len(data) >= 4 && data[0] == 0x36 && data[1] == 0x04:
        f, err = parsePSF1(data)
    case len(data) >= 32 && bytes.Equal(data[0:4],
        []byte{0x72, 0xb5, 0x4a, 0x86}):
        f, err = parsePSF2(data)
    default:
        err = fmt.Errorf("not a PSF font")
    }
    if err != nil {
        return nil, fmt.Errorf("%s: %v", path, err)
    }
    f.Name = name
    return f, nil
}

func glyphRows(raw []byte, width, height int) []uint32 {
    bpr := (width + 7) / 8
    rows := make([]uint32, height)
    for y := 0; y < height; y++ {
        var r uint32
        for x := 0; x < width; x++ {
            if raw[y*bpr+x/8]&(0x80>>(x%8)) != 0 {
                r |= 1 << x
            }
        }
        rows[y] = r
    }
    return rows
}

// pickRune selects the best character for a glyph mapped to several
// code points (e.g. 'A', Greek Alpha and Cyrillic A share one glyph).
func pickRune(runes []rune) rune {
    for _, r := range runes {
        // The IBM VGA font draws '|' as a broken bar: on a console,
        // that glyph is a pipe.
        if r == '¦' {
            return '|'
        }
    }
    for _, r := range runes {
        if r >= 0x20 && r < 0x7f {
            return r
        }
    }
    if len(runes) > 0 {
        return runes[0]
    }
    return -1
}

func parsePSF1(data []byte) (*Font, error) {
    mode := data[2]
    height := int(data[3])
    n := 256
    if mode&0x01 != 0 {
        n = 512
    }
    if height == 0 || len(data) < 4+n*height {
        return nil, fmt.Errorf("truncated PSF1 font")
    }
    f := &Font{Width: 8, Height: height}
    maps := make([][]rune, n)
    if mode&0x06 != 0 {
        // Unicode table: little endian u16 per entry, 0xFFFE starts
        // sequences (ignored), 0xFFFF ends the glyph entry.
        tbl := data[4+n*height:]
        g, inSeq := 0, false
        for i := 0; i+1 < len(tbl) && g < n; i += 2 {
            v := binary.LittleEndian.Uint16(tbl[i:])
            switch {
            case v == 0xFFFF:
                g++
                inSeq = false
            case v == 0xFFFE:
                inSeq = true
            case !inSeq:
                maps[g] = append(maps[g], rune(v))
            }
        }
    } else {
        for i := 0; i < n && i < 256; i++ {
            maps[i] = []rune{cp437(byte(i))}
        }
    }
    for i := 0; i < n; i++ {
        r := pickRune(maps[i])
        if r < 0 {
            continue
        }
        raw := data[4+i*height : 4+(i+1)*height]
        f.Glyphs = append(f.Glyphs, Glyph{r, glyphRows(raw, 8, height)})
    }
    return f, nil
}

func parsePSF2(data []byte) (*Font, error) {
    le := binary.LittleEndian
    hdr := int(le.Uint32(data[8:]))
    flags := le.Uint32(data[12:])
    n := int(le.Uint32(data[16:]))
    charsize := int(le.Uint32(data[20:]))
    height := int(le.Uint32(data[24:]))
    width := int(le.Uint32(data[28:]))
    if width < 1 || width > 32 || height < 1 || height > 64 ||
        charsize < height*((width+7)/8) || n < 1 || n > 65536 ||
        len(data) < hdr+n*charsize {
        return nil, fmt.Errorf("unsupported or truncated PSF2 font")
    }
    f := &Font{Width: width, Height: height}
    maps := make([][]rune, n)
    if flags&0x01 != 0 {
        // Unicode table: UTF-8 strings, 0xFE starts sequences (ignored),
        // 0xFF ends the glyph entry.
        tbl := data[hdr+n*charsize:]
        g, inSeq := 0, false
        for i := 0; i < len(tbl) && g < n; {
            switch b := tbl[i]; {
            case b == 0xFF:
                g++
                inSeq = false
                i++
            case b == 0xFE:
                inSeq = true
                i++
            default:
                r, sz := utf8.DecodeRune(tbl[i:])
                if !inSeq && r != utf8.RuneError {
                    maps[g] = append(maps[g], r)
                }
                i += sz
            }
        }
    } else {
        for i := 0; i < n && i < 256; i++ {
            maps[i] = []rune{cp437(byte(i))}
        }
    }
    for i := 0; i < n; i++ {
        r := pickRune(maps[i])
        if r < 0 {
            continue
        }
        raw := data[hdr+i*charsize : hdr+(i+1)*charsize]
        f.Glyphs = append(f.Glyphs,
            Glyph{r, glyphRows(raw, width, height)})
    }
    return f, nil
}

// cp437 maps a code page 437 byte (VGA BIOS charset) to Unicode.
func cp437(b byte) rune {
    if b >= 0x20 && b < 0x7f {
        return rune(b)
    }
    if b < 0x20 {
        return []rune("\u0000☺☻♥♦♣♠•◘○◙♂♀♪♫☼►◄↕‼¶§▬↨↑↓→←∟↔▲▼")[b]
    }
    if b == 0x7f {
        return '⌂'
    }
    return []rune(
        "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»" +
            "░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
            "αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ ")[b-0x80]
}
