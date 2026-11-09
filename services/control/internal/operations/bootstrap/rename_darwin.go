package bootstrap

import "golang.org/x/sys/unix"

func renameExclusive(directory int, source, destination string) error {
	return unix.RenameatxNp(directory, source, directory, destination, unix.RENAME_EXCL)
}
