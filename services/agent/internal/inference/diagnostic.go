package inference

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// ContainerFailure maps bounded private engine diagnostics to static codes; raw messages are not returned.
func (d *Docker) ContainerFailure(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("INFERENCE_CONTAINER_CONFLICT")
	}
	request, e := http.NewRequestWithContext(ctx, "GET", "http://docker"+engineAPI+"/containers/"+id+"/logs?stdout=true&stderr=true&tail=40&timestamps=false", nil)
	if e != nil {
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	response, e := d.client.Do(request)
	if e != nil {
		return fail("INFERENCE_DOCKER_UNAVAILABLE")
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || response.StatusCode != 200 || len(body) > 65536 {
		return fail("INFERENCE_OPERATION_FAILED")
	}
	text, e := decodeDockerLogs(body)
	if e != nil {
		return e
	}
	return diagnosticCode(text)
}
func decodeDockerLogs(body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}
	if len(body) >= 8 && (body[0] == 1 || body[0] == 2) && body[1] == 0 && body[2] == 0 && body[3] == 0 {
		var value strings.Builder
		for len(body) > 0 {
			if len(body) < 8 || body[0] != 1 && body[0] != 2 || body[1] != 0 || body[2] != 0 || body[3] != 0 {
				return "", fail("INFERENCE_OPERATION_FAILED")
			}
			size := int(binary.BigEndian.Uint32(body[4:8]))
			body = body[8:]
			if size > len(body) {
				return "", fail("INFERENCE_OPERATION_FAILED")
			}
			value.Write(body[:size])
			body = body[size:]
		}
		body = []byte(value.String())
	}
	if !utf8.Valid(body) {
		return "", fail("INFERENCE_OPERATION_FAILED")
	}
	return string(body), nil
}
func diagnosticCode(value string) error {
	text := strings.ToLower(value)
	switch {
	case strings.Contains(text, "cuda out of memory"), strings.Contains(text, "torch.outofmemoryerror"), strings.Contains(text, "cudaerroroutofmemory"):
		return fail("INFERENCE_GPU_OOM")
	case strings.Contains(text, "cuda driver version is insufficient"), strings.Contains(text, "driver/library version mismatch"):
		return fail("INFERENCE_DRIVER_UNSUPPORTED")
	case strings.Contains(text, "no space left on device"):
		return fail("INFERENCE_DISK_SPACE_LOW")
	case strings.Contains(text, "permission denied"):
		return fail("INFERENCE_IO_FAILED")
	default:
		return fail("INFERENCE_NOT_READY")
	}
}
