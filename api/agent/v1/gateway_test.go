package v1

import (
	"strings"
	"testing"
)

const testGateway = "12345678-1234-1234-1234-123456789abc"

func testToken() string { return strings.Repeat("A", 42) + "Q" }

func TestGatewayTasksBelongToTheInferenceProfileOnly(t *testing.T) {
	for _, operation := range []string{GatewayOperation("start"), GatewayOperation("stop")} {
		if !IsGatewayOperation(operation) || !OperationAllowed(DockerInferenceProfile, operation) {
			t.Fatal(operation)
		}
		for _, profile := range []string{ReadOnlyProfile, SystemdComponentsProfile, SystemdMaintenanceProfile, DockerAIProfile, DockerModelLifecycleProfile} {
			if OperationAllowed(profile, operation) {
				t.Fatalf("%s may run %s", profile, operation)
			}
		}
	}
	if IsGatewayOperation(GatewayOperation("restart")) || IsGatewayOperation(InferenceOperation("stop")) {
		t.Fatal("unknown gateway operation accepted")
	}
}

func TestGatewayCommandsCarryTheControlOriginOnlyToStart(t *testing.T) {
	start := GatewayCommand{Action: "start", GatewayID: testGateway, ControlOrigin: "https://admin.example.test:8443"}
	if e := start.Validate(GatewayOperation("start")); e != nil {
		t.Fatal(e)
	}
	stop := GatewayCommand{Action: "stop", GatewayID: testGateway}
	if e := stop.Validate(GatewayOperation("stop")); e != nil {
		t.Fatal(e)
	}
	for name, bad := range map[string]struct {
		command   GatewayCommand
		operation string
	}{
		"operation mismatch":       {start, GatewayOperation("stop")},
		"unknown action":           {GatewayCommand{Action: "restart", GatewayID: testGateway}, GatewayOperation("restart")},
		"bad gateway":              {GatewayCommand{Action: "stop", GatewayID: "gateway"}, GatewayOperation("stop")},
		"start without origin":     {GatewayCommand{Action: "start", GatewayID: testGateway}, GatewayOperation("start")},
		"start with plain HTTP":    {GatewayCommand{Action: "start", GatewayID: testGateway, ControlOrigin: "http://admin.example.test"}, GatewayOperation("start")},
		"stop that carries origin": {GatewayCommand{Action: "stop", GatewayID: testGateway, ControlOrigin: start.ControlOrigin}, GatewayOperation("stop")},
	} {
		if bad.command.Validate(bad.operation) == nil {
			t.Error(name)
		}
	}
}

func TestGatewayCredentialsNameTheirGateway(t *testing.T) {
	credential := GatewayCredential(testGateway, testToken())
	if !ValidGatewayCredential(credential, testGateway) {
		t.Fatal(credential)
	}
	for _, bad := range []string{
		GatewayCredential("22345678-1234-1234-1234-123456789abc", testToken()),
		"gw2." + testGateway + "." + testToken(),
		GatewayCredential(testGateway, testToken()[:42]),
		GatewayCredential(testGateway, testToken()+"A"),
		GatewayCredential(testGateway, strings.Repeat("A", 42)+"B"),
		credential + ".extra",
	} {
		if ValidGatewayCredential(bad, testGateway) {
			t.Error(bad)
		}
	}
}

func TestGatewayOriginsAreCanonicalHTTPS(t *testing.T) {
	for origin, want := range map[string]GatewayOrigin{
		"https://gateway.rack7.example": {Host: "gateway.rack7.example", Port: 443},
		"https://node-a01:18443":        {Host: "node-a01", Port: 18443},
		"https://192.0.2.10:18443":      {Port: 18443},
		"https://[2001:db8::7]:18443":   {Port: 18443},
	} {
		got, e := ParseGatewayOrigin(origin)
		if e != nil || got.Host != want.Host || got.Port != want.Port || (want.Host == "") != (got.IP != nil) || got.String() != origin {
			t.Errorf("%s: %+v %v", origin, got, e)
		}
	}
	for _, bad := range []string{
		"", "http://gateway.example", "https://gateway.example/", "https://gateway.example:443",
		"https://Gateway.example", "https://user@gateway.example", "https://gateway.example?x=1",
		"https://gateway.example#top", "https://gateway.example:0", "https://gateway.example:65536",
		"https://gateway.example:080", "https://0.0.0.0:18443", "https://224.0.0.1:18443",
		"https://-gateway.example", "https://gateway..example", "https://gate_way.example",
		"https://" + strings.Repeat("a", 64) + ".example", "https://[2001:0db8::7]:18443",
	} {
		if _, e := ParseGatewayOrigin(bad); e == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestGatewayActivationsCarryOnlyWellFormedSecrets(t *testing.T) {
	good := GatewayActivation{LeaseToken: testToken(), Credential: GatewayCredential(testGateway, testToken()), CertificateRequest: "-----BEGIN CERTIFICATE REQUEST-----", Origin: "https://192.0.2.10:18443"}
	if e := good.Validate(testGateway); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*GatewayActivation){
		"lease": func(a *GatewayActivation) { a.LeaseToken = "short" },
		"other gateway": func(a *GatewayActivation) {
			a.Credential = GatewayCredential("22345678-1234-1234-1234-123456789abc", testToken())
		},
		"no request":     func(a *GatewayActivation) { a.CertificateRequest = "" },
		"large request":  func(a *GatewayActivation) { a.CertificateRequest = strings.Repeat("A", MaxCertificateRequestBytes+1) },
		"plain origin":   func(a *GatewayActivation) { a.Origin = "http://192.0.2.10:18443" },
		"missing origin": func(a *GatewayActivation) { a.Origin = "" },
	} {
		bad := good
		change(&bad)
		if bad.Validate(testGateway) == nil {
			t.Error(name)
		}
	}
}

func TestGatewayResultsMatchTheirCommand(t *testing.T) {
	start := GatewayCommand{Action: "start", GatewayID: testGateway, ControlOrigin: "https://admin.example.test"}
	stop := GatewayCommand{Action: "stop", GatewayID: testGateway}
	container := strings.Repeat("a", 64)
	running := Result{Outcome: "success", Gateway: &GatewayObservation{GatewayID: testGateway, State: "running", ContainerID: container}}
	stopped := Result{Outcome: "success", Gateway: &GatewayObservation{GatewayID: testGateway, State: "stopped"}}
	failed := Result{Outcome: "failure", ErrorCode: "GATEWAY_IMAGE_MISSING", Gateway: &GatewayObservation{GatewayID: testGateway, State: "stopped", ErrorCode: "GATEWAY_IMAGE_MISSING"}}
	for name, accepted := range map[string]struct {
		result  Result
		command GatewayCommand
	}{"start": {running, start}, "stop": {stopped, stop}, "failure": {failed, start}} {
		if e := accepted.result.ValidateGateway(accepted.command); e != nil {
			t.Errorf("%s: %v", name, e)
		}
	}
	other := running
	other.Gateway = &GatewayObservation{GatewayID: "22345678-1234-1234-1234-123456789abc", State: "running", ContainerID: container}
	unknown := failed
	unknown.ErrorCode, unknown.Gateway = "INFERENCE_OOM", &GatewayObservation{GatewayID: testGateway, State: "stopped", ErrorCode: "INFERENCE_OOM"}
	mixed := running
	mixed.Inference = &InferenceObservation{}
	noContainer := running
	noContainer.Gateway = &GatewayObservation{GatewayID: testGateway, State: "running"}
	for name, refused := range map[string]struct {
		result  Result
		command GatewayCommand
	}{
		"start reported stopped":    {stopped, start},
		"stop reported running":     {running, stop},
		"another gateway":           {other, start},
		"unknown error":             {unknown, start},
		"mixed observation":         {mixed, start},
		"running without container": {noContainer, start},
		"no observation":            {Result{Outcome: "success"}, start},
	} {
		if refused.result.ValidateGateway(refused.command) == nil {
			t.Error(name)
		}
	}
	inference := Result{Outcome: "success", Gateway: running.Gateway, Inference: &InferenceObservation{}}
	if inference.ValidateInference(InferenceCommand{Action: "observe"}) == nil {
		t.Fatal("an inference result carried a gateway observation")
	}
}
