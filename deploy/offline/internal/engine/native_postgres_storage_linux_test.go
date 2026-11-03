//go:build linux

package engine

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// A real local Unix socket with coincident paths is deliberately insufficient.
// This is a Go peer refusal fixture, never a successful Docker/host observation.
func TestNativePostgreSQLStorageRefusesLocalProxyPeerBeforeHTTP(t *testing.T) {
	dir, e := os.MkdirTemp(os.Getenv("TMPDIR"), "p")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(dir) })
	socket := filepath.Join(dir, "s")
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, e := listener.AcceptUnix()
		if e == nil {
			accepted <- conn
		} else {
			close(accepted)
		}
	}()
	conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	server := <-accepted
	if server == nil {
		t.Fatal("socket fixture unavailable")
	}
	defer server.Close()
	if _, e = nativePostgreSQLPeerIdentity(conn, socket); e == nil {
		t.Fatal("same-host Go proxy became dockerd authority")
	}
}

func nativePostgreSQLActivatedProcessFixture(pid int32, name string) nativePostgreSQLProcessIdentity {
	return nativePostgreSQLProcessIdentity{PID: pid, Start: "123", ExecutableName: name, UIDMap: "0 0 4294967295", Executable: NativePostgreSQLStorageFilesystem{Device: 1, Inode: uint64(pid) + 10}, Root: NativePostgreSQLStorageFilesystem{Device: 1, Inode: 2}, ExecutableMode: syscall.S_IFREG | 0755, HaveUIDs: true, HaveGIDs: true, Namespaces: [3]string{"mnt:[1]", "user:[2]", "pid:[3]"}}
}

func TestNativePostgreSQLStorageActivatedListenerUsesExactKernelCustody(t *testing.T) {
	path := "/run/selected.sock"
	header := "Num RefCount Protocol Flags Type St Inode Path\n"
	listener := "00000000: 00000002 00000000 00010000 0001 01 913 " + path + "\n"
	connected := "00000000: 00000002 00000000 00000000 0001 03 914 " + path + "\n"
	inode, e := nativePostgreSQLUnixListener([]byte(header+listener+connected), []string{path})
	if e != nil || inode != 913 {
		t.Fatal("exact kernel listener refused", e)
	}
	for _, raw := range []string{header, header + listener + listener, header + connected, header + "00000000: 2 00000000 00010000 0002 01 913 " + path + "\n"} {
		if _, e := nativePostgreSQLUnixListener([]byte(raw), []string{path}); e == nil {
			t.Fatal("missing/duplicate/nonstream listener accepted")
		}
	}
	activator := nativePostgreSQLActivatedProcessFixture(1, "systemd")
	daemon := nativePostgreSQLActivatedProcessFixture(77, "dockerd")
	holders := map[int32][]string{1: {"12"}, 77: {"5"}}
	read := func(pid int32) (nativePostgreSQLProcessIdentity, error) {
		if pid != daemon.PID {
			return nativePostgreSQLProcessIdentity{}, errors.New("unbound PID")
		}
		return daemon, nil
	}
	actual, e := nativePostgreSQLActivatedDaemon(activator, inode, holders, read)
	if e != nil || !reflect.DeepEqual(actual, daemon) {
		t.Fatal("original activated daemon refused", e)
	}
	for _, fault := range []string{"missing-listener", "foreign-holder", "missing-activator", "stale-pid", "stale-exe", "namespace", "root", "uid-map", "unprivileged", "missing-ids", "unreadable", "empty-fd"} {
		t.Run(fault, func(t *testing.T) {
			a := activator
			d := daemon
			i := inode
			h := map[int32][]string{1: {"12"}, 77: {"5"}}
			var readError error
			switch fault {
			case "missing-listener":
				i = 0
			case "foreign-holder":
				h[88] = []string{"9"}
			case "missing-activator":
				delete(h, 1)
			case "stale-pid":
				d.PID = 78
			case "stale-exe":
				d.ExecutableName = "proxy"
			case "namespace":
				d.Namespaces[0] = "mnt:[foreign]"
			case "root":
				d.Root.Inode++
			case "uid-map":
				d.UIDMap = "0 1000 1"
			case "unprivileged":
				d.UIDs[1] = 1000
			case "missing-ids":
				d.HaveUIDs = false
			case "unreadable":
				readError = errors.New("selected metadata unavailable")
			case "empty-fd":
				h[77] = nil
			}
			if _, e := nativePostgreSQLActivatedDaemon(a, i, h, func(int32) (nativePostgreSQLProcessIdentity, error) { return d, readError }); e == nil {
				t.Fatal("unproven activation accepted", fault)
			}
		})
	}
}

func TestNativePostgreSQLStorageActivatedCustodyCannotChangeBetweenRequests(t *testing.T) {
	activator := nativePostgreSQLActivatedProcessFixture(1, "systemd")
	daemon := nativePostgreSQLActivatedProcessFixture(77, "dockerd")
	current := nativePostgreSQLDaemonPeer{PID: 1, Process: activator, ActivatedDaemon: &daemon, ListenerKernelInode: 913, ListenerHolders: map[int32][]string{1: {"12"}, 77: {"5"}}}
	var original *nativePostgreSQLDaemonPeer
	if e := nativePostgreSQLBindOriginalPeer(&original, current); e != nil {
		t.Fatal(e)
	}
	if e := nativePostgreSQLBindOriginalPeer(&original, current); e != nil {
		t.Fatal(e)
	}
	current.ActivatedDaemon.Start = "124"
	if e := nativePostgreSQLBindOriginalPeer(&original, current); e == nil {
		t.Fatal("changed original daemon start identity accepted")
	}
	current.ActivatedDaemon.Start = "123"
	current.ListenerHolders[77][0] = "6"
	if e := nativePostgreSQLBindOriginalPeer(&original, current); e == nil {
		t.Fatal("changed original listener FD custody accepted")
	}
}

func TestNativePostgreSQLStorageDurableRoleRejectsMemoryAndUnprovenBacking(t *testing.T) {
	for _, kind := range []int64{0x01021994, 0x858458f6, 0x794c7630, 0x9123683e, 0} {
		if nativePostgreSQLDurableFilesystemType(kind) {
			t.Fatal("ephemeral/virtual/unproven filesystem admitted", kind)
		}
	}
	for _, kind := range []int64{0xef53, 0x58465342} {
		if !nativePostgreSQLDurableFilesystemType(kind) {
			t.Fatal("supported native filesystem refused", kind)
		}
	}
	for _, device := range []uint64{0, 7<<8 | 1} {
		if e := nativePostgreSQLDirectBlockDevice(device); e == nil {
			t.Fatal("RAM/loop backing became durable", device)
		}
	}
}

func TestNativePostgreSQLStorageDirectoryRefusesAliasAndWritableRoot(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(dir, "alias")
	if e := os.Symlink(dir, alias); e != nil {
		t.Fatal(e)
	}
	if _, e := nativePostgreSQLStorageDirectory(alias); e == nil {
		t.Fatal("symlink root accepted")
	}
	if e := os.Chmod(dir, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := nativePostgreSQLStorageDirectory(dir); e == nil {
		t.Fatal("writable root accepted")
	}
	a := NativePostgreSQLStorageFilesystem{Device: 1, Inode: 2, TotalBytes: 100, AvailableBytes: 70, AllocationUnitBytes: 4}
	b := a
	b.AvailableBytes--
	if !nativePostgreSQLSameStorageIdentity(a, b) {
		t.Fatal("fresh free capacity treated as immutable identity")
	}
	b.Inode++
	if nativePostgreSQLSameStorageIdentity(a, b) {
		t.Fatal("replaced directory accepted")
	}
}

func TestNativePostgreSQLStorageRefusesNestedBindEvenOnSameDevice(t *testing.T) {
	ordinary := []byte("36 25 8:1 / / rw - ext4 /dev/example rw\n")
	if e := nativePostgreSQLStorageMountLayoutBytes(ordinary, "/docker/volumes"); e != nil {
		t.Fatal(e)
	}
	for _, mount := range []string{"/docker/volumes", "/docker/volumes/owned/_data", "/docker/volumes/owned/_data/subtree"} {
		raw := append(append([]byte(nil), ordinary...), []byte("37 36 8:1 /foreign "+mount+" rw - ext4 /dev/example rw\n")...)
		if e := nativePostgreSQLStorageMountLayoutBytes(raw, "/docker/volumes"); e == nil {
			t.Fatal("nested same-device bind inferred as original backing", mount)
		}
	}
}
