package main

import (
	"github.com/arhammahajan/harness/cmd"
	"github.com/arhammahajan/harness/internal/logging"
)

func main() {
	defer logging.RecoverPanic("main", func() {
		logging.ErrorPersist("Application terminated due to unhandled panic")
	})

	cmd.Execute()
}
