// Package privateclusterproof is the closed, side-effect-free M28 Job program.
// It demonstrates a bounded observed execution, not cryptographic attestation.
package privateclusterproof

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
)

const Version = 1
const MaximumChallengeBytes = 1024
const MaximumProofBytes = 1024
const ContainerName = "probe"
const PodUIDVariable = "PROJECT_ONE_PRIVATE_CLUSTER_POD_UID"
const Command = "/private-cluster-probe"

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var nonce = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Challenge struct {
	FormatVersion  int    `json:"format_version"`
	RunID          string `json:"run_id"`
	NamespaceUID   string `json:"namespace_uid"`
	InstallationID string `json:"installation_id"`
	Nonce          string `json:"nonce"`
	Architecture   string `json:"architecture"`
	RunAsUser      int64  `json:"run_as_user"`
}
type Proof struct {
	FormatVersion   int    `json:"format_version"`
	RunID           string `json:"run_id"`
	NamespaceUID    string `json:"namespace_uid"`
	InstallationID  string `json:"installation_id"`
	PodUID          string `json:"pod_uid"`
	Architecture    string `json:"architecture"`
	UID             int64  `json:"uid"`
	ChallengeSHA256 string `json:"challenge_sha256"`
}

func failure() error           { return errors.New("PRIVATE_CLUSTER_PROBE_INVALID") }
func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func (c Challenge) Validate() error {
	if c.FormatVersion != Version || !uuid.MatchString(c.RunID) || !uuid.MatchString(c.NamespaceUID) || !uuid.MatchString(c.InstallationID) || !nonce.MatchString(c.Nonce) || c.Architecture != "amd64" && c.Architecture != "arm64" || c.RunAsUser < 1 || c.RunAsUser > 2147483647 {
		return failure()
	}
	return nil
}
func Encode(c Challenge) ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(c)
	if e != nil || len(raw) > MaximumChallengeBytes {
		return nil, failure()
	}
	return raw, nil
}
func Decode(raw []byte) (Challenge, error) {
	var c Challenge
	if len(raw) < 1 || len(raw) > MaximumChallengeBytes || json.Unmarshal(raw, &c) != nil {
		return c, failure()
	}
	canonical, e := Encode(c)
	if e != nil || !bytes.Equal(raw, canonical) {
		return Challenge{}, failure()
	}
	return c, nil
}
func Arguments(c Challenge) ([]string, error) {
	raw, e := Encode(c)
	if e != nil {
		return nil, e
	}
	return []string{"--challenge", base64.RawURLEncoding.EncodeToString(raw), "--pod-uid", "$(" + PodUIDVariable + ")"}, nil
}
func makeProof(c Challenge, pod, osName, architecture string, uid int64) (Proof, error) {
	raw, e := Encode(c)
	if e != nil || !uuid.MatchString(pod) || osName != "linux" || architecture != c.Architecture || uid != c.RunAsUser || uid <= 0 {
		return Proof{}, failure()
	}
	return Proof{Version, c.RunID, c.NamespaceUID, c.InstallationID, pod, architecture, uid, Digest(raw)}, nil
}

// Run receives only the closed challenge and kubelet-expanded downward Pod UID.
// It never executes a command, opens a network/file, reads ambient credentials or
// writes anything beyond its one bounded proof on the supplied stdout writer.
func Run(args []string, out io.Writer) error {
	if out == nil || len(args) != 4 || args[0] != "--challenge" || args[2] != "--pod-uid" || len(args[1]) > base64.RawURLEncoding.EncodedLen(MaximumChallengeBytes) {
		return failure()
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(args[1])
	if e != nil || base64.RawURLEncoding.EncodeToString(raw) != args[1] {
		return failure()
	}
	c, e := Decode(raw)
	if e != nil {
		return e
	}
	p, e := makeProof(c, args[3], runtime.GOOS, runtime.GOARCH, int64(os.Geteuid()))
	if e != nil {
		return e
	}
	data, e := json.Marshal(p)
	if e != nil || len(data)+1 > MaximumProofBytes {
		return failure()
	}
	data = append(data, '\n')
	n, e := out.Write(data)
	if e != nil {
		return errors.New("PRIVATE_CLUSTER_PROBE_OUTPUT_FAILED")
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
func Verify(raw []byte, c Challenge, pod string) (Proof, error) {
	var p Proof
	if len(raw) < 1 || len(raw) > MaximumProofBytes || !bytes.HasSuffix(raw, []byte{'\n'}) || json.Unmarshal(raw, &p) != nil {
		return Proof{}, failure()
	}
	expected, e := makeProof(c, pod, "linux", c.Architecture, c.RunAsUser)
	if e != nil || p != expected {
		return Proof{}, failure()
	}
	canonical, e := json.Marshal(p)
	if e != nil || !bytes.Equal(raw, append(canonical, '\n')) || strings.Contains(p.PodUID, "$") {
		return Proof{}, failure()
	}
	return p, nil
}
