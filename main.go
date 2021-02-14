package main

import (
    "os"
    "io"
    "fmt"
)

// Config
type Config struct {
    // Outputs
    Stdout io.Writer
    Stdin  io.Reader
    Stderr io.Writer
    ConsoleUrl string
}

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Websocket Console URL is required!")
        os.Exit(1)
    }

    c := Config{
        Stdout:     os.Stdout,
        Stdin:      os.Stdin,
        Stderr:     os.Stderr,
        ConsoleUrl: os.Args[1],
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
