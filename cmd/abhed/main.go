// Command abhed is the Community Edition binary. Everything it does lives in
// the app package; this main exists so the linker has somewhere to put the
// version. Its test keeps every package under ee/ out of its dependencies.
package main

import (
	"os"

	"github.com/zybuu-ai/abhed/app"
)

var version = "0.1.0-dev"

func main() {
	os.Exit(app.Main(os.Args[1:], app.WithVersion(version)))
}
