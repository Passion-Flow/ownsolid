package dameng

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The library never calls os.Chdir. The child launcher selects the typed cwd;
// native admission verifies that actual directory and both relative lookups.
func verifyNativeLayout(m NativeManifest, library os.FileInfo) error {
	if m.FormatVersion == 1 {
		return nil
	}
	bad := fail("DAMENG_NATIVE_LAYOUT_INVALID")
	if library == nil || m.FormatVersion != 2 || m.LoaderProfile != RelativeReleaseLoaderProfile || filepath.Base(m.LibraryDirectory) != "bin" {
		return bad
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != m.LibraryDirectory {
		return fail("DAMENG_NATIVE_WORKING_DIRECTORY_INVALID")
	}
	current, err := os.Open(".")
	if err != nil {
		return bad
	}
	actual, statError := current.Stat()
	closeError := current.Close()
	if statError != nil || closeError != nil || !os.SameFile(library, actual) || !odbcPrivateOwned(actual, true) {
		return bad
	}
	parent := filepath.Dir(m.LibraryDirectory)
	before, err := os.Lstat(parent)
	if err != nil || !odbcPrivateOwned(before, true) {
		return bad
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return bad
	}
	defer root.Close()
	pinned, err := root.Open(".")
	if err != nil {
		return bad
	}
	pinnedInfo, statError := pinned.Stat()
	closeError = pinned.Close()
	if statError != nil || closeError != nil || !nativeSamePrivateDirectory(before, pinnedInfo) {
		return bad
	}
	if err := verifyEmptyNativeDirectory(root, "release"); err != nil {
		return bad
	}
	libraryRoot, err := os.OpenRoot(m.LibraryDirectory)
	if err != nil {
		return bad
	}
	defer libraryRoot.Close()
	libraryPinned, err := libraryRoot.Open(".")
	if err != nil {
		return bad
	}
	libraryInfo, statError := libraryPinned.Stat()
	closeError = libraryPinned.Close()
	if statError != nil || closeError != nil || !nativeSamePrivateDirectory(library, libraryInfo) {
		return bad
	}
	if err := verifyEmptyNativeDirectory(libraryRoot, "dependencies"); err != nil {
		return bad
	}
	parentAfter, err := os.Lstat(parent)
	libraryAfter, libraryError := os.Lstat(m.LibraryDirectory)
	if err != nil || libraryError != nil || !nativeSamePrivateDirectory(before, parentAfter) || !nativeSamePrivateDirectory(library, libraryAfter) {
		return bad
	}
	return nil
}

func verifyEmptyNativeDirectory(root *os.Root, name string) error {
	bad := fail("DAMENG_NATIVE_LAYOUT_INVALID")
	initial, err := root.Lstat(name)
	if err != nil || !odbcPrivateOwned(initial, true) {
		return bad
	}
	directory, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return bad
	}
	opened, statError := directory.Stat()
	if statError != nil || !nativeSamePrivateDirectory(initial, opened) {
		directory.Close()
		return bad
	}
	entries, readError := directory.Readdirnames(1)
	after, statError := directory.Stat()
	closeError := directory.Close()
	named, nameError := root.Lstat(name)
	if len(entries) != 0 || readError != io.EOF || statError != nil || closeError != nil || nameError != nil || !nativeSamePrivateDirectory(initial, after) || !nativeSamePrivateDirectory(initial, named) {
		return bad
	}
	return nil
}

// Directories have directory modes and link-count semantics. Keep the ordinary
// private-file check unchanged; it deliberately requires a single-link 0600 file.
func nativeSamePrivateDirectory(before, after os.FileInfo) bool {
	return odbcPrivateOwned(before, true) && odbcPrivateOwned(after, true) &&
		os.SameFile(before, after) && before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}
