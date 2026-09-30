package webhook

import "github.com/dragovics/gpt-go-agent/internal/policy"

// validateNativeCommand is the webhook's deterministic second gate after the
// LLM plan. Webhook-native execution always uses strict policy.
func validateNativeCommand(command string, args []string, _ string) error {
	return policy.ValidateNativeCommand(command, args, policy.NativeStrict, nil)
}
