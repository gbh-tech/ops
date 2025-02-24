package aws

import (
	"github.com/charmbracelet/log"
	"ops/pkg/k8s"
	"os/exec"
	"strings"
)

func EKSLogin(clusterName string, awsRegion string) {
	if k8s.CredentialsRequired(clusterName) {
		updateConfigForEKS(awsRegion, clusterName)
	}
}

func updateConfigForEKS(awsRegion string, clusterName string) {
	cmd := exec.Command(
		"aws",
		"eks",
		"update-kubeconfig",
		"--region",
		awsRegion,
		"--name",
		clusterName,
	)

	log.Info(
		"Executing command:",
		"command",
		strings.Join(cmd.Args, " "),
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		log.Fatalf("Failed to get AWS EKS credentials: %v\nOutput: %s", err, output)
	}

	log.Infof("AWS EKS credentials added!")
}
