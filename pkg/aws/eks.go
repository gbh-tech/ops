package aws

import (
	"errors"
	"github.com/charmbracelet/log"
	"ops/pkg/config"
	"ops/pkg/utils"
	"os"
	"os/exec"
	"strings"
)

type RequiresAuth bool

func EKSLogin(clusterName string) {
	utils.CheckBinary("kubectl")

	utils.GetEnvironment("AWS_PROFILE")
	awsRegion := os.Getenv("AWS_REGION")

	if eksCredentialsRequired(clusterName) {
		eksUpdateKubeConfig(
			awsRegion,
			config.NewConfig().ClusterName,
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

func eksCredentialsRequired(clusterName string) RequiresAuth {
	cmd := []string{"kubectl", "config", "get-contexts", "-o", "name"}
	log.Info(
		"Executing command:",
		"command",
		strings.Join(cmd, " "),
	)

	availableClusters := exec.Command(cmd[0], cmd[1:]...)
	clusters, err := availableClusters.Output()

	if err != nil {
		var execError *exec.Error
		if errors.As(err, &execError) {
			log.Fatalf(
				"Command execution failed: %v %v",
				execError.Name,
				execError.Err,
			)
		}
		log.Fatalf("Failed to get available clusters: %v", err)
	}

	log.Infof("Local authenticated K8s clusters colleted!")

	contexts := strings.Split(string(clusters), "\n")
	for _, context := range contexts {
		if strings.Contains(context, clusterName) {
			log.Warn(
				"An entry in the kube-config was found. Skipping authentication!",
				"cluster",
				clusterName,
			)
			return false
		}
	}

	return true
}
