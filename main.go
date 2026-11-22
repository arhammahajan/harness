package main

import (
	"os"

	"github.com/arhammahajan/harness/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:]))
}
