package config

import "testing"

func TestModelGatewayIdentitySectionIsChecked(t *testing.T) {
	var absent *ModelGatewayIdentityConfiguration
	if absent.Validate() != nil {
		t.Fatal("an absent section is valid")
	}
	good := ModelGatewayIdentityConfiguration{CACertificateFile: "/run/gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca-key.pem"}
	if good.Validate() != nil {
		t.Fatal("complete section")
	}
	if good.Lifetime().Hours() != defaultGatewayCertificateLifetimeHours {
		t.Fatal(good.Lifetime())
	}
	for name, bad := range map[string]ModelGatewayIdentityConfiguration{
		"no key":         {CACertificateFile: "/run/gateway-ca.pem"},
		"no bundle":      {CAPrivateKeyFile: "/run/gateway-ca-key.pem"},
		"relative":       {CACertificateFile: "gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca-key.pem"},
		"key is bundle":  {CACertificateFile: "/run/gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca.pem"},
		"lifetime short": {CACertificateFile: "/run/gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca-key.pem", CertificateLifetimeHours: 23},
		"lifetime long":  {CACertificateFile: "/run/gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca-key.pem", CertificateLifetimeHours: 8761},
	} {
		section := bad
		if section.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestOnlyPostgreSQLWithAGatewayCALetsAgentsRunGateways(t *testing.T) {
	c := machineTestConfiguration(TransportDirectHTTPS)
	if c.AgentsRunGateways() {
		t.Fatal("agents ran gateways without a gateway CA")
	}
	c.ModelGatewayIdentity = &ModelGatewayIdentityConfiguration{CACertificateFile: "/run/gateway-ca.pem", CAPrivateKeyFile: "/run/gateway-ca-key.pem"}
	if !c.AgentsRunGateways() {
		t.Fatal("a PostgreSQL installation with a gateway CA could not deliver gateways")
	}
	c.Database.Engine = MySQLDatabaseEngine
	if c.AgentsRunGateways() {
		t.Fatal("agents ran gateways on a database that stores no delivery")
	}
	if _, _, e := (Config{}).ModelGatewayAuthorityMaterial(); e == nil || e.Error() != "CONFIG_MODEL_GATEWAY_IDENTITY_REQUIRED" {
		t.Fatal(e)
	}
}
