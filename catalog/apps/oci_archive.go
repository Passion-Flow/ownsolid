package apps

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ociIndexType    = "application/vnd.oci.image.index.v1+json"
	ociManifestType = "application/vnd.oci.image.manifest.v1+json"
	ociConfigType   = "application/vnd.oci.image.config.v1+json"
	ociLayerType    = "application/vnd.oci.image.layer.v1.tar"
	ociBlobPrefix   = "blobs/sha256/"
	ociMetadataMax  = 1 << 20
	ociEntryMax     = 8192
	tarTailMax      = 10240
	tarBlockBytes   = 512
)

type ociSection struct {
	offset, size int64
}

type ociDescriptor struct {
	MediaType string         `json:"mediaType"`
	Digest    string         `json:"digest"`
	Size      int64          `json:"size"`
	URLs      []string       `json:"urls"`
	Data      jsontext.Value `json:"data"`
	Platform  *ociPlatform   `json:"platform"`
}

type ociPlatform struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	OSFeatures   []string `json:"os.features"`
}

func (p ociPlatform) amd64() bool {
	return p.OS == "linux" && p.Architecture == "amd64" &&
		(p.Variant == "" || p.Variant == "v1") && len(p.OSFeatures) == 0
}

func invalidOCI(reason string) error {
	return fmt.Errorf("%w: images.tar %s", ErrBundle, reason)
}

func verifyOCIArchive(ctx context.Context, root *os.Root, expected string, maximum int64, images []string) error {
	file, size, err := openLocalFile(root, ImageArchiveName, maximum)
	if err != nil {
		return invalidOCI("must be a regular OCI archive within the payload limit")
	}
	defer file.Close()
	source := payloadReader{ctx: ctx, file: file}
	hash := sha256.New()
	input := &ociCounter{reader: io.TeeReader(io.LimitReader(source, size), hash)}
	files, err := scanOCIArchive(input)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	var extra [1]byte
	n, err := source.Read(extra[:])
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if n != 0 || err != io.EOF || input.offset != size || encodeDigest(hash.Sum(nil)) != expected {
		return invalidOCI("does not match the signed archive")
	}
	return verifyOCIClosure(ctx, file, files, images)
}

type ociCounter struct {
	reader io.Reader
	offset int64
}

func (r *ociCounter) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.offset += int64(n)
	return n, err
}

func scanOCIArchive(input *ociCounter) (map[string]ociSection, error) {
	files := make(map[string]ociSection)
	directories := make(map[string]bool)
	archive := tar.NewReader(input)
	var paddedEnd int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			if input.offset-paddedEnd != 2*tarBlockBytes {
				return nil, invalidOCI("has an incomplete or malformed tar end marker")
			}
			break
		}
		if errors.Is(err, tar.ErrInsecurePath) {
			return nil, invalidOCI("contains a file outside the OCI layout")
		}
		if err != nil || len(files)+len(directories) >= ociEntryMax {
			return nil, invalidOCI("has a malformed tar header or too many entries")
		}
		if header.Format != tar.FormatUSTAR && header.Format != tar.FormatPAX {
			return nil, invalidOCI("uses an unsupported tar header format")
		}
		if header.Typeflag == tar.TypeDir {
			name := strings.TrimSuffix(header.Name, "/")
			if (name != "blobs" && name != "blobs/sha256") || header.Size != 0 ||
				len(header.PAXRecords) != 0 || directories[name] {
				return nil, invalidOCI("has an unexpected or repeated directory")
			}
			directories[name] = true
			paddedEnd = input.offset
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Linkname != "" {
			return nil, invalidOCI("contains an entry other than a regular file")
		}
		for field := range header.PAXRecords {
			if field != "size" {
				return nil, invalidOCI("contains unsupported tar metadata")
			}
		}
		if _, duplicate := files[header.Name]; duplicate {
			return nil, invalidOCI("contains a repeated file")
		}
		files[header.Name] = ociSection{offset: input.offset, size: header.Size}
		switch header.Name {
		case "index.json", "oci-layout":
			if header.Size > ociMetadataMax {
				return nil, invalidOCI("has oversized layout metadata")
			}
			if _, err := io.Copy(io.Discard, archive); err != nil {
				return nil, invalidOCI("contains incomplete layout metadata")
			}
		default:
			digestHex, ok := strings.CutPrefix(header.Name, ociBlobPrefix)
			expected := "sha256:" + digestHex
			if !ok || !digest.MatchString(expected) {
				return nil, invalidOCI("contains a file outside the OCI layout")
			}
			hash := sha256.New()
			if _, err := io.Copy(hash, archive); err != nil || encodeDigest(hash.Sum(nil)) != expected {
				return nil, invalidOCI("contains a blob that does not match its digest")
			}
		}
		paddedEnd = input.offset + (tarBlockBytes-input.offset%tarBlockBytes)%tarBlockBytes
	}
	tail, err := io.ReadAll(io.LimitReader(input, tarTailMax+1))
	if err != nil || len(tail) > tarTailMax || len(bytes.Trim(tail, "\x00")) != 0 {
		return nil, invalidOCI("has data after the tar end marker")
	}
	return files, nil
}

func verifyOCIClosure(ctx context.Context, file *os.File, files map[string]ociSection, images []string) error {
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	var index struct {
		SchemaVersion int             `json:"schemaVersion"`
		MediaType     string          `json:"mediaType"`
		Manifests     []ociDescriptor `json:"manifests"`
	}
	if !readOCIMetadata(file, files["oci-layout"], "", &layout) || layout.Version != "1.0.0" ||
		!readOCIMetadata(file, files["index.json"], "", &index) || index.SchemaVersion != 2 ||
		(index.MediaType != "" && index.MediaType != ociIndexType) || len(index.Manifests) != len(images) {
		return invalidOCI("must contain a version 1.0.0 OCI layout with the declared image inventory")
	}
	wanted := make(map[string]bool, len(images))
	for _, image := range images {
		wanted[image] = true
	}
	used := make(map[string]bool)
	for _, image := range index.Manifests {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !wanted[image.Digest] || image.MediaType != ociManifestType ||
			(image.Platform != nil && !image.Platform.amd64()) {
			return invalidOCI("contains an undeclared, repeated or unsupported image manifest")
		}
		delete(wanted, image.Digest)
		if err := verifyOCIImage(file, files, used, image); err != nil {
			return err
		}
	}
	if len(used)+2 != len(files) {
		return invalidOCI("contains an unreferenced blob")
	}
	return nil
}

func verifyOCIImage(file *os.File, files map[string]ociSection, used map[string]bool, image ociDescriptor) error {
	var manifest struct {
		SchemaVersion int             `json:"schemaVersion"`
		MediaType     string          `json:"mediaType"`
		ArtifactType  string          `json:"artifactType"`
		Subject       *ociDescriptor  `json:"subject"`
		Config        ociDescriptor   `json:"config"`
		Layers        []ociDescriptor `json:"layers"`
	}
	var config struct {
		ociPlatform `json:",inline"`
		RootFS      struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if !readOCIMetadata(file, image.section(files, used), image.Digest, &manifest) ||
		manifest.SchemaVersion != 2 || (manifest.MediaType != "" && manifest.MediaType != ociManifestType) ||
		manifest.ArtifactType != "" || manifest.Subject != nil || manifest.Layers == nil ||
		manifest.Config.MediaType != ociConfigType ||
		!readOCIMetadata(file, manifest.Config.section(files, used), manifest.Config.Digest, &config) ||
		!config.amd64() || config.RootFS.Type != "layers" || config.RootFS.DiffIDs == nil ||
		len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		return invalidOCI("must contain complete Linux amd64 image manifests and configurations")
	}
	for i, layer := range manifest.Layers {
		if (layer.MediaType != ociLayerType && layer.MediaType != ociLayerType+"+gzip") ||
			!digest.MatchString(config.RootFS.DiffIDs[i]) || layer.section(files, used).size == 0 {
			return invalidOCI("contains a missing, remote or unsupported layer")
		}
	}
	return nil
}

func (d ociDescriptor) section(files map[string]ociSection, used map[string]bool) ociSection {
	if !digest.MatchString(d.Digest) || d.Size <= 0 || len(d.URLs) != 0 || len(d.Data) != 0 {
		return ociSection{}
	}
	path := ociBlobPrefix + strings.TrimPrefix(d.Digest, "sha256:")
	section := files[path]
	if section.size != d.Size {
		return ociSection{}
	}
	used[path] = true
	return section
}

func readOCIMetadata(file *os.File, section ociSection, expected string, dest any) bool {
	if section.size < 1 || section.size > ociMetadataMax {
		return false
	}
	raw, err := io.ReadAll(io.NewSectionReader(file, section.offset, section.size))
	return err == nil && int64(len(raw)) == section.size &&
		(expected == "" || digestBytes(raw) == expected) && json.Unmarshal(raw, dest) == nil
}
