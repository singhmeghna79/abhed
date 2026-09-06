package extension

import "os"

func pathEnv() string { return os.Getenv("PATH") }
