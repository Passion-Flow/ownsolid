package engine

import "context"

// These are the named slots of the existing cumulative locale-volume contract,
// not caller-selected host paths or a second localization configuration owner.
const LocalizationConfigurationRole = "localization-config"
const LocalizationTaskDestination = "/var/lib/projectone/locales"

func (c *Client) databaseLocalizationConsumers(ctx context.Context, t DatabaseTask, id string) error {
	if t.LocalizationOwner == nil {
		return nil
	}
	allowed := []string{t.Peers["control"]}
	if id != "" {
		allowed = append(allowed, id)
	}
	return c.VerifyVolumeConsumers(ctx, *t.LocalizationOwner, allowed)
}
