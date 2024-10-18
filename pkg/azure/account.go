package azure

import (
	"encoding/json"
	"github.com/charmbracelet/log"
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
