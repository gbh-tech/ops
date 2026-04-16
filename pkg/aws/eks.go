package aws

import (
	"charm.land/log/v2"
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
		log.Fatal("Failed to get AWS EKS credentials", "err", err, "output", string(output))
	}

	log.Info("AWS EKS credentials added")
}
