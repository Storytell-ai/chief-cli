// Command chief is the CLI for the Chief public API.
package main

import (
	"context"
	"os"
)

func main() {
	if err := Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
