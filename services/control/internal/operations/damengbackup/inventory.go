package damengbackup

import (
	"context"
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Tool struct {
	Artifact    Artifact `json:"artifact"`
	VersionLine string   `json:"version_line"`
}
type Inventory struct {
	FormatVersion        int        `json:"format_version"`
	OS                   string     `json:"os"`
	Architecture         string     `json:"architecture"`
	ServerVersion        string     `json:"server_version"`
	Dexp                 Tool       `json:"dexp"`
	Dimp                 Tool       `json:"dimp"`
	Libraries            []Artifact `json:"libraries"`
	Licenses             []Artifact `json:"licenses"`
	TermsReview          Artifact   `json:"terms_review"`
	ServiceConfiguration Artifact   `json:"service_configuration"`
}
type Native struct {
	configuration     Configuration
	inventory         Inventory
	workingDirectory  string
	launchEnvironment map[string]string
}

func (*Native) String() string   { return "DAMENG_RECOVERY_NATIVE_REDACTED" }
func (*Native) GoString() string { return "DAMENG_RECOVERY_NATIVE_REDACTED" }
func validText(v string) bool {
	return len(v) > 0 && len(v) <= 256 && !strings.ContainsAny(v, "\x00\r\n")
}
func validArtifact(a Artifact) bool { return canonical(a.Path) && digest.MatchString(a.SHA256) }
func (i Inventory) Validate() error {
	if i.FormatVersion != FormatVersion || i.OS != "linux" || (i.Architecture != "amd64" && i.Architecture != "arm64") || !validText(i.ServerVersion) || !validText(i.Dexp.VersionLine) || !validText(i.Dimp.VersionLine) || !validArtifact(i.Dexp.Artifact) || !validArtifact(i.Dimp.Artifact) || !validArtifact(i.TermsReview) || !validArtifact(i.ServiceConfiguration) || filepath.Base(i.ServiceConfiguration.Path) != "dm_svc.conf" || len(i.Libraries) < 1 || len(i.Libraries) > 128 || len(i.Licenses) < 1 || len(i.Licenses) > 32 {
		return fail("INVENTORY_INVALID")
	}
	seen := map[string]bool{}
	for _, a := range i.artifacts() {
		if !validArtifact(a) || seen[a.Path] {
			return fail("INVENTORY_INVALID")
		}
		seen[a.Path] = true
	}
	return nil
}
func (i Inventory) artifacts() []Artifact {
	out := []Artifact{i.Dexp.Artifact, i.Dimp.Artifact, i.TermsReview, i.ServiceConfiguration}
	out = append(out, i.Libraries...)
	return append(out, i.Licenses...)
}
func New(ctx context.Context, c Configuration) (*Native, error) {
	return newNative(ctx, c, "", nil)
}
func newNative(ctx context.Context, c Configuration, directory string, environment map[string]string) (*Native, error) {
	if ctx == nil || c.Validate() != nil {
		return nil, fail("CONFIGURATION_INVALID")
	}
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return nil, fail("NONROOT_LINUX_REQUIRED")
	}
	raw, e := readPrivate(c.InventoryFile, maximumDocumentBytes)
	var i Inventory
	if e != nil || sha(raw) != c.InventorySHA256 || strictJSON(raw, &i) != nil || i.Validate() != nil || i.OS != runtime.GOOS || i.Architecture != runtime.GOARCH {
		return nil, fail("INVENTORY_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Policy.OperationSeconds)*time.Second)
	defer cancel()
	n := &Native{configuration: c, inventory: i, workingDirectory: directory, launchEnvironment: environment}
	if e = n.verifyLaunch(); e != nil {
		return nil, e
	}
	if e = n.verify(ctx); e != nil {
		return nil, e
	}
	return n, nil
}
func verifiedArtifact(ctx context.Context, a Artifact, executable bool) error {
	if noSymlinks(a.Path) != nil {
		return fail("ARTIFACT_INVALID")
	}
	f, e := os.Open(a.Path)
	if e != nil {
		return fail("ARTIFACT_MISSING")
	}
	defer f.Close()
	i, e := f.Stat()
	l, le := os.Lstat(a.Path)
	if e != nil || le != nil || !os.SameFile(i, l) || !owned(i, false, false) || i.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || i.Size() < 1 || i.Size() > maximumArtifactBytes || executable && i.Mode().Perm()&0111 == 0 {
		return fail("ARTIFACT_INVALID")
	}
	// Hash an open descriptor; do not trust a filename or an upstream version.
	h := newHash()
	n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, maximumArtifactBytes+1))
	after, se := f.Stat()
	if e != nil || se != nil || n != i.Size() || after.Size() != i.Size() || after.ModTime() != i.ModTime() || hashString(h) != a.SHA256 {
		return fail("ARTIFACT_CHANGED")
	}
	return nil
}
func (n *Native) environment() []string {
	dirs := map[string]bool{}
	for _, a := range n.inventory.Libraries {
		dirs[filepath.Dir(a.Path)] = true
	}
	paths := make([]string, 0, len(dirs))
	for path := range dirs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	// No inherited HOME, PATH, LD_PRELOAD, credential or dm_svc.conf override.
	libraryPath := strings.Join(paths, ":")
	if n.workingDirectory != "" {
		libraryPath = n.launchEnvironment["LD_LIBRARY_PATH"]
	}
	env := []string{"LANG=C", "LC_ALL=C", "LD_LIBRARY_PATH=" + libraryPath, "DM_SVC_PATH=" + filepath.Dir(n.inventory.ServiceConfiguration.Path)}
	for _, key := range []string{"ODBCSYSINI", "ODBCINI", "ODBCINSTINI"} {
		if value, ok := n.launchEnvironment[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func (n *Native) configurationFingerprint() string {
	if n.workingDirectory == "" && len(n.launchEnvironment) == 0 {
		return fingerprint(n.configuration)
	}
	return fingerprint(struct {
		Configuration    Configuration
		WorkingDirectory string
		Environment      map[string]string
	}{n.configuration, n.workingDirectory, n.launchEnvironment})
}
func (n *Native) verifyLaunch() error {
	if n.workingDirectory == "" && len(n.launchEnvironment) == 0 {
		return nil
	}
	if !canonical(n.workingDirectory) || filepath.Base(n.workingDirectory) != "bin" || len(n.launchEnvironment) != 5 || n.launchEnvironment["LD_LIBRARY_PATH"] != n.workingDirectory || n.launchEnvironment["DM_SVC_PATH"] != filepath.Dir(n.inventory.ServiceConfiguration.Path) || n.launchEnvironment["ODBCSYSINI"] != n.launchEnvironment["DM_SVC_PATH"] || n.launchEnvironment["ODBCINI"] != filepath.Join(n.launchEnvironment["DM_SVC_PATH"], "odbc.ini") || n.launchEnvironment["ODBCINSTINI"] != "odbcinst.ini" {
		return fail("NATIVE_LAUNCH_PROFILE_INVALID")
	}
	for _, path := range []string{n.workingDirectory, filepath.Dir(n.workingDirectory), filepath.Join(filepath.Dir(n.workingDirectory), "release"), filepath.Join(n.workingDirectory, "dependencies")} {
		r, e := openPrivateRoot(path)
		if e != nil {
			return fail("NATIVE_LAUNCH_PROFILE_INVALID")
		}
		if filepath.Base(path) == "release" || filepath.Base(path) == "dependencies" {
			f, e := r.Open(".")
			if e != nil {
				r.Close()
				return fail("NATIVE_LAUNCH_PROFILE_INVALID")
			}
			names, e := f.Readdirnames(-1)
			f.Close()
			if e != nil || len(names) != 0 {
				r.Close()
				return fail("NATIVE_LAUNCH_PROFILE_INVALID")
			}
		}
		r.Close()
	}
	return nil
}
func (n *Native) executionDirectory(fallback string) string {
	if n.workingDirectory != "" {
		return n.workingDirectory
	}
	return fallback
}

func (n *Native) verifyServiceConfiguration() error {
	a := n.inventory.ServiceConfiguration
	r, e := openPrivateRoot(filepath.Dir(a.Path))
	if e != nil {
		return fail("SERVICE_CONFIGURATION_INVALID")
	}
	r.Close()
	raw, e := readPrivate(a.Path, 65536)
	if e != nil || sha(raw) != a.SHA256 {
		return fail("SERVICE_CONFIGURATION_INVALID")
	}
	// Match the current native connector's comments-only profile. Explicit
	// CONFIG_FILE supplies the sole connection; global aliases/wallet/autologin,
	// failover and policy overrides cannot silently enter this separate process.
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
			return fail("SERVICE_CONFIGURATION_INVALID")
		}
	}
	return nil
}
func (n *Native) verifyArtifacts(ctx context.Context) error {
	if ctx.Err() != nil {
		return fail("CANCELLED")
	}
	if n.inventory.Validate() != nil {
		return fail("INVENTORY_INVALID")
	}
	if e := n.verifyLaunch(); e != nil {
		return e
	}
	raw, e := readPrivate(n.configuration.InventoryFile, maximumDocumentBytes)
	if e != nil || sha(raw) != n.configuration.InventorySHA256 {
		return fail("INVENTORY_CHANGED")
	}
	for _, a := range n.inventory.artifacts() {
		if e := verifiedArtifact(ctx, a, a == n.inventory.Dexp.Artifact || a == n.inventory.Dimp.Artifact); e != nil {
			return e
		}
	}
	if _, _, e = n.libraryBindings(); e != nil {
		return e
	}
	return n.verifyServiceConfiguration()
}

// A system DSO can be an explicitly pinned regular versioned file while its
// ELF SONAME is a protected loader symlink. Admit that name only when its actual
// resolution is the same pinned file; never infer identity from the name alone.
func (n *Native) libraryBindings() (map[string]string, map[string]bool, error) {
	libs := map[string]string{}
	dirs := map[string]bool{}
	for _, a := range n.inventory.Libraries {
		dir := filepath.Dir(a.Path)
		dirs[dir] = true
		names := []string{filepath.Base(a.Path)}
		f, e := elf.Open(a.Path)
		if e != nil {
			return nil, nil, fail("ELF_REQUIRED")
		}
		sonames, e := f.DynString(elf.DT_SONAME)
		f.Close()
		if e != nil || len(sonames) > 1 {
			return nil, nil, fail("ELF_PROFILE_INVALID")
		}
		if len(sonames) == 1 {
			name := sonames[0]
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "\x00\r\n/\\") {
				return nil, nil, fail("LIBRARY_ALIAS_INVALID")
			}
			if name != names[0] {
				info, e := os.Lstat(dir)
				resolved, re := filepath.EvalSymlinks(filepath.Join(dir, name))
				if e != nil || re != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || (identity(info).UID != 0 && identity(info).UID != uint32(os.Geteuid())) || noSymlinks(dir) != nil || resolved != a.Path {
					return nil, nil, fail("LIBRARY_ALIAS_INVALID")
				}
				names = append(names, name)
			}
		}
		for _, name := range names {
			if old := libs[name]; old != "" && old != a.Path {
				return nil, nil, fail("LIBRARY_AMBIGUOUS")
			}
			libs[name] = a.Path
		}
	}
	if n.workingDirectory != "" {
		dirs[n.workingDirectory] = true
	}
	// All declared ELF search directories must resolve each present alias to the
	// same pinned artifact. A different earlier/shadow library cannot be admitted.
	for name, expected := range libs {
		for dir := range dirs {
			path := filepath.Join(dir, name)
			if _, e := os.Lstat(path); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return nil, nil, fail("LIBRARY_ALIAS_INVALID")
			}
			actual, e := filepath.EvalSymlinks(path)
			if e != nil || actual != expected {
				return nil, nil, fail("LIBRARY_AMBIGUOUS")
			}
		}
	}
	return libs, dirs, nil
}
func (n *Native) verify(ctx context.Context) error {
	if e := n.verifyArtifacts(ctx); e != nil {
		return e
	}
	// Pin the complete ELF dependency closure. A public ODBC manager is not a
	// replacement for either vendor executable or an indispensable native DSO.
	libs, dirs, e := n.libraryBindings()
	if e != nil {
		return e
	}
	for _, a := range append([]Artifact{n.inventory.Dexp.Artifact, n.inventory.Dimp.Artifact}, n.inventory.Libraries...) {
		f, e := elf.Open(a.Path)
		if e != nil {
			return fail("ELF_REQUIRED")
		}
		machine := elf.EM_X86_64
		if n.inventory.Architecture == "arm64" {
			machine = elf.EM_AARCH64
		}
		needed, de := f.ImportedLibraries()
		valid := f.Class == elf.ELFCLASS64 && f.Data == elf.ELFDATA2LSB && f.Machine == machine
		for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
			values, ve := f.DynString(tag)
			if ve != nil {
				valid = false
			}
			for _, value := range values {
				for _, entry := range strings.Split(value, ":") {
					entry = strings.ReplaceAll(strings.ReplaceAll(entry, "${ORIGIN}", filepath.Dir(a.Path)), "$ORIGIN", filepath.Dir(a.Path))
					if n.workingDirectory != "" && (entry == "./" || entry == "../release") {
						continue
					}
					if !canonical(entry) || !dirs[entry] {
						valid = false
					}
				}
			}
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				raw, re := io.ReadAll(io.LimitReader(p.Open(), 4096))
				path, pe := filepath.EvalSymlinks(strings.TrimRight(string(raw), "\x00"))
				found := false
				for _, lib := range n.inventory.Libraries {
					if lib.Path == path {
						found = true
					}
				}
				if re != nil || pe != nil || !found {
					valid = false
				}
			}
		}
		f.Close()
		if de != nil || !valid {
			return fail("ELF_PROFILE_INVALID")
		}
		for _, name := range needed {
			if libs[name] == "" {
				return fail("LIBRARY_CLOSURE_INCOMPLETE")
			}
		}
	}
	for _, item := range []struct {
		tool     Tool
		keywords []string
	}{{n.inventory.Dexp, []string{"CONFIG_FILE", "UNIXSOCKET", "SCHEMAS", "WITH_UR", "FILESIZE", "FILENUM", "NOLOGFILE", "DUMMY"}}, {n.inventory.Dimp, []string{"CONFIG_FILE", "UNIXSOCKET", "SCHEMAS", "IGNORE", "NOLOGFILE", "DUMMY", "FAST_LOAD"}}} {
		probe, cancel := context.WithTimeout(ctx, time.Duration(n.configuration.Policy.ProbeSeconds)*time.Second)
		result, raw, e := runProcess(probe, item.tool.Artifact.Path, []string{"HELP"}, n.executionDirectory(filepath.Dir(item.tool.Artifact.Path)), n.environment(), n.configuration.Policy, nil)
		cancel()
		if e != nil || !result.Started || result.ExitCode != 0 {
			return fail("TOOL_PROBE_FAILED")
		}
		found := false
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == item.tool.VersionLine {
				found = true
			}
		}
		if !found {
			return fail("TOOL_VERSION_MISMATCH")
		}
		for _, key := range item.keywords {
			if !strings.Contains(strings.ToUpper(string(raw)), key) {
				return fail("TOOL_CAPABILITY_MISSING")
			}
		}
	}
	return nil
}
func (n *Native) verifySocket() (FileIdentity, error) {
	c := n.configuration.Connection
	if noSymlinks(c.SocketFile) != nil {
		return FileIdentity{}, fail("SOCKET_INVALID")
	}
	i, e := os.Lstat(c.SocketFile)
	if e != nil {
		return FileIdentity{}, fail("SOCKET_INVALID")
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	parent, e := os.Lstat(filepath.Dir(c.SocketFile))
	if !ok || s.Uid != uint32(c.SocketOwnerUID) || i.Mode()&os.ModeSocket == 0 || i.Mode().Perm()&0077 != 0 || e != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 || identity(parent).UID != uint32(c.SocketOwnerUID) {
		return FileIdentity{}, fail("SOCKET_INVALID")
	}
	return identity(i), nil
}
