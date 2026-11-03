//go:build linux

package engine

import (
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type nativePostgreSQLProcessIdentity struct {
	PID                           int32
	Start, ExecutableName, UIDMap string
	Executable, Root              NativePostgreSQLStorageFilesystem
	ExecutableMode, ExecutableUID uint32
	UIDs, GIDs                    [4]uint32
	HaveUIDs, HaveGIDs            bool
	Namespaces                    [3]string
}

type nativePostgreSQLDaemonPeer struct {
	PID                 int32
	Socket              NativePostgreSQLStorageFilesystem
	Process             nativePostgreSQLProcessIdentity
	ActivatedDaemon     *nativePostgreSQLProcessIdentity
	ListenerKernelInode uint64
	ListenerHolders     map[int32][]string
}

func nativePostgreSQLStorageDirectory(path string) (NativePostgreSQLStorageFilesystem, error) {
	var out NativePostgreSQLStorageFilesystem
	resolved, e := filepath.EvalSymlinks(path)
	var st syscall.Stat_t
	var fs syscall.Statfs_t
	if e != nil || resolved != path || !filepath.IsAbs(path) || filepath.Clean(path) != path || syscall.Lstat(path, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFDIR || st.Uid != 0 || st.Mode&0022 != 0 || syscall.Statfs(path, &fs) != nil || fs.Bsize <= 0 || fs.Blocks == 0 || fs.Bavail > fs.Blocks || uint64(fs.Bsize) > math.MaxInt64/fs.Blocks {
		return out, failure("OFFLINE_ENGINE_NATIVE_STORAGE_HOST_ROOT_UNPROVEN")
	}
	if !nativePostgreSQLDurableFilesystemType(fs.Type) || nativePostgreSQLDirectBlockDevice(uint64(st.Dev)) != nil {
		return out, failure("OFFLINE_ENGINE_NATIVE_STORAGE_DURABLE_BACKING_REQUIRED")
	}
	return NativePostgreSQLStorageFilesystem{BackingRole: NativePostgreSQLDurableBacking, FilesystemType: fs.Type, Device: uint64(st.Dev), Inode: st.Ino, TotalBytes: int64(fs.Blocks) * fs.Bsize, AvailableBytes: int64(fs.Bavail) * fs.Bsize, AllocationUnitBytes: fs.Bsize}, nil
}

func nativePostgreSQLDurableDirectory(path string) (NativePostgreSQLStorageFilesystem, error) {
	return nativePostgreSQLStorageDirectory(path)
}

func nativePostgreSQLDurableFilesystemType(kind int64) bool {
	return kind == 0xef53 || kind == 0x58465342
}

func nativePostgreSQLDirectBlockDevice(device uint64) error {
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_DURABLE_BACKING_REQUIRED")
	major := (device>>8)&0xfff | (device>>32)&0xfffff000
	minor := device&0xff | (device>>12)&0xffffff00
	if major == 0 || major == 7 {
		return bad
	}
	identity := strconv.FormatUint(major, 10) + ":" + strconv.FormatUint(minor, 10)
	root, e := filepath.EvalSymlinks("/sys/dev/block/" + identity)
	if e != nil || !strings.HasPrefix(root, "/sys/devices/") {
		return bad
	}
	raw, e := nativePostgreSQLKernelBytes(root+"/dev", 128)
	if e != nil || strings.TrimSpace(string(raw)) != identity {
		return bad
	}
	for current := root; strings.HasPrefix(current, "/sys/devices/"); current = filepath.Dir(current) {
		for _, name := range []string{"loop", "dm"} {
			if _, e := os.Lstat(current + "/" + name); e == nil || !os.IsNotExist(e) {
				return bad
			}
		}
	}
	return nil
}

// SO_PEERCRED records credentials at listen/connect time. A standard activated
// socket therefore identifies systemd; it grants no daemon authority by itself.
// See unix(7), proc_pid_fd(5) and systemd sd_listen_fds(3).
func nativePostgreSQLPeerIdentity(conn *net.UnixConn, socket string) (nativePostgreSQLDaemonPeer, error) {
	return nativePostgreSQLPeerIdentityContext(context.Background(), conn, socket, socket)
}

func nativePostgreSQLPeerIdentityContext(ctx context.Context, conn *net.UnixConn, socket, configured string) (nativePostgreSQLDaemonPeer, error) {
	var out nativePostgreSQLDaemonPeer
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_DAEMON_PEER_UNPROVEN")
	var cred *syscall.Ucred
	var ce error
	fd, e := conn.SyscallConn()
	if e != nil {
		return out, bad
	}
	if e = fd.Control(func(handle uintptr) {
		cred, ce = syscall.GetsockoptUcred(int(handle), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); e != nil || ce != nil || cred == nil || cred.Pid < 1 || cred.Uid != 0 || cred.Gid != 0 {
		return out, bad
	}
	var st syscall.Stat_t
	resolved, e := filepath.EvalSymlinks(socket)
	if e != nil || resolved != socket || syscall.Lstat(socket, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFSOCK || st.Uid != 0 || st.Mode&0002 != 0 {
		return out, bad
	}
	out.PID = cred.Pid
	out.Socket = NativePostgreSQLStorageFilesystem{Device: uint64(st.Dev), Inode: st.Ino}
	process, e := nativePostgreSQLReadProcess(cred.Pid)
	if e != nil {
		return out, e
	}
	out.Process = process
	if validNativePostgreSQLProcess(process, "dockerd") {
		return out, nil
	}
	if process.PID != 1 || !validNativePostgreSQLProcess(process, "systemd") {
		return out, bad
	}
	raw, e := nativePostgreSQLKernelBytes("/proc/net/unix", 16<<20)
	if e != nil {
		return out, e
	}
	inode, e := nativePostgreSQLUnixListener(raw, []string{socket, configured})
	if e != nil {
		return out, e
	}
	holders, e := nativePostgreSQLListenerHolders(ctx, inode)
	if e != nil {
		return out, e
	}
	daemon, e := nativePostgreSQLActivatedDaemon(process, inode, holders, nativePostgreSQLReadProcess)
	if e != nil {
		return out, e
	}
	out.ActivatedDaemon = &daemon
	out.ListenerKernelInode = inode
	out.ListenerHolders = holders
	return out, nil
}

func nativePostgreSQLKernelBytes(path string, maximum int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, failure("OFFLINE_ENGINE_NATIVE_STORAGE_KERNEL_METADATA_UNAVAILABLE")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	if e != nil || int64(len(raw)) > maximum {
		return nil, failure("OFFLINE_ENGINE_NATIVE_STORAGE_KERNEL_METADATA_UNAVAILABLE")
	}
	return raw, nil
}

func validNativePostgreSQLProcess(p nativePostgreSQLProcessIdentity, name string) bool {
	if p.PID < 1 || p.ExecutableName != name || p.ExecutableUID != 0 || p.ExecutableMode&syscall.S_IFMT != syscall.S_IFREG || p.ExecutableMode&0022 != 0 || p.ExecutableMode&0111 == 0 || p.Executable.Device == 0 || p.Executable.Inode == 0 || p.Root.Device == 0 || p.Root.Inode == 0 || p.UIDMap != "0 0 4294967295" || !p.HaveUIDs || !p.HaveGIDs {
		return false
	}
	start, e := strconv.ParseUint(p.Start, 10, 64)
	if e != nil || start == 0 {
		return false
	}
	for _, id := range append(p.UIDs[:], p.GIDs[:]...) {
		if id != 0 {
			return false
		}
	}
	for _, ns := range p.Namespaces {
		if ns == "" {
			return false
		}
	}
	return true
}

func nativePostgreSQLReadProcess(pid int32) (nativePostgreSQLProcessIdentity, error) {
	var out nativePostgreSQLProcessIdentity
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_PROCESS_IDENTITY_UNPROVEN")
	out.PID = pid
	proc := "/proc/" + strconv.Itoa(int(pid))
	exe, e := os.Readlink(proc + "/exe")
	var st syscall.Stat_t
	if e != nil || !filepath.IsAbs(exe) || syscall.Stat(proc+"/exe", &st) != nil {
		return out, bad
	}
	out.ExecutableName = filepath.Base(exe)
	out.ExecutableMode = st.Mode
	out.ExecutableUID = st.Uid
	out.Executable = NativePostgreSQLStorageFilesystem{Device: uint64(st.Dev), Inode: st.Ino}
	for i, name := range []string{"mnt", "user", "pid"} {
		peer, pe := os.Readlink(proc + "/ns/" + name)
		self, se := os.Readlink("/proc/self/ns/" + name)
		initial, ie := os.Readlink("/proc/1/ns/" + name)
		if pe != nil || se != nil || ie != nil || peer == "" || peer != self || peer != initial {
			return out, bad
		}
		out.Namespaces[i] = peer
	}
	uidMap, e := nativePostgreSQLKernelBytes(proc+"/uid_map", 1024)
	if e != nil {
		return out, e
	}
	out.UIDMap = strings.Join(strings.Fields(string(uidMap)), " ")
	selfMap, e := nativePostgreSQLKernelBytes("/proc/self/uid_map", 1024)
	if e != nil || out.UIDMap != strings.Join(strings.Fields(string(selfMap)), " ") {
		return out, bad
	}
	var ownRoot syscall.Stat_t
	if syscall.Stat(proc+"/root", &st) != nil || syscall.Stat("/", &ownRoot) != nil || st.Dev != ownRoot.Dev || st.Ino != ownRoot.Ino {
		return out, bad
	}
	out.Root = NativePostgreSQLStorageFilesystem{Device: uint64(st.Dev), Inode: st.Ino}
	raw, e := nativePostgreSQLKernelBytes(proc+"/stat", 65536)
	if e != nil {
		return out, e
	}
	at := strings.LastIndex(string(raw), ") ")
	if at < 0 {
		return out, bad
	}
	fields := strings.Fields(string(raw[at+2:]))
	if len(fields) < 20 {
		return out, bad
	}
	out.Start = fields[19]
	raw, e = nativePostgreSQLKernelBytes(proc+"/status", 65536)
	if e != nil {
		return out, e
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "Uid" && key != "Gid" {
			continue
		}
		f := strings.Fields(value)
		if len(f) != 4 {
			return out, bad
		}
		var ids [4]uint32
		for i, text := range f {
			n, e := strconv.ParseUint(text, 10, 32)
			if e != nil {
				return out, bad
			}
			ids[i] = uint32(n)
		}
		if key == "Uid" {
			if out.HaveUIDs {
				return out, bad
			}
			out.HaveUIDs = true
			out.UIDs = ids
		} else {
			if out.HaveGIDs {
				return out, bad
			}
			out.HaveGIDs = true
			out.GIDs = ids
		}
	}
	if !validNativePostgreSQLProcess(out, out.ExecutableName) {
		return out, bad
	}
	return out, nil
}

func nativePostgreSQLUnixListener(raw []byte, paths []string) (uint64, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_EXACT_LISTENER_REQUIRED")
	var inode uint64
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "Num") {
		return 0, bad
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 7 {
			return 0, bad
		}
		if len(f) != 8 {
			continue
		}
		selected := false
		for _, path := range paths {
			selected = selected || path != "" && f[7] == path
		}
		if !selected {
			continue
		}
		if f[4] != "0001" {
			return 0, bad
		}
		flags, e := strconv.ParseUint(f[3], 16, 32)
		if e != nil {
			return 0, bad
		}
		if flags == 0 && f[5] == "03" {
			continue
		}
		if flags != 0x10000 || f[5] != "01" || f[2] != "00000000" || inode != 0 {
			return 0, bad
		}
		inode, e = strconv.ParseUint(f[6], 10, 64)
		if e != nil || inode == 0 {
			return 0, bad
		}
	}
	if inode == 0 {
		return 0, bad
	}
	return inode, nil
}

func nativePostgreSQLListenerHolders(ctx context.Context, inode uint64) (map[int32][]string, error) {
	out := map[int32][]string{}
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_LISTENER_CUSTODY_UNPROVEN")
	target := "socket:[" + strconv.FormatUint(inode, 10) + "]"
	proc, e := os.Open("/proc")
	if e != nil {
		return nil, bad
	}
	defer proc.Close()
	for {
		entries, e := proc.ReadDir(128)
		if e != nil && e != io.EOF {
			return nil, bad
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return nil, bad
			}
			n, pe := strconv.ParseInt(entry.Name(), 10, 32)
			if pe != nil || n < 1 {
				continue
			}
			pid := int32(n)
			fdRoot := "/proc/" + entry.Name() + "/fd"
			directory, de := os.Open(fdRoot)
			if de != nil {
				if os.IsNotExist(de) && syscall.Kill(int(pid), 0) == syscall.ESRCH {
					continue
				}
				return nil, bad
			}
			gone := false
			for {
				fds, fe := directory.ReadDir(128)
				if fe != nil && fe != io.EOF {
					directory.Close()
					return nil, bad
				}
				for _, fd := range fds {
					value, re := os.Readlink(fdRoot + "/" + fd.Name())
					if re != nil {
						if os.IsNotExist(re) && syscall.Kill(int(pid), 0) == syscall.ESRCH {
							gone = true
							break
						}
						directory.Close()
						return nil, bad
					}
					if value == target {
						out[pid] = append(out[pid], fd.Name())
					}
				}
				if gone || fe == io.EOF {
					break
				}
				if ctx.Err() != nil {
					directory.Close()
					return nil, bad
				}
			}
			if ce := directory.Close(); ce != nil {
				return nil, bad
			}
			if gone {
				delete(out, pid)
			}
		}
		if e == io.EOF {
			break
		}
	}
	for pid := range out {
		sort.Strings(out[pid])
	}
	return out, nil
}

func nativePostgreSQLActivatedDaemon(activator nativePostgreSQLProcessIdentity, inode uint64, holders map[int32][]string, read func(int32) (nativePostgreSQLProcessIdentity, error)) (nativePostgreSQLProcessIdentity, error) {
	var out nativePostgreSQLProcessIdentity
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_ORIGINAL_ACTIVATED_DAEMON_REQUIRED")
	if activator.PID != 1 || !validNativePostgreSQLProcess(activator, "systemd") || inode == 0 || len(holders) != 2 || len(holders[1]) == 0 {
		return out, bad
	}
	var pid int32
	for holder, fds := range holders {
		if len(fds) == 0 {
			return out, bad
		}
		if holder != 1 {
			pid = holder
		}
	}
	if pid < 2 {
		return out, bad
	}
	out, e := read(pid)
	if e != nil || out.PID != pid || !validNativePostgreSQLProcess(out, "dockerd") || out.Root != activator.Root || out.Namespaces != activator.Namespaces || out.UIDMap != activator.UIDMap {
		return nativePostgreSQLProcessIdentity{}, bad
	}
	return out, nil
}

func nativePostgreSQLBindOriginalPeer(original **nativePostgreSQLDaemonPeer, current nativePostgreSQLDaemonPeer) error {
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_DAEMON_CUSTODY_CHANGED")
	if original == nil {
		return bad
	}
	if *original != nil {
		if !reflect.DeepEqual(**original, current) {
			return bad
		}
		return nil
	}
	retained := current
	if current.ActivatedDaemon != nil {
		copy := *current.ActivatedDaemon
		retained.ActivatedDaemon = &copy
	}
	if current.ListenerHolders != nil {
		retained.ListenerHolders = map[int32][]string{}
		for pid, fds := range current.ListenerHolders {
			retained.ListenerHolders[pid] = append([]string(nil), fds...)
		}
	}
	*original = &retained
	return nil
}

func (c *Client) observeDirectNativePostgreSQLStorage(ctx context.Context, task DatabaseTask) (NativePostgreSQLStorageObservation, error) {
	var out NativePostgreSQLStorageObservation
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_HOST_BACKING_UNPROVEN")
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.config.RequestSeconds)*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: time.Duration(c.config.DialSeconds) * time.Second}
	var identity *nativePostgreSQLDaemonPeer
	var identityMu sync.Mutex
	// All requests use a fresh connection whose kernel peer is the same original
	// root dockerd process. A stale pooled socket cannot supply a different info.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 1, DialContext: func(call context.Context, _, _ string) (net.Conn, error) {
		conn, e := dialer.DialContext(call, "unix", c.socket)
		if e != nil {
			return nil, bad
		}
		unix, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			return nil, bad
		}
		peer, e := nativePostgreSQLPeerIdentityContext(call, unix, c.socket, c.config.SocketFile)
		identityMu.Lock()
		defer identityMu.Unlock()
		if e != nil || nativePostgreSQLBindOriginalPeer(&identity, peer) != nil {
			conn.Close()
			return nil, bad
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	copy := *c
	copy.http = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return bad }}
	preflight, e := copy.Preflight(ctx)
	if e != nil || preflight.Status != NativeStatus {
		return out, bad
	}
	before, e := copy.nativePostgreSQLStorageInfo(ctx)
	if e != nil || wire.Digest([]byte(before.ID)) != preflight.EngineSHA256 {
		return out, bad
	}
	root, e := nativePostgreSQLStorageDirectory(before.DockerRootDir)
	if e != nil {
		return out, e
	}
	allocation := filepath.Join(before.DockerRootDir, "volumes")
	volumeRoot, e := nativePostgreSQLStorageDirectory(allocation)
	if e != nil || root.Device != volumeRoot.Device || root.TotalBytes != volumeRoot.TotalBytes || root.AllocationUnitBytes != volumeRoot.AllocationUnitBytes {
		return out, bad
	}
	if e = nativePostgreSQLStorageMountLayout(allocation); e != nil {
		return out, e
	}
	out = NativePostgreSQLStorageObservation{Status: NativePostgreSQLStorageObserved, EngineSHA256: preflight.EngineSHA256, EndpointSHA256: preflight.EndpointSHA256, Filesystem: volumeRoot, AllocationRoot: allocation, Qualification: "readonly_kernel_peer_and_actual_host_local_volume_backing_only_not_reserved_capacity_native_scratch_quota_SQL_or_target_acceptance"}
	for _, owner := range nativePostgreSQLStorageSourceOwners(task) {
		mountpoint, e := copy.nativePostgreSQLStorageVolume(ctx, owner, allocation)
		if e != nil {
			return NativePostgreSQLStorageObservation{}, e
		}
		fs, e := nativePostgreSQLStorageDirectory(mountpoint)
		if e != nil || fs.Device != volumeRoot.Device || fs.TotalBytes != volumeRoot.TotalBytes || fs.AllocationUnitBytes != volumeRoot.AllocationUnitBytes {
			return NativePostgreSQLStorageObservation{}, bad
		}
		out.SourceOwners = append(out.SourceOwners, NativePostgreSQLStorageOwner{Owner: owner, Filesystem: fs, Mountpoint: mountpoint})
	}
	// Observe every original source identity again after all path/capacity reads.
	for _, owner := range out.SourceOwners {
		path, e := copy.nativePostgreSQLStorageVolume(ctx, owner.Owner, allocation)
		fs, fe := nativePostgreSQLStorageDirectory(path)
		if e != nil || fe != nil || path != owner.Mountpoint || !nativePostgreSQLSameStorageIdentity(fs, owner.Filesystem) {
			return NativePostgreSQLStorageObservation{}, bad
		}
	}
	after, e := copy.nativePostgreSQLStorageInfo(ctx)
	lastRoot, re := nativePostgreSQLStorageDirectory(before.DockerRootDir)
	lastVolume, ve := nativePostgreSQLStorageDirectory(allocation)
	if e != nil || re != nil || ve != nil || !reflect.DeepEqual(before, after) || !nativePostgreSQLSameStorageIdentity(root, lastRoot) || !nativePostgreSQLSameStorageIdentity(volumeRoot, lastVolume) {
		return NativePostgreSQLStorageObservation{}, bad
	}
	if e = nativePostgreSQLStorageMountLayout(allocation); e != nil {
		return NativePostgreSQLStorageObservation{}, e
	}
	finalPeer, e := transport.DialContext(ctx, "unix", c.socket)
	if e != nil {
		return NativePostgreSQLStorageObservation{}, bad
	}
	if e = finalPeer.Close(); e != nil {
		return NativePostgreSQLStorageObservation{}, bad
	}
	identityMu.Lock()
	out.PeerProof = "kernel_root_dockerd"
	if identity != nil && identity.ActivatedDaemon != nil {
		out.PeerProof = "kernel_original_systemd_dockerd_listener"
	}
	identityMu.Unlock()
	out.Filesystem.AvailableBytes = min(out.Filesystem.AvailableBytes, lastVolume.AvailableBytes)
	return out, nil
}

func nativePostgreSQLSameStorageIdentity(a, b NativePostgreSQLStorageFilesystem) bool {
	return a.BackingRole == b.BackingRole && a.FilesystemType == b.FilesystemType && a.Device == b.Device && a.Inode == b.Inode && a.TotalBytes == b.TotalBytes && a.AllocationUnitBytes == b.AllocationUnitBytes
}

func nativePostgreSQLStorageMountLayout(allocation string) error {
	f, e := os.Open("/proc/self/mountinfo")
	if e != nil {
		return failure("OFFLINE_ENGINE_NATIVE_STORAGE_MOUNT_LAYOUT_UNPROVEN")
	}
	defer f.Close()
	// Read kernel mount metadata only, never source-volume file contents/options.
	raw, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if e != nil || len(raw) > 16<<20 {
		return failure("OFFLINE_ENGINE_NATIVE_STORAGE_MOUNT_LAYOUT_UNPROVEN")
	}
	return nativePostgreSQLStorageMountLayoutBytes(raw, allocation)
}

func nativePostgreSQLStorageMountLayoutBytes(raw []byte, allocation string) error {
	bad := failure("OFFLINE_ENGINE_NATIVE_STORAGE_NESTED_MOUNT_UNSUPPORTED")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return bad
		}
		mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, "\\").Replace(fields[4])
		if mount == allocation || strings.HasPrefix(mount, allocation+"/") {
			return bad
		}
	}
	return nil
}
