package inference

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestPrivateDockerDiagnosticsYieldOnlyStableCodes(t *testing.T) {
	message := []byte("SYNTHETIC_PRIVATE_MARKER: CUDA out of memory")
	body := make([]byte, 8)
	body[0] = 2
	binary.BigEndian.PutUint32(body[4:8], uint32(len(message)))
	body = append(body, message...)
	text, e := decodeDockerLogs(body)
	if e != nil {
		t.Fatal(e)
	}
	code := diagnosticCode(text)
	if code.Error() != "INFERENCE_GPU_OOM" || strings.Contains(code.Error(), "PRIVATE") {
		t.Fatal("diagnostic leaked or misclassified")
	}
	if _, e = decodeDockerLogs(append(body, 1)); e == nil {
		t.Fatal("truncated multiplex frame accepted")
	}
	if diagnosticCode("CUDA driver version is insufficient for CUDA runtime version").Error() != "INFERENCE_DRIVER_UNSUPPORTED" {
		t.Fatal("driver mismatch misclassified")
	}
}
