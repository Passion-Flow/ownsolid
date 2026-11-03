package training_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualTLSTrainingUploadSchemasStrictEnvelopesChunksAndCurrentAuthority(t *testing.T) {
	f := setup(t)
	l, u, input := chunkIntent(t, f)
	handler := transport.New(f.s.Identity)
	handler.Training.Store = f.s.Store
	handler.Datasets.Service.Store = f.s.Datasets.Store
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	client := httpfixture.New(t, server, "admin", schemas)
	client.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	path := "/agent/v1/nodes/" + f.input.NodeID + "/tasks/" + l.TaskID + "/training-uploads"
	var metadata []byte
	if e := f.s.DB.QueryRow(context.Background(), "SELECT manifest_bytes FROM training_uploads WHERE id=$1", u.ID).Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"lease_token":"` + l.LeaseToken + `","kind":"checkpoint","metadata":` + string(metadata) + `}`)
	first := client.Call("POST", path, body, 201, "TrainingUploadResponse")
	if first["id"] != u.ID {
		t.Fatal("HTTP replay changed upload identity")
	}
	for _, raw := range [][]byte{append([]byte(`{"unknown":true,`), body[1:]...), bytes.Replace(body, []byte(`"kind"`), []byte(`"Kind"`), 1), bytes.Replace(body, []byte(`"metadata":`), []byte(`"metadata":null,"metadata":`), 1)} {
		client.Call("POST", path, raw, 400, "Problem")
	}
	client.Call("POST", path+"?scope=foreign", body, 400, "Problem")
	client.Call("POST", path, bytes.Repeat([]byte("x"), wire.MaximumUploadSubmissionBytes+1), 413, "Problem")
	chunkPath := path + "/" + u.ID + "/chunks"
	chunk := func(data string, duplicateHeader bool, status int) {
		t.Helper()
		r, e := http.NewRequest("POST", httpfixture.AdminOrigin+chunkPath, strings.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+f.credential)
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Training-Lease-Token", l.LeaseToken)
		r.Header.Set("X-Training-Asset", input.Asset)
		r.Header.Set("X-Training-Offset", "0")
		r.Header.Set("X-Training-SHA256", input.SHA256)
		if duplicateHeader {
			r.Header.Add("X-Training-Asset", input.Asset)
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != status {
			t.Fatalf("actual chunk HTTP status=%d expected=%d error=%v", response.StatusCode, status, e)
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("non-JSON chunk acknowledgment")
		}
		name := "Problem"
		if status == 200 {
			name = "TrainingChunkAcceptanceResponse"
		}
		if e = schemas[name].Validate(value); e != nil {
			t.Fatal("chunk response contract", e)
		}
	}
	chunk("x", true, 400)
	chunk("xx", false, 400)
	chunk("y", false, 409)
	chunk("x", false, 200)
	chunk("x", false, 200)
	client.Call("POST", path+"/"+u.ID+"/finish", map[string]any{"lease_token": l.LeaseToken}, 409, "Problem")
	if _, e := f.s.DB.Exec(context.Background(), "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	chunk("x", false, 403)
	client.Call("POST", path, body, 403, "Problem")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Headers = client.Headers
	portal.Call("POST", path, body, 404, "Problem")
}

func TestActualTLSIncompleteTrainingBodyHonorsTypedReadDeadlineWithoutAcceptingBytes(t *testing.T) {
	f := setup(t)
	l, u, input := chunkIntent(t, f)
	policy := f.s.Identity.Config.Policy()
	policy.Training.UploadSeconds = 1
	f.s.Identity.Config.Runtime = &policy
	handler := transport.New(f.s.Identity)
	handler.Training.Store = f.s.Store
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	clientTLS := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	clientTLS.ServerName = "admin.example.invalid"
	connection, e := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", server.Listener.Addr().String(), clientTLS)
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	if e = connection.SetDeadline(time.Now().Add(5 * time.Second)); e != nil {
		t.Fatal(e)
	}
	path := "/agent/v1/nodes/" + f.input.NodeID + "/tasks/" + l.TaskID + "/training-uploads/" + u.ID + "/chunks"
	request := "POST " + path + " HTTP/1.1\r\nHost: admin.example.invalid\r\nAuthorization: Bearer " + f.credential + "\r\nContent-Type: application/octet-stream\r\nContent-Length: 1\r\nX-Training-Lease-Token: " + l.LeaseToken + "\r\nX-Training-Asset: " + input.Asset + "\r\nX-Training-Offset: 0\r\nX-Training-SHA256: " + input.SHA256 + "\r\n\r\n"
	started := time.Now()
	if _, e = io.WriteString(connection, request); e != nil {
		t.Fatal(e)
	}
	response, e := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: "POST"})
	if e != nil {
		t.Fatal("bounded real slow request did not receive safe failure", e)
	}
	defer response.Body.Close()
	if response.StatusCode != 503 || time.Since(started) > 4*time.Second {
		t.Fatal("typed upload read deadline not enforced", response.StatusCode)
	}
	if count(t, f, "SELECT count(*) FROM training_upload_chunks") != 0 || count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 {
		t.Fatal("timed-out body accepted")
	}
}
