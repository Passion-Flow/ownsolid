package install

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const encryptedWorkDirectory = ".encrypted-backup-work"

var (
	errArchiveExists  = errors.New("INSTALL_ENCRYPTED_BACKUP_EXISTS: the archive already exists; use a new output name")
	errArchiveInvalid = errors.New("INSTALL_ENCRYPTED_BACKUP_INVALID: use an intact ownsolid encrypted backup")
)

func openEncryptedDirectory(c Config, archive string) (*os.Root, *os.File, error) {
	if !filepath.IsAbs(archive) || filepath.Clean(archive) != archive || !strings.HasSuffix(archive, ".tar.age") {
		return nil, nil, errors.New("INSTALL_ENCRYPTED_BACKUP_PATH_INVALID: specify an absolute .tar.age file path")
	}
	return openBackupDirectory(c, filepath.Dir(archive),
		errors.New("INSTALL_ENCRYPTED_BACKUP_DIRECTORY_INVALID: use a root-owned directory with mode 0700 outside the installation and without symlinks"),
		errors.New("INSTALL_ENCRYPTED_BACKUP_BUSY: another job holds the archive directory; retry when it finishes"))
}

func readEncryptionKey(path string) (string, error) {
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	var key string
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if key != "" {
			return "", errors.New("multiple keys")
		}
		key = string(line)
	}
	if key == "" {
		return "", errors.New("missing key")
	}
	return key, nil
}

// BackupEncrypted verifies a logical backup and publishes it as a new age archive.
// It uses only the public recipient file.
func BackupEncrypted(ctx context.Context, c Config, runner StreamRunner, output, recipientFile string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: backup reads files owned by the service users; run it as root")
	}
	key, err := readEncryptionKey(recipientFile)
	recipient, parseErr := age.ParseX25519Recipient(key)
	if err != nil || parseErr != nil {
		return errors.New("INSTALL_ENCRYPTED_BACKUP_RECIPIENT_INVALID: supply a protected file containing one native age X25519 public recipient")
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	root, dir, err := openEncryptedDirectory(c, output)
	if err != nil {
		return err
	}
	defer root.Close()
	defer dir.Close()
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	work, cleanup, err := encryptedScratch(c, out)
	if err != nil {
		return err
	}
	defer cleanup()
	name := filepath.Base(output)
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("INSTALL_ENCRYPTED_BACKUP_OUTPUT_FAILED: %w", err)
		}
		return errArchiveExists
	}
	path, err := o.backup(ctx, work)
	if err == nil {
		if err = publishEncryptedBackup(ctx, root, dir, name, path, recipient); err != nil && !errors.Is(err, errArchiveExists) {
			err = &StepError{Step: "backup", Code: "INSTALL_ENCRYPTED_BACKUP_OUTPUT_FAILED", Detail: err.Error()}
		}
	}
	if err != nil {
		err = retryWith(err, "backup")
		o.record("backup", "failed", err.Error())
		return err
	}
	o.record("backup", "complete", output)
	fmt.Fprintf(out, "BACKUP_COMPLETE path=%s\n", output)
	return nil
}

func publishEncryptedBackup(ctx context.Context, root *os.Root, dir *os.File, name, path string, recipient *age.X25519Recipient) error {
	temporary := ".ownsolid-encrypted-partial-" + rand.Text()
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	defer f.Close()
	if err := writeEncryptedArchive(ctx, f, path, recipient); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Link(temporary, name); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errArchiveExists
		}
		return err
	}
	if err := root.Remove(temporary); err != nil {
		return err
	}
	return dir.Sync()
}

// RestoreEncrypted authenticates the entire age archive before running the normal
// checked restore. The identity remains outside the installation and repository.
func RestoreEncrypted(ctx context.Context, c Config, runner StreamRunner, from, identityFile string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: restore writes files owned by the service users; run it as root")
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	work, manifest, cleanup, err := loadEncryptedBackup(ctx, c, from, identityFile, out)
	if err != nil {
		if errors.Is(err, errArchiveInvalid) {
			o.record("restore", "failed", from+": "+err.Error())
		}
		return err
	}
	defer cleanup()
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	return o.restoreVerified(ctx, work, from, manifest)
}

func loadEncryptedBackup(ctx context.Context, c Config, from, identityFile string, out io.Writer) (string, backupManifest, func(), error) {
	root, dir, err := openEncryptedDirectory(c, from)
	if err != nil {
		return "", backupManifest{}, nil, err
	}
	defer root.Close()
	defer dir.Close()
	installed, err := filepath.EvalSymlinks(c.InstallRoot)
	if err != nil {
		return "", backupManifest{}, nil, err
	}
	for _, protected := range []string{installed, filepath.Dir(from)} {
		relative, err := filepath.Rel(protected, identityFile)
		if err == nil && filepath.IsLocal(relative) {
			return "", backupManifest{}, nil, errors.New("INSTALL_ENCRYPTED_BACKUP_IDENTITY_LOCATION: keep the recovery identity outside the installation and the archive directory")
		}
	}
	key, err := readEncryptionKey(identityFile)
	identity, parseErr := age.ParseX25519Identity(key)
	if err != nil || parseErr != nil {
		return "", backupManifest{}, nil, errors.New("INSTALL_ENCRYPTED_BACKUP_IDENTITY_INVALID: supply a protected file containing one native age X25519 private identity")
	}
	archive, err := openEncryptedArchive(root, filepath.Base(from))
	if err != nil {
		return "", backupManifest{}, nil, err
	}
	defer archive.Close()
	plaintext, err := age.Decrypt(archiveReader{ctx, archive}, identity)
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		return "", backupManifest{}, nil, errors.New("INSTALL_ENCRYPTED_BACKUP_WRONG_IDENTITY: use the identity for this archive's recipient")
	}
	if err != nil {
		return "", backupManifest{}, nil, errArchiveInvalid
	}
	work, cleanup, err := encryptedScratch(c, out)
	if err != nil {
		return "", backupManifest{}, nil, err
	}
	manifest, err := readEncryptedArchive(ctx, plaintext, work)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cleanup()
		return "", backupManifest{}, nil, err
	}
	return work, manifest, cleanup, nil
}

func openEncryptedArchive(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 ||
		before.Sys().(*syscall.Stat_t).Uid != 0 {
		return nil, errArchiveInvalid
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errArchiveInvalid
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.Mode() != before.Mode() {
		f.Close()
		return nil, errArchiveInvalid
	}
	return f, nil
}

// Only the installation operation lock holder may use this directory. The
// ownership marker prevents cleanup of a pre-existing operator directory.
func encryptedScratch(c Config, out io.Writer) (string, func(), error) {
	invalid := errors.New("INSTALL_ENCRYPTED_BACKUP_WORK_INVALID: the install root must allow a root-owned private encrypted backup workspace")
	path := filepath.Join(c.InstallRoot, encryptedWorkDirectory)
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, invalid
	}
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 || st.Sys().(*syscall.Stat_t).Uid != 0 {
		return "", nil, invalid
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", nil, invalid
	}
	if err := claimEncryptedScratch(root); err != nil {
		root.Close()
		return "", nil, invalid
	}
	const data = "data"
	if err := root.RemoveAll(data); err != nil {
		root.Close()
		return "", nil, invalid
	}
	if err := root.Mkdir(data, 0o700); err != nil {
		root.Close()
		return "", nil, invalid
	}
	return filepath.Join(path, data), func() {
		if err := root.RemoveAll(data); err != nil {
			fmt.Fprintln(out, "warning: could not clear the private encrypted backup workspace")
		}
		root.Close()
	}, nil
}

func claimEncryptedScratch(root *os.Root) error {
	const marker = "ownsolid-workspace-v1"
	info, err := root.Lstat(marker)
	if err == nil && info.IsDir() && info.Mode().Perm() == 0o700 && info.Sys().(*syscall.Stat_t).Uid == 0 {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("workspace ownership does not match")
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	if entries, err := dir.Readdirnames(1); len(entries) != 0 || !errors.Is(err, io.EOF) {
		return errors.New("workspace is not empty")
	}
	if err := root.Mkdir(marker, 0o700); err != nil {
		return err
	}
	return dir.Sync()
}
