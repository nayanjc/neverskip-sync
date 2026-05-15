package state

import "os"

func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}
