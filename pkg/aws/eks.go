package aws

import (
	"errors"
	"github.com/charmbracelet/log"
	"ops/pkg/kubectl"
	"ops/pkg/utils"
	"os/exec"
	"strings"
)

type RequiresAuth bool

func EKSLogin(clusterName string) {
	utils.CheckBinary("kubectl")

	utils.GetEnvironment("AWS_PROFILE")
	awsRegion := utils.GetEnvironment("AWS_REGION")

	if kubectl.CredentialsRequired(clusterName) {
		eksUpdateKubeConfig(
			awsRegion,
			clusterName,
		)
	}
}

func eksUpdateKubeConfig(awsRegion string, clusterName string) {
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
