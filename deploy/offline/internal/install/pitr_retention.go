package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

var pitrWALName = regexp.MustCompile(`^[0-9A-F]{24}$`)

// PITRPrune expires owned bases before a verified full cutoff, except where
// they are dependencies of retained backups. It then expires older WAL.
func PITRPrune(ctx context.Context, c Config, runner StreamRunner, cutoff string, out io.Writer) error {
	if !pitrID.MatchString(cutoff) {
		return errors.New("INSTALL_PITR_ID_INVALID: specify the full base to keep from")
	}
	return withPITR(ctx, c, runner, out, func(ctx context.Context, j *pitrJob) error {
		if err := j.prune(ctx, cutoff); err != nil {
			j.record("pitr-prune", "failed", err.Error())
			return err
		}
		return nil
	})
}

func (j *pitrJob) prune(ctx context.Context, cutoff string) error {
	bases, err := j.ownedBases()
	if err != nil {
		return err
	}
	retain, floor, err := pitrRetainedBases(bases, cutoff)
	if err != nil {
		return err
	}
	var keep, expire []string
	for id := range bases {
		if retain[id] {
			keep = append(keep, id)
		} else {
			expire = append(expire, id)
		}
	}
	slices.Sort(keep)
	slices.Sort(expire)
	for _, id := range keep {
		if err := j.verifyChain(ctx, []pitrBase{bases[id]}); err != nil {
			return err
		}
	}
	walDir := filepath.Join(pitrSpool, "wal", j.source.cluster)
	if err := pitrDirectory(j.repo.root, walDir, j.config.Runtime.PostgresUID, 0o700); err != nil {
		return err
	}
	timeline := fmt.Sprintf("%08X", bases[cutoff].Timeline)
	floorName, err := j.query(ctx, maintenanceDatabase, fmt.Sprintf(
		"SELECT pg_walfile_name('%X/%X'::pg_lsn)", floor>>32, uint32(floor)))
	if err != nil || !pitrWALName.MatchString(floorName) || !strings.HasPrefix(floorName, timeline) {
		return errors.New("INSTALL_PITR_WAL_FLOOR_INVALID: verify the retained bases belong to the active PostgreSQL timeline")
	}
	sizeText, err := j.query(ctx, maintenanceDatabase, "SELECT pg_size_bytes(current_setting('wal_segment_size'))")
	size, parseErr := strconv.ParseUint(sizeText, 10, 32)
	if err != nil || parseErr != nil || size < 1<<20 || size > 1<<30 || size&(size-1) != 0 {
		return errors.New("INSTALL_PITR_WAL_FLOOR_INVALID: cannot verify PostgreSQL WAL segment size")
	}
	wal, err := j.walExpiry(walDir, floorName, size)
	if err != nil {
		return err
	}
	// Remove expired catalog names durably before deleting any WAL that
	// could have been needed by one of those bases.
	for _, id := range expire {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := randomBytes(12)
		if err != nil {
			return err
		}
		removed := filepath.Join(pitrBases, fmt.Sprintf(".expired-%x", token))
		if err := j.repo.root.Rename(filepath.Join(pitrBases, id), removed); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Join(j.repo.path, pitrBases)); err != nil {
			return err
		}
		if err := j.repo.root.RemoveAll(removed); err != nil {
			return err
		}
	}
	for _, segment := range wal {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := j.repo.root.Remove(filepath.Join(walDir, segment)); err != nil {
			return err
		}
	}
	if err := syncDirectory(j.repo.wal(j.source.cluster)); err != nil {
		return err
	}
	j.record("pitr-prune", "complete", cutoff)
	fmt.Fprintf(j.out, "PITR_PRUNE_COMPLETE expired_bases=%d expired_wal=%d keep_from=%s\n", len(expire), len(wal), floorName)
	return nil
}

func (j *pitrJob) ownedBases() (map[string]pitrBase, error) {
	bases := make(map[string]pitrBase)
	dir, err := j.repo.root.Open(pitrBases)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !pitrID.MatchString(id) {
			continue
		}
		name := filepath.Join(pitrBases, id, pitrRecordName)
		if _, err := j.repo.root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			continue
		}
		raw, err := bundle.ReadProtectedConfiguration(filepath.Join(j.repo.path, name))
		var header pitrBase
		if err != nil || wire.StrictJSON(raw, &header) != nil {
			return nil, fmt.Errorf("INSTALL_PITR_BASE_INVALID: verify base metadata for %s before expiry", id)
		}
		if header.Owner != j.repo.owner.Token || header.Cluster != j.source.cluster {
			continue
		}
		b, err := j.readBase(id)
		if err != nil {
			return nil, err
		}
		bases[id] = b
	}
	return bases, nil
}

func pitrRetainedBases(bases map[string]pitrBase, cutoff string) (map[string]bool, uint64, error) {
	full, ok := bases[cutoff]
	if !ok || full.Parent != "" {
		return nil, 0, errors.New("INSTALL_PITR_CUTOFF_INVALID: select a verified full base from this installation")
	}
	retain := make(map[string]bool)
	floor := full.Start
	for id, b := range bases {
		if b.Timeline != full.Timeline {
			return nil, 0, errors.New("INSTALL_PITR_TIMELINE_MIXED: inspect the timeline history before expiring backups")
		}
		// Check every dependency before deletion, including branches
		// that are about to expire.
		seen := make(map[string]bool)
		keep := b.Start >= full.Start
		for {
			if seen[id] || len(seen) == pitrMaxChain {
				return nil, 0, errors.New("INSTALL_PITR_CHAIN_INVALID: base dependency cycle or chain limit exceeded")
			}
			seen[id] = true
			if keep {
				retain[id] = true
				floor = min(floor, b.Start)
			}
			if b.Parent == "" {
				break
			}
			parent, ok := bases[b.Parent]
			if !ok || parent.Timeline != b.Timeline || parent.End > b.Start {
				return nil, 0, errors.New("INSTALL_PITR_CHAIN_INVALID: an owned base has a missing or unordered dependency")
			}
			id, b = b.Parent, parent
		}
	}
	return retain, floor, nil
}

func (j *pitrJob) walExpiry(name, floor string, size uint64) ([]string, error) {
	dir, err := j.repo.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var expire, retained []string
	for {
		entries, err := dir.ReadDir(256)
		for _, entry := range entries {
			segment := entry.Name()
			if !pitrWALName.MatchString(segment) {
				continue
			}
			info, statErr := entry.Info()
			if statErr != nil || !info.Mode().IsRegular() || info.Size() != int64(size) || segment[:8] != floor[:8] {
				return nil, errors.New("INSTALL_PITR_WAL_UNSAFE: inspect the archive for unexpected segments or timelines before expiry")
			}
			if segment < floor {
				expire = append(expire, segment)
			} else {
				retained = append(retained, segment)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if err := validatePITRWALRange(floor, size, retained); err != nil {
		return nil, err
	}
	return expire, nil
}

func validatePITRWALRange(floor string, size uint64, retained []string) error {
	slices.Sort(retained)
	segmentsPerLog := (1 << 32) / size
	ordinal := func(name string) uint64 {
		log, _ := strconv.ParseUint(name[8:16], 16, 32)
		segment, _ := strconv.ParseUint(name[16:], 16, 32)
		if segment >= segmentsPerLog {
			return ^uint64(0)
		}
		return log*segmentsPerLog + segment
	}
	if len(retained) == 0 || retained[0] != floor {
		return fmt.Errorf("INSTALL_PITR_WAL_GAP: the archive has not retained the required floor segment %s", floor)
	}
	next := ordinal(floor)
	for _, segment := range retained {
		if actual := ordinal(segment); actual != next || actual == ^uint64(0) {
			return fmt.Errorf("INSTALL_PITR_WAL_GAP: check the archive before %s", segment)
		}
		next++
	}
	return nil
}
