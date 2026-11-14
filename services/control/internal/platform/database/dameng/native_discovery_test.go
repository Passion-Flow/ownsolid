package dameng

import (
	"context"
	"fmt"
	"testing"
)

func TestNativeDiscoveryRejectsPartialAndAlreadyObservedIdentity(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		t.Run(fmt.Sprintf("identity_fields_%d", mask), func(t *testing.T) {
			m := relativeManifestFixture("/private/native/bin")
			m.DriverVersion, m.DBMSName, m.DBMSVersion = "", "", ""
			values := []*string{&m.DriverVersion, &m.DBMSName, &m.DBMSVersion}
			for i, value := range values {
				if mask&(1<<i) != 0 {
					*value = "observed"
				}
			}
			if err := VerifyNativeInventoryForValidation(context.Background(), m); FailureCode(err) != "DAMENG_NATIVE_MANIFEST_INVALID" {
				t.Fatal("discovery mixed static inputs with asserted runtime identity", err)
			}
			if (m.Validate() == nil) != (mask == 7) {
				t.Fatal("normal manifest identity requirements changed")
			}
		})
	}
}
