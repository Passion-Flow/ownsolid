package apps

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

const maxCatalogVersions = 128

// ErrReleaseTransition identifies a missing, ambiguous or incompatible release selection.
var ErrReleaseTransition = errors.New("CATALOG_RELEASE_TRANSITION_INVALID")

// ReleasePin identifies a release by its version and exact signed bundle.json bytes.
type ReleasePin struct {
	Version      string `json:"version"`
	BundleSHA256 string `json:"bundle_sha256"`
}

func (p ReleasePin) valid() bool {
	return triplet.MatchString(p.Version) && digest.MatchString(p.BundleSHA256)
}

// TransitionRequest carries the installed pin and running platform from the
// installer. With no target it selects the unique next upgrade. UpgradeTo
// pins that choice for a repeated operation. RollbackTo comes from trusted
// installation history and selects the exact prior artifact.
type TransitionRequest struct {
	AppID           string
	PlatformVersion string
	Installed       ReleasePin
	UpgradeTo       *ReleasePin
	RollbackTo      *ReleasePin
}

// InitialReleaseRequest carries the app, platform and exact pin selected by the installer.
type InitialReleaseRequest struct {
	AppID           string
	PlatformVersion string
	Pin             ReleasePin
}

// ReleaseMetadata contains verified manifest and locale documents and their
// exact release pin. The installer verifies payloads before use.
type ReleaseMetadata struct {
	Release
	Pin ReleasePin
}

// ListReleaseMetadata reads the signed metadata for one authorized app from
// its private immutable version directory. It verifies every entry with the
// trusted publisher key and returns newest versions first without opening
// engine artifacts or image archives.
func ListReleaseMetadata(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, appID string) ([]ReleaseMetadata, error) {
	if appRoot == nil || len(key) != ed25519.PublicKeySize || !validID(appID) {
		return nil, transitionError("supply a private catalog directory, trusted publisher and app identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := catalogVersionEntries(appRoot)
	if err != nil {
		return nil, err
	}
	releases := make([]ReleaseMetadata, 0, len(entries))
	for _, entry := range entries {
		release, err := readSignedVersion(ctx, appRoot, key, appID, entry.Name())
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	slices.SortFunc(releases, func(a, b ReleaseMetadata) int {
		left, _ := parseVersion(a.Pin.Version)
		right, _ := parseVersion(b.Pin.Version)
		return slices.Compare(right[:], left[:])
	})
	return releases, nil
}

// VerifyInitialRelease verifies the exact release chosen for a fresh install.
// appRoot contains the app's private, immutable version directories. The
// caller supplies the app, platform and release pin from its trusted selection.
func VerifyInitialRelease(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, maximumPayloadBytes int64, request InitialReleaseRequest) (InstallBundle, error) {
	if appRoot == nil || len(key) != ed25519.PublicKeySize || maximumPayloadBytes <= 0 ||
		!validID(request.AppID) || !request.Pin.valid() {
		return InstallBundle{}, transitionError("supply a trusted app and release pin and a payload limit")
	}
	release, err := verifyCatalogVersion(ctx, appRoot, key, maximumPayloadBytes, request.Pin)
	if err != nil {
		return InstallBundle{}, err
	}
	if release.Manifest.ID != request.AppID {
		return InstallBundle{}, transitionError("the selected release belongs to a different app")
	}
	if err := checkTransitionPlatform(release.Manifest, request.PlatformVersion); err != nil {
		return InstallBundle{}, err
	}
	return release, nil
}

// VerifyReleaseTransition selects an adjacent signed release and runs the
// existing artifact and OCI verification on that exact destination. appRoot
// is a private immutable app directory containing version subdirectories.
// The caller owns publisher trust, installation history and authorization.
func VerifyReleaseTransition(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, maximumPayloadBytes int64, request TransitionRequest) (InstallBundle, error) {
	if appRoot == nil || len(key) != ed25519.PublicKeySize || maximumPayloadBytes <= 0 ||
		!validID(request.AppID) || !request.Installed.valid() ||
		request.UpgradeTo != nil && (!request.UpgradeTo.valid() || request.RollbackTo != nil) ||
		request.RollbackTo != nil && !request.RollbackTo.valid() {
		return InstallBundle{}, transitionError("supply trusted app and installed release identities, one target at most, and a payload limit")
	}
	installed, err := readSignedVersion(ctx, appRoot, key, request.AppID, request.Installed.Version)
	if err != nil {
		return InstallBundle{}, err
	}
	if installed.Pin != request.Installed {
		return InstallBundle{}, transitionError("the installed version differs from its retained signed bundle")
	}
	if err := checkTransitionPlatform(installed.Manifest, request.PlatformVersion); err != nil {
		return InstallBundle{}, err
	}
	var target ReleaseMetadata
	if request.RollbackTo != nil {
		if !slices.Contains(installed.Manifest.UpgradeFrom, *request.RollbackTo) {
			return InstallBundle{}, transitionError("the retained rollback pin is not a direct predecessor of the installed release")
		}
		target, err = readSignedVersion(ctx, appRoot, key, request.AppID, request.RollbackTo.Version)
		if err == nil && target.Pin != *request.RollbackTo {
			return InstallBundle{}, transitionError("the rollback version differs from its retained signed bundle")
		}
	} else {
		target, err = selectNextUpgrade(ctx, appRoot, key, request)
	}
	if err != nil {
		return InstallBundle{}, err
	}
	if target.Manifest.Artifact.Engine != installed.Manifest.Artifact.Engine {
		return InstallBundle{}, transitionError("the installed and target releases must use the same workload engine")
	}
	if err := checkTransitionPlatform(target.Manifest, request.PlatformVersion); err != nil {
		return InstallBundle{}, err
	}
	if request.RollbackTo == nil {
		if _, err := verifyCatalogVersion(ctx, appRoot, key, maximumPayloadBytes, installed.Pin); err != nil {
			return InstallBundle{}, fmt.Errorf("%w: verify the installed recovery bundle: %w", ErrReleaseTransition, err)
		}
	}
	return verifyCatalogVersion(ctx, appRoot, key, maximumPayloadBytes, target.Pin)
}

func verifyCatalogVersion(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, maximumPayloadBytes int64, expected ReleasePin) (InstallBundle, error) {
	destination, err := openCatalogVersion(appRoot, expected.Version)
	if err != nil {
		return InstallBundle{}, err
	}
	defer destination.Close()
	verified, err := VerifyInstallBundle(ctx, destination, key, maximumPayloadBytes)
	if err != nil {
		return InstallBundle{}, err
	}
	if verified.Pin != expected {
		return InstallBundle{}, transitionError("the selected release changed during verification")
	}
	return verified, nil
}

func catalogVersionEntries(appRoot *os.Root) ([]os.DirEntry, error) {
	directory, err := appRoot.Open(".")
	if err != nil {
		return nil, transitionError("open the private app catalog directory")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxCatalogVersions + 1)
	if (err != nil && !errors.Is(err, io.EOF)) || len(entries) > maxCatalogVersions {
		return nil, transitionError("provide a readable catalog with at most 128 version directories")
	}
	for _, entry := range entries {
		if !entry.IsDir() || !triplet.MatchString(entry.Name()) {
			return nil, transitionError("use only stable version directories in the app catalog")
		}
	}
	return entries, nil
}

func selectNextUpgrade(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, request TransitionRequest) (ReleaseMetadata, error) {
	entries, err := catalogVersionEntries(appRoot)
	if err != nil {
		return ReleaseMetadata{}, err
	}
	var target ReleaseMetadata
	for _, entry := range entries {
		if entry.Name() == request.Installed.Version {
			continue
		}
		release, err := readSignedVersion(ctx, appRoot, key, request.AppID, entry.Name())
		if err != nil {
			return ReleaseMetadata{}, err
		}
		if !slices.Contains(release.Manifest.UpgradeFrom, request.Installed) {
			continue
		}
		if target.Pin.Version != "" {
			return ReleaseMetadata{}, transitionError("the installed release has multiple direct successors")
		}
		target = release
	}
	if target.Pin.Version == "" {
		return ReleaseMetadata{}, transitionError("no direct upgrade is available for the installed signed release")
	}
	if request.UpgradeTo != nil && target.Pin != *request.UpgradeTo {
		return ReleaseMetadata{}, transitionError("the expected signed upgrade is missing or is not the direct successor")
	}
	return target, nil
}

func readSignedVersion(ctx context.Context, root *os.Root, key ed25519.PublicKey, appID, version string) (ReleaseMetadata, error) {
	if err := ctx.Err(); err != nil {
		return ReleaseMetadata{}, err
	}
	releaseRoot, err := openCatalogVersion(root, version)
	if err != nil {
		return ReleaseMetadata{}, err
	}
	defer releaseRoot.Close()
	release, index, err := readSignedRelease(releaseRoot, key)
	if err != nil {
		return ReleaseMetadata{}, fmt.Errorf("%w: verify catalog version %s: %w", ErrReleaseTransition, version, err)
	}
	if release.Manifest.ID != appID || release.Manifest.Version != version {
		return ReleaseMetadata{}, transitionError("a version directory does not match its signed app and version")
	}
	return ReleaseMetadata{Release: release, Pin: ReleasePin{Version: version, BundleSHA256: index.digest}}, nil
}

func openCatalogVersion(root *os.Root, version string) (*os.Root, error) {
	before, err := root.Lstat(version)
	if err != nil || !before.IsDir() {
		return nil, fmt.Errorf("%w: provide the local directory for version %s", ErrReleaseTransition, version)
	}
	opened, err := root.OpenRoot(version)
	if err != nil {
		return nil, fmt.Errorf("%w: open catalog version %s", ErrReleaseTransition, version)
	}
	after, err := opened.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		opened.Close()
		return nil, transitionError("a version directory changed while opening the release")
	}
	return opened, nil
}

func checkTransitionPlatform(manifest Manifest, platformVersion string) error {
	supported, err := manifest.Platform.Supports(platformVersion)
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("%w: version %s is incompatible with the running platform", ErrReleaseTransition, manifest.Version)
	}
	return nil
}

func transitionError(message string) error {
	return fmt.Errorf("%w: %s", ErrReleaseTransition, message)
}
