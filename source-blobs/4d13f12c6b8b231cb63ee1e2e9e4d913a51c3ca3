package training

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

func TestTypedTrainingConfigRejectsUnknownCaseDuplicatesNullAndPartialPolicy(t *testing.T) {
	config := fixtureConfig()
	raw, _ := json.Marshal(config)
	var actual Config
	if e := json.Unmarshal(raw, &actual); e != nil || actual.Validate() != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"image_id":`, `"Image_ID":`, 1), strings.Replace(string(raw), `"image_id":`, `"image_id":"duplicate","image_id":`, 1), strings.Replace(string(raw), `"image_id":"`+config.ImageID+`"`, `"image_id":null`, 1), strings.TrimSuffix(string(raw), "}") + `,"runtime":{"user_id":12001}}`} {
		if json.Unmarshal([]byte(bad), &actual) == nil {
			t.Fatal("ambiguous or incomplete configuration decoded")
		}
	}
	p := settings.Default()
	p.ControlConnections = 1
	if ValidateAgentPolicy(config, p) == nil {
		t.Fatal("single occupied transfer connection can block independent renewal")
	}
	p = settings.Default()
	local := config.Policy()
	local.StopSeconds = p.DockerHeaderSeconds
	config.Runtime = &local
	if ValidateAgentPolicy(config, p) == nil {
		t.Fatal("stop grace exceeds its transport header budget")
	}
}
func TestAvailableHostMemoryIsBoundedCurrentCapacityNotTotalInstalledRAM(t *testing.T) {
	if e := validateHostMemory("MemTotal: 99999999 kB\nMemAvailable: 8192 kB\n", 8388608); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{"MemTotal: 99999999 kB\n", "MemAvailable: 8191 kB\n", "MemAvailable: -1 kB\n", "MemAvailable: 999999999999999999999999 kB\n", "MemAvailable: 8192 MB\n", "MemAvailable: 8192 kB\nMemAvailable: 8192 kB\n"} {
		if validateHostMemory(body, 8388608) == nil {
			t.Fatal("unsafe or low current memory admitted")
		}
	}
}
