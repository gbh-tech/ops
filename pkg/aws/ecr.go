package aws

import (
	"errors"
	"ops/pkg/utils"
	"os/exec"
	"strings"

	"charm.land/log/v2"
)

func ECRLogin(registryUrl string, awsRegion string) {
	utils.CheckBinary("aws")
	utils.CheckBinary("docker")
	dockerRegistryLogin(awsRegion, registryUrl)
}

func dockerRegistryLogin(awsRegion string, registryURL string) {
	ecrLoginPassword := ecrGetLoginPassword(awsRegion)

	log.Info("Executing command", "command", "docker login [omitted sensitive params]")

	registryLogin := exec.Command(
		"docker",
		"login",
		"--username",
		"AWS",
		"--password",
		ecrLoginPassword,
		registryURL,
	)

	output, err := registryLogin.Output()
	if err != nil {
		log.Info("Login command output", "output", string(output))
		log.Fatal("Failed to login to Docker with ECR credentials", "err", err)
	}

	log.Info("Docker login successful")
}

func ecrGetLoginPassword(awsRegion string) string {
	cmd := []string{"aws", "ecr", "get-login-password", "--region", awsRegion}
	log.Info("Executing command", "command", strings.Join(cmd, " "))

	loginPassword := exec.Command(cmd[0], cmd[1:]...)

	password, err := loginPassword.Output()
	if err != nil {
		var execError *exec.Error
		if errors.As(err, &execError) {
			log.Fatal("Command execution failed", "name", execError.Name, "err", execError.Err)
		}
		log.Fatal("Failed to get AWS ECR login password", "err", err)
	}

	log.Info("AWS ECR login password collected")
	return string(password)
}
