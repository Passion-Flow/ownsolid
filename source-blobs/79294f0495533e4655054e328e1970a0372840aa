package client

import (
	"context"
	"errors"
	"regexp"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

var gatewayTaskPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/tasks/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// ActivateGateway sends a started gateway's credential and key request under the task's
// lease and returns its serving certificate (GW-114).
func (c *Client) ActivateGateway(ctx context.Context, taskPath string, activation wire.GatewayActivation) (wire.GatewayActivated, error) {
	var out wire.GatewayActivated
	if !gatewayTaskPath.MatchString(taskPath) {
		return out, errors.New("AGENT_PATH_INVALID")
	}
	if e := c.Post(ctx, taskPath+"/"+wire.GatewayActivationPath, activation, &out); e != nil {
		return wire.GatewayActivated{}, e
	}
	if out.Certificate == "" || len(out.Certificate) > wire.MaxCertificateBytes || out.CertificateAuthority == "" || len(out.CertificateAuthority) > wire.MaxGatewayAuthorityBytes || out.NotAfter.IsZero() {
		return wire.GatewayActivated{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
