package identityfixture

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maximumTestDSNBytes = 4096

// databaseInput belongs only to isolated test construction,not serving config.
func databaseInput(path, legacy string) (string, error) {
	if path == "" {
		return legacy, nil
	}
	fail := func() (string, error) { return "", errors.New("ISOLATED_TEST_DATABASE_PRIVATE_INPUT_INVALID") }
	if legacy != "" || !filepath.IsAbs(path) {
		return fail()
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return fail()
	}
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || parent.Mode().Perm() != 0700 {
		return fail()
	}
	parentOwner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || parentOwner.Uid != uint32(os.Geteuid()) {
		return fail()
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return fail()
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return fail()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || info.Size() < 1 || info.Size() > maximumTestDSNBytes {
		return fail()
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximumTestDSNBytes+1))
	if e != nil || len(raw) != int(info.Size()) {
		return fail()
	}
	dsn := strings.TrimSpace(string(raw))
	if dsn == "" || strings.ContainsAny(dsn, "\x00\r\n") {
		return fail()
	}
	return dsn, nil
}
