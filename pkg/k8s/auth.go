package k8s

import (
	"errors"
	"os/exec"
	"strings"

	"charm.land/log/v2"
)

func CredentialsRequired(clusterName string) bool {
	clusters := GetContexts()

	for _, context := range clusters {
		// We split the string by "/" to get the cluster name in case the
		// context uses a different format.
		parts := strings.Split(context, "/")
		if strings.Contains(parts[len(parts)-1], clusterName) {
			log.Warn(
				"An entry in the kube-config was found. Skipping authentication!",
				"clusterName",
				clusterName,
			)
			log.Info("Set current context to selected cluster...")
			SetConfig(clusterName)

			return false
		}
	}

	return true
}

func UpdateConfigForAKS(clusterName string, resourceGroup string) {
	cmd := []string{"az", "aks", "get-credentials", "--resource-group", resourceGroup, "--name", clusterName}

	log.Info(
		"Executing command:",
		"command",
		strings.Join(cmd, " "),
	)

	aksCredentials := exec.Command(cmd[0], cmd[1:]...)
	_, err := aksCredentials.Output()

	if err != nil {
		var execError *exec.Error
		if errors.As(err, &execError) {
			log.Fatal("Command execution failed", "name", execError.Name, "err", execError.Err)
		}
		log.Fatal("Failed to get Azure Kubernetes Service credentials", "err", err)
	}

	log.Info("Azure Kubernetes Service credentials added")
}
