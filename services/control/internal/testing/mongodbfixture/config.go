// Package mongodbfixture owns one synthetic native TLS/SCRAM replica set. It is
// used by explicitly tagged integration tests, never production composition.
package mongodbfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const InputEnvironment = "PROJECT_ONE_MONGODB_NATIVE_FIXTURE_FILE"
const InputFormat = "projectone.mongodb.native-fixture-input.v1"
const ReceiptFormat = "projectone.mongodb.native-fixture-receipt.v1"

type Policy struct {
	ListenHost         string `json:"listen_host"`
	Port               int    `json:"port"`
	ReplicaSet         string `json:"replica_set"`
	Database           string `json:"database"`
	Collection         string `json:"collection"`
	CacheMiB           int    `json:"cache_mib"`
	WorkingMemoryMiB   int    `json:"working_memory_mib"`
	StartupSeconds     int    `json:"startup_seconds"`
	StopSeconds        int    `json:"stop_seconds"`
	QuerySeconds       int    `json:"query_seconds"`
	PollMilliseconds   int    `json:"poll_milliseconds"`
	CertificateDays    int    `json:"certificate_days"`
	MaximumLogBytes    int64  `json:"maximum_log_bytes"`
	MaximumConnections int    `json:"maximum_connections"`
}

type Input struct {
	Format            string `json:"format"`
	Binary            string `json:"binary"`
	BinarySHA256      string `json:"binary_sha256"`
	Version           string `json:"version"`
	EvidenceDirectory string `json:"evidence_directory"`
	SeedFile          string `json:"seed_file"`
	SeedSHA256        string `json:"seed_sha256"`
	Policy            Policy `json:"policy"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (p Policy) Validate() error {
	valid := p.ListenHost == "127.0.0.1" && p.Port >= 1024 && p.Port <= 65535 &&
		regexp.MustCompile(`^m21_[a-z0-9_]{1,40}$`).MatchString(p.ReplicaSet) &&
		regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}_test$`).MatchString(p.Database) &&
		regexp.MustCompile(`^m21_[a-z0-9_]{1,40}$`).MatchString(p.Collection) &&
		p.CacheMiB >= 263 && p.CacheMiB <= 512 && p.WorkingMemoryMiB >= 1024 && p.WorkingMemoryMiB <= 2048 &&
		p.WorkingMemoryMiB >= p.CacheMiB+768 && p.StartupSeconds >= 10 && p.StartupSeconds <= 120 &&
		p.StopSeconds >= 5 && p.StopSeconds <= 60 && p.QuerySeconds >= 2 && p.QuerySeconds <= 30 &&
		p.QuerySeconds <= p.StartupSeconds && p.PollMilliseconds >= 50 && p.PollMilliseconds <= 2000 &&
		p.CertificateDays >= 1 && p.CertificateDays <= 7 && p.MaximumLogBytes >= 1<<20 && p.MaximumLogBytes <= 32<<20 &&
		p.MaximumConnections >= 16 && p.MaximumConnections <= 64
	if !valid {
		return errors.New("NATIVE_MONGODB_POLICY_INVALID")
	}
	return nil
}

func hashBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func privateDirectory(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || resolved != path {
		return errors.New("NATIVE_MONGODB_PRIVATE_DIRECTORY_REQUIRED")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode() != os.ModeDir|0700 {
		return errors.New("NATIVE_MONGODB_PRIVATE_DIRECTORY_REQUIRED")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("NATIVE_MONGODB_PRIVATE_DIRECTORY_REQUIRED")
	}
	return nil
}

func fileSHA(path string, maximum int64, executable bool) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || resolved != path {
		return "", errors.New("NATIVE_MONGODB_CANONICAL_FILE_REQUIRED")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("NATIVE_MONGODB_FILE_REQUIRED")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() < 1 || before.Size() > maximum || executable && before.Mode().Perm()&0111 == 0 {
		return "", errors.New("NATIVE_MONGODB_FILE_INVALID")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return "", errors.New("NATIVE_MONGODB_FILE_OWNER_INVALID")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maximum+1))
	after, ae := f.Stat()
	current, ce := os.Lstat(path)
	if err != nil || ae != nil || ce != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || n != before.Size() || after.Size() != n || !after.ModTime().Equal(before.ModTime()) {
		return "", errors.New("NATIVE_MONGODB_FILE_CHANGED")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Load(path string) (Input, string, error) {
	var input Input
	raw, err := config.MongoDBCA(path)
	if err != nil {
		return input, "", errors.New("NATIVE_MONGODB_PRIVATE_INPUT_REQUIRED")
	}
	if wire.DecodeStrict(raw, &input) != nil || input.Format != InputFormat || input.Version != "8.0.34" ||
		!digestPattern.MatchString(input.BinarySHA256) || !digestPattern.MatchString(input.SeedSHA256) || input.Policy.Validate() != nil ||
		!supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		return input, "", errors.New("NATIVE_MONGODB_INPUT_INVALID")
	}
	if err = privateDirectory(input.EvidenceDirectory); err != nil {
		return input, "", err
	}
	if sum, err := fileSHA(input.Binary, 1<<30, true); err != nil || sum != input.BinarySHA256 {
		return input, "", errors.New("NATIVE_MONGODB_BINARY_CHANGED")
	}
	if sum, err := fileSHA(input.SeedFile, 8192, false); err != nil || sum != input.SeedSHA256 {
		return input, "", errors.New("NATIVE_MONGODB_SEED_CHANGED")
	}
	return input, hashBytes(raw), nil
}

// Only the declared native fixtures are qualified; architecture compilation is separate.
func supportedPlatform(goos, goarch string) bool {
	return goos == "darwin" && goarch == "arm64" || goos == "linux" && goarch == "amd64"
}
