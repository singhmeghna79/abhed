// Command abhed-ee is the enterprise edition binary: the Community command
// with this edition's options passed in. Nothing about serving, sessions or
// the console is repeated here; that is the point of the arrangement.
package main

import (
	"os"

	eeapp "github.com/zybuu-ai/abhed/ee/app"
	"github.com/zybuu-ai/abhed/app"
)

var version = "0.1.0-dev"

func main() {
	os.Exit(app.Main(os.Args[1:], eeapp.Options(version)...))
}
