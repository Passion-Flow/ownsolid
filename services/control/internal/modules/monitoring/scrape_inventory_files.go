package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const maxScrapeTargets = 10_000

var scrapePlacement = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

type scrapePolicy struct {
	FormatVersion   int               `json:"format_version"`
	WorkspaceID     string            `json:"workspace_id"`
	AllowedNetworks []string          `json:"allowed_networks"`
	Endpoints       map[string]string `json:"endpoints"`
}

type scrapeTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

func readScrapePolicy(path, workspace string) (map[string]string, error) {
	bad := errors.New("MONITORING_SCRAPE_POLICY_INVALID")
	raw, e := bundle.ReadProtectedConfiguration(path)
	var p scrapePolicy
	if e != nil || release.StrictJSON(raw, &p) != nil || p.FormatVersion != 1 || p.WorkspaceID != workspace ||
		len(p.AllowedNetworks) < 1 || len(p.AllowedNetworks) > 64 || p.Endpoints == nil || len(p.Endpoints) > maxScrapeTargets {
		return nil, bad
	}
	prefixes := make([]netip.Prefix, 0, len(p.AllowedNetworks))
	for _, raw := range p.AllowedNetworks {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil || prefix.Masked().String() != raw || !privateScrapeAddress(prefix.Addr()) {
			return nil, bad
		}
		prefixes = append(prefixes, prefix)
	}
	addresses := make(map[netip.AddrPort]struct{}, len(p.Endpoints))
	for node, endpoint := range p.Endpoints {
		address, e := netip.ParseAddrPort(endpoint)
		if e != nil || address.String() != endpoint || address.Port() == 0 ||
			!privateScrapeAddress(address.Addr()) || !scrapeNetworkAllows(prefixes, address.Addr()) || !validID(node) {
			return nil, bad
		}
		if _, duplicate := addresses[address]; duplicate {
			return nil, bad
		}
		addresses[address] = struct{}{}
	}
	return p.Endpoints, nil
}

func privateScrapeAddress(address netip.Addr) bool {
	return !address.Is4In6() && address.Zone() == "" && (address.IsPrivate() || address.IsLoopback())
}

func scrapeNetworkAllows(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(address) {
			return true
		}
	}
	return false
}

func openScrapeDirectory(path string) (*os.Root, *os.File, error) {
	bad := errors.New("MONITORING_SCRAPE_DIRECTORY_INVALID")
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || resolved != path {
		return nil, nil, bad
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, nil, bad
	}
	dir, e := root.Open(".")
	if e != nil {
		root.Close()
		return nil, nil, bad
	}
	info, e := dir.Stat()
	if e == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Geteuid()) {
			e = bad
		}
	}
	if e != nil {
		dir.Close()
		root.Close()
		return nil, nil, bad
	}
	if e := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		dir.Close()
		root.Close()
		if errors.Is(e, unix.EWOULDBLOCK) {
			return nil, nil, errors.New("MONITORING_SCRAPE_PUBLISH_BUSY")
		}
		return nil, nil, bad
	}
	return root, dir, nil
}

func writeScrapeInventory(ctx context.Context, root *os.Root, dir *os.File, workspace string, groups []scrapeTarget) error {
	bad := errors.New("MONITORING_SCRAPE_PUBLISH_FAILED")
	name := ".nodes-" + uuid.NewString() + ".tmp"
	file, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return bad
	}
	defer root.Remove(name)
	e = file.Chmod(0600)
	if e == nil {
		e = json.NewEncoder(file).Encode(groups)
	}
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return bad
	}
	if e := ctx.Err(); e != nil {
		return safeError(e)
	}
	if e := root.Rename(name, "nodes-"+workspace+".json"); e != nil {
		return bad
	}
	if e := dir.Sync(); e != nil {
		return errors.New("MONITORING_SCRAPE_PUBLISH_UNCERTAIN")
	}
	return nil
}
