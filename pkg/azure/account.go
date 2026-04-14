package azure

import (
	"charm.land/log/v2"
	"encoding/json"
	"os/exec"
)

type AccountInfo struct {
	SubscriptionId string `json:"id"`
	Name           string `json:"name"`
	State          string `json:"state"`
	TenantName     string `json:"tenantDisplayName"`
	TenantId       string `json:"tenantId"`
	User           struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"user"`
}

func SetAccountSubscription(subscriptionId string) {
	if CurrentAccount().SubscriptionId == subscriptionId {
		log.Info("Current account is already set to target subscription!")
		return
	}

	cmd := []string{"az", "account", "set", "--subscription", subscriptionId}

	accountSubscription := exec.Command(cmd[0], cmd[1:]...)
	_, err := accountSubscription.Output()

	if err != nil {
		log.Fatalf("Failed to set Azure account subscription: %v", err)
	}

	log.Info("Azure accounted set!", "subscription", subscriptionId)
}

func CurrentAccount() AccountInfo {
	cmd := []string{"az", "account", "show", "--output", "json"}

	accountInfo := exec.Command(cmd[0], cmd[1:]...)
	output, err := accountInfo.Output()

	if err != nil {
		log.Fatalf("Failed to get Azure account information: %v", err)
	}

	var account AccountInfo

	err = json.Unmarshal(output, &account)
	if err != nil {
		log.Fatalf("Failed to parse Azure account information: %v", err)
	}

	log.Info(
		"Azure account information:",
		"subscription",
		account.SubscriptionId,
		"accountName",
		account.Name,
		"tenantName",
		account.TenantName,
	)

	return account
}
