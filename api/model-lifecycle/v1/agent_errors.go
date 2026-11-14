package v1

import (
	_ "embed"
	"encoding/json"
)

//go:embed agent-errors.json
var agentErrorJSON []byte
var agentErrorCodes = func() map[string]bool {
	var values []string
	if json.Unmarshal(agentErrorJSON, &values) != nil {
		panic("INVALID_MODEL_PREPARATION_AGENT_ERROR_CONTRACT")
	}
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}()

func ValidAgentError(code string) bool { return agentErrorCodes[code] }
