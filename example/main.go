package main

import (
	"fmt"
	"io"
	"os"

	linenoise "github.com/hasyimibhar/go-linenoise"
)

func main() {
	ln := linenoise.New()
	defer ln.Cleanup()

REPLLoop:
	for {
		line, err := ln.Readline("> ")
		if err == io.EOF {
			break REPLLoop
		}

		switch line {
		case "exit":
			break REPLLoop
		case "clear":
			ln.ClearScreen()
		default:
			fmt.Println(line)
		}
	}

	os.Exit(0)
}
