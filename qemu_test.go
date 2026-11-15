package main

import (
    "image/png"
    "os"
    "strings"
    "testing"
)

// cirros-vga-text.png is a framebuffer captured through nova-novncproxy
// from a cirros instance: QEMU in VGA text mode with the SeaVGABIOS font
// (whose 'w', 'x', 'v', '0', 'T', 'X', 'V' and broken '|' differ from the
// Linux console fonts).
const cirrosScreen = `login as 'cirros' user. default password: 'gocubsgo'. use 'sudo' for root.
cirros login: cirros
Password:
$
$
$ ip -br -c a
BusyBox v1.35.0 (2024-09-26 17:30:50 UTC) multi-call binary.

Usage: ip [OPTIONS] address|route|link|tunnel|neigh|rule [ARGS]

OPTIONS := -f[amily] inet|inet6|link | -o[neline]

ip addr add|del IFADDR dev IFACE | show|flush [dev IFACE] [to PREFIX]
ip route list|flush|add|del|change|append|replace|test ROUTE
ip link set IFACE [up|down] [arp on|off] [multicast on|off]
        [promisc on|off] [mtu NUM] [name NAME] [qlen NUM] [address MAC]
        [master IFACE | nomaster] [netns PID]
ip tunnel add|change|del|show [NAME]
        [mode ipip|gre|sit] [remote ADDR] [local ADDR] [ttl TTL]
ip neigh show|flush [to PREFIX] [dev DEV] [nud STATE]
ip rule [list] | add|del SELECTOR ACTION
$

login as 'cirros' user. default password: 'gocubsgo'. use 'sudo' for root.
cirros login:
`

func loadPNG(t *testing.T, path string) *Framebuffer {
    t.Helper()
    f, err := os.Open(path)
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()
    img, err := png.Decode(f)
    if err != nil {
        t.Fatal(err)
    }
    b := img.Bounds()
    fb := NewFramebuffer(b.Dx(), b.Dy())
    for y := 0; y < fb.H; y++ {
        for x := 0; x < fb.W; x++ {
            r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
            fb.Pix[y*fb.W+x] = (r>>8)<<16 | (g>>8)<<8 | bl>>8
        }
    }
    return fb
}

func TestRecognizeRealQEMUScreen(t *testing.T) {
    fb := loadPNG(t, "testdata/cirros-vga-text.png")

    // The embedded VGA font alone must be enough, as well as combined
    // with whatever console fonts are installed locally.
    vga, err := ParsePSF("vga", vgaFont)
    if err != nil {
        t.Fatal(err)
    }
    all, err := LoadFonts(nil)
    if err != nil {
        t.Fatal(err)
    }
    for name, fonts := range map[string][]*Font{
        "embedded": {vga}, "defaults": all} {

        got := GridText(NewRecognizer(fonts).Recognize(fb))
        if got != cirrosScreen {
            gl, wl := strings.Split(got, "\n"), strings.Split(cirrosScreen, "\n")
            for i := range wl {
                if i >= len(gl) || gl[i] != wl[i] {
                    t.Errorf("%s fonts, line %d:\n got  %q\n want %q",
                        name, i+1, lineAt(gl, i), wl[i])
                }
            }
        }
    }
}

func TestSGRUsesTerminalDefaults(t *testing.T) {
    for _, c := range []struct {
        fg, bg uint8
        want   string
    }{
        {7, 0, "\x1b[0m"},           // console default
        {0, 0, "\x1b[0;30;49m"},     // never force a black background
        {10, 0, "\x1b[0;92;49m"},    // bright green on default
        {7, 1, "\x1b[0;39;44m"},     // default text on blue
        {15, 4, "\x1b[0;97;41m"},    // white on red
    } {
        if got := sgr(c.fg, c.bg); got != c.want {
            t.Errorf("sgr(%d, %d) = %q, want %q", c.fg, c.bg, got, c.want)
        }
    }

    // Blank cells must not carry a foreground color, otherwise the
    // renderer switches attributes on every space.
    fb := NewFramebuffer(720, 400)
    vga, _ := ParsePSF("vga", vgaFont)
    grid := NewRecognizer([]*Font{vga}).Recognize(fb)
    if c := grid[0][0]; c.R != ' ' || c.FG != 7 || c.BG != 0 {
        t.Errorf("blank cell = %+v, want space with default colors", c)
    }
}

func lineAt(lines []string, i int) string {
    if i < len(lines) {
        return lines[i]
    }
    return "<missing>"
}
