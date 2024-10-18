package azure

import (
	"errors"
	"github.com/charmbracelet/log"
	"ops/pkg/kubectl"
	"ops/pkg/utils"
	"os/exec"
	"strings"
)

func AKSLogin(clusterName string, resourceGroup string) {
	utils.CheckBinary("az")

	utils.GetEnvironment("AZURE_SUBSCRIPTION_ID")

	if kubectl.CredentialsRequired(clusterName) {
		aksUpdateKubeConfig(clusterName, resourceGroup)
	}

}

func aksUpdateKubeConfig(clusterName string, resourceGroup string) {
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
