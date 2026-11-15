package main

import (
    "encoding/binary"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "strings"
    "testing"

    "github.com/gorilla/websocket"
)

const testFont = "/usr/share/consolefonts/Uni2-VGA16.psf.gz"

func loadTestFont(t *testing.T) *Font {
    t.Helper()
    if _, err := os.Stat(testFont); err != nil {
        t.Skip("test font not installed")
    }
    f, err := LoadPSF(testFont)
    if err != nil {
        t.Fatal(err)
    }
    return f
}

// drawText renders lines like QEMU does in VGA text mode: cw pixel wide
// cells (9 for VGA, the 9th column left as background).
func drawText(fb *Framebuffer, f *Font, cw int, lines []string,
    fg, bg uint32) {

    glyphs := map[rune][]uint32{}
    for _, g := range f.Glyphs {
        if _, ok := glyphs[g.Rune]; !ok {
            glyphs[g.Rune] = g.Rows
        }
    }
    for i := range fb.Pix {
        fb.Pix[i] = bg
    }
    for row, line := range lines {
        for col, r := range []rune(line) {
            rows := glyphs[r]
            for y := 0; y < f.Height; y++ {
                for x := 0; x < f.Width; x++ {
                    if rows[y]&(1<<x) != 0 {
                        fb.Pix[(row*f.Height+y)*fb.W+col*cw+x] = fg
                    }
                }
            }
        }
    }
}

func TestCP437Table(t *testing.T) {
    if cp437(0xb3) != '│' || cp437(0xdb) != '█' || cp437(0xff) != 0xa0 ||
        cp437(0x80) != 'Ç' || cp437(0x1f) != '▼' || cp437('A') != 'A' {
        t.Fatal("cp437 table misaligned")
    }
}

func TestRecognizeVGATextMode(t *testing.T) {
    f := loadTestFont(t)
    fb := NewFramebuffer(720, 400)
    lines := []string{
        "SeaBIOS (version 1.16.3-debian-1.16.3-2)",
        "",
        "iPXE (https://ipxe.org) 00:03.0 CA00 PCI2.10 PnP PMM+7EFD0FA0+7EF30FA0 CA00",
        "Booting from Hard Disk...",
        "[  OK  ] Started systemd-journald.service - Journal Service.",
        "cirros login: root_{}|~`!@#$%^&*()-=+[];:'\",.<>/?\\",
        "+--------+ ░▒│┤ é à ü",
    }
    drawText(fb, f, 9, lines, 0xaaaaaa, 0x000000)

    rec := NewRecognizer([]*Font{f})
    grid := rec.Recognize(fb)
    if len(grid) != 25 || len(grid[0]) != 80 {
        t.Fatalf("expected 80x25 grid, got %dx%d", len(grid[0]),
            len(grid))
    }
    got := GridText(grid)
    want := strings.Join(lines, "\n") + "\n"
    if got != want {
        t.Fatalf("got:\n%s\nwant:\n%s", got, want)
    }
    if grid[0][0].FG != 7 || grid[0][0].BG != 0 {
        t.Fatalf("unexpected colors %d/%d", grid[0][0].FG, grid[0][0].BG)
    }
}

func TestRecognizeFbconWithCursorAndColors(t *testing.T) {
    f := loadTestFont(t)
    fb := NewFramebuffer(1024, 768) // fbcon: 8 pixel wide cells
    drawText(fb, f, 8, []string{"[  OK  ] Reached target", "login: x"},
        0xaaaaaa, 0x000000)

    // Green "OK" like systemd, underline cursor on the "x" (rows 14-15)
    // and on the blank cell after it.
    for y := 0; y < 16; y++ {
        for x := 3 * 8; x < 5*8; x++ {
            if fb.Pix[y*1024+x] != 0 {
                fb.Pix[y*1024+x] = 0x55ff55
            }
        }
    }
    for _, col := range []int{7, 8} {
        for y := 16 + 14; y < 16+16; y++ {
            for x := col * 8; x < col*8+8; x++ {
                fb.Pix[y*1024+x] = 0xaaaaaa
            }
        }
    }

    grid := NewRecognizer([]*Font{f}).Recognize(fb)
    if len(grid[0]) != 128 || len(grid) != 48 {
        t.Fatalf("expected 128x48 grid, got %dx%d", len(grid[0]),
            len(grid))
    }
    got := GridText(grid[:2])
    if got != "[  OK  ] Reached target\nlogin: x\n" {
        t.Fatalf("got %q", got)
    }
    if grid[0][3].FG != 10 {
        t.Fatalf("OK should be bright green, got color %d", grid[0][3].FG)
    }
    if !grid[1][7].Cursor || !grid[1][8].Cursor || grid[1][6].Cursor {
        t.Fatal("cursor not detected on cells 7 and 8")
    }
}

func TestKeysFromInput(t *testing.T) {
    ev := KeysFromInput([]byte("aB\r\x03\x1b[A"))
    want := []KeyEvent{
        {'a', true}, {'a', false},
        {ksShiftL, true}, {'B', true}, {'B', false}, {ksShiftL, false},
        {ksReturn, true}, {ksReturn, false},
        {ksControlL, true}, {'c', true}, {'c', false}, {ksControlL, false},
        {ksUp, true}, {ksUp, false},
    }
    if len(ev) != len(want) {
        t.Fatalf("got %v", ev)
    }
    for i := range ev {
        if ev[i] != want[i] {
            t.Fatalf("event %d: got %v want %v", i, ev[i], want[i])
        }
    }
    if ev := KeysFromInput([]byte("\x1b[3~")); ev[0].Sym != ksDelete {
        t.Fatalf("delete: got %v", ev)
    }
}

func TestVNCWebsocketURL(t *testing.T) {
    for in, want := range map[string]string{
        "http://h:6080/vnc_lite.html?path=%3Ftoken%3Dabc":     "ws://h:6080/?token=abc",
        "https://h/novnc/vnc_auto.html?path=novnc%3Ftoken%3Dx": "wss://h/novnc?token=x",
        "http://h:6080/vnc_auto.html?token=abc":               "ws://h:6080/?token=abc",
        "ws://h:6080/?token=abc":                              "ws://h:6080/?token=abc",
    } {
        got, err := VNCWebsocketURL(in)
        if err != nil || got != want {
            t.Errorf("%s: got %q (%v), want %q", in, got, err, want)
        }
    }
}

// fakeRFBServer behaves like nova-novncproxy in front of QEMU: RFB 3.8,
// security type None, and serves the given framebuffer as Raw.
func fakeRFBServer(t *testing.T, fb *Framebuffer, keys chan<- KeyEvent) *httptest.Server {
    up := websocket.Upgrader{}
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,
        r *http.Request) {

        if r.URL.Query().Get("token") != "secret" {
            http.Error(w, "bad token", http.StatusForbidden)
            return
        }
        c, err := up.Upgrade(w, r, nil)
        if err != nil {
            return
        }
        defer c.Close()
        s := &wsStream{c: c}
        must := func(_ int, err error) {
            if err != nil {
                panic(err)
            }
        }
        rd := func(n int) []byte {
            b := make([]byte, n)
            if _, err := io.ReadFull(s, b); err != nil {
                panic(err)
            }
            return b
        }
        defer func() { recover() }()

        must(s.Write([]byte("RFB 003.008\n")))
        if string(rd(12)) != "RFB 003.008\n" {
            t.Error("bad client version")
            return
        }
        must(s.Write([]byte{1, 1}))
        if rd(1)[0] != 1 {
            t.Error("client did not pick None")
            return
        }
        must(s.Write([]byte{0, 0, 0, 0}))
        rd(1) // ClientInit
        init := make([]byte, 24)
        binary.BigEndian.PutUint16(init[0:], uint16(fb.W))
        binary.BigEndian.PutUint16(init[2:], uint16(fb.H))
        binary.BigEndian.PutUint32(init[20:], 4)
        must(s.Write(append(init, "test"...)))

        for {
            switch mt := rd(1)[0]; mt {
            case 0:
                rd(19)
            case 2:
                n := binary.BigEndian.Uint16(rd(3)[1:])
                rd(4 * int(n))
            case 3:
                if rd(9)[0] == 1 {
                    continue // incremental: nothing changed
                }
                msg := []byte{0, 0, 0, 1, 0, 0, 0, 0}
                msg = append(msg, byte(uint16(fb.W)>>8), byte(uint16(fb.W)))
                msg = append(msg, byte(uint16(fb.H)>>8), byte(uint16(fb.H)))
                msg = append(msg, 0, 0, 0, 0) // Raw
                for _, p := range fb.Pix {
                    msg = append(msg, byte(p), byte(p>>8), byte(p>>16), byte(p>>24))
                }
                must(s.Write(msg))
            case 4:
                b := rd(7)
                keys <- KeyEvent{binary.BigEndian.Uint32(b[3:]), b[0] == 1}
            default:
                t.Errorf("unexpected client message %d", mt)
                return
            }
        }
    }))
}

func TestVNCEndToEnd(t *testing.T) {
    f := loadTestFont(t)
    fb := NewFramebuffer(720, 400)
    drawText(fb, f, 9, []string{"Welcome", "cirros login:"}, 0xaaaaaa, 0)
    keys := make(chan KeyEvent, 100)
    srv := fakeRFBServer(t, fb, keys)
    defer srv.Close()

    pageURL := "http" + strings.TrimPrefix(srv.URL, "http") +
        "/vnc_lite.html?path=%3Ftoken%3Dsecret"
    wsURL, err := VNCWebsocketURL(pageURL)
    if err != nil {
        t.Fatal(err)
    }
    v, err := DialVNC(wsURL)
    if err != nil {
        t.Fatal(err)
    }
    defer v.Close()
    if v.Name != "test" || v.FB.W != 720 || v.FB.H != 400 {
        t.Fatalf("bad ServerInit: %q %dx%d", v.Name, v.FB.W, v.FB.H)
    }

    rec := NewRecognizer([]*Font{f})
    texts := make(chan string, 10)
    v.OnUpdate = func(fb *Framebuffer) {
        texts <- GridText(rec.Recognize(fb))
    }
    go v.Run()
    if got := <-texts; got != "Welcome\ncirros login:\n" {
        t.Fatalf("got %q", got)
    }

    if err := v.SendKeys(KeysFromInput([]byte("R"))); err != nil {
        t.Fatal(err)
    }
    want := []KeyEvent{{ksShiftL, true}, {'R', true}, {'R', false},
        {ksShiftL, false}}
    for _, w := range want {
        if got := <-keys; got != w {
            t.Fatalf("got key %v want %v", got, w)
        }
    }
}
