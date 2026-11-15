package main

import (
    "os"
    "io"
    "flag"
    "fmt"
    "net/url"
    "strings"
    "time"
)

// Config
type Config struct {
    // Outputs
    Stdout io.Writer
    Stdin  io.Reader
    Stderr io.Writer
    ConsoleUrl string
}

type fontList []string

func (f *fontList) String() string     { return strings.Join(*f, ",") }
func (f *fontList) Set(s string) error { *f = append(*f, s); return nil }

func main() {
    var fonts fontList
    vnc := flag.Bool("vnc", false, "force VNC mode (default for "+
        "http(s):// noVNC URLs)")
    noColor := flag.Bool("no-color", false, "VNC mode: do not "+
        "reproduce console colors")
    shot := flag.Bool("screenshot", false, "VNC mode: print the "+
        "console screen as text and exit")
    typ := flag.String("type", "", "VNC mode with -screenshot: text "+
        "typed before the screenshot (\\r for Enter)")
    wait := flag.Duration("wait", 2*time.Second, "VNC mode with "+
        "-screenshot: how long to let the screen settle")
    flag.Var(&fonts, "font", "VNC mode: PSF console font (path or glob)"+
        " used to recognize characters, can be repeated")
    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: %s [options] CONSOLE_URL\n\n"+
            "CONSOLE_URL is either a serial console URL (ws://, wss://)\n"+
            "or a noVNC console URL (http://, https://).\n\n",
            os.Args[0])
        flag.PrintDefaults()
    }
    flag.Parse()

    if flag.NArg() < 1 {
        fmt.Println("Websocket Console URL is required!")
        os.Exit(1)
    }

    c := Config{
        Stdout:     os.Stdout,
        Stdin:      os.Stdin,
        Stderr:     os.Stderr,
        ConsoleUrl: flag.Arg(0),
    }

    u, err := url.Parse(c.ConsoleUrl)
    if err == nil && (*vnc || u.Scheme == "http" || u.Scheme == "https") {
        err = RunVNC(VNCOptions{
            URL:        c.ConsoleUrl,
            Fonts:      fonts,
            Color:      !*noColor,
            Screenshot: *shot,
            Type:       strings.ReplaceAll(*typ, `\r`, "\r"),
            Wait:       *wait,
        })
        if err != nil {
            fmt.Println(err)
            os.Exit(1)
        }
        os.Exit(0)
    }
    os.Exit(connect(c))
}

func connect(c Config) (exitcode int) {
    var err error

    con := &Connect{config: c}
    err = con.Run()
    if  err != nil {
        fmt.Println(err)
        return 1
    }
    return 0
}
