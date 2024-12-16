package k8s

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/charmbracelet/log"
)

func CredentialsRequired(clusterName string) bool {
	clusters := GetContexts()

	for _, context := range clusters {
		if strings.Contains(context, clusterName) {
			log.Warn(
				"An entry in the kube-config was found. Skipping authentication!",
				"clusterName",
				clusterName,
			)

			return false
		}
	}

	return true
}

func UpdateConfigForEKS(awsRegion string, clusterName string) {
	cmd := []string{"aws", "eks", "update-kubeconfig", "--region", awsRegion, "--name", clusterName}

	log.Info(
		"Executing command:",
		"command",
		strings.Join(cmd, " "),
	)

	eksCredentials := exec.Command(cmd[0], cmd[1:]...)
	_, err := eksCredentials.Output()

	if err != nil {
		var execError *exec.Error
		if errors.As(err, &execError) {
			log.Fatalf(
				"Command execution failed: %v %v",
				execError.Name,
				execError.Err,
			)
		}
		log.Fatalf("Failed to get AWS EKS credentials: %v", err)
	}

	log.Infof("AWS EKS credentials added!")
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
			log.Fatalf(
				"Command execution failed: %v %v",
				execError.Name,
				execError.Err,
			)
		}
		log.Fatalf("Failed to get Azure Kubernetes Service credentials: %v", err)
	}

	log.Infof("Azure Kubernetes Service credentials added!")
}
