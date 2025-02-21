package aws

import (
	"errors"
	"ops/pkg/utils"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

type RequiresLogin bool
type ECRAuthorizationData struct {
	AuthorizationData []struct {
		ProxyEndpoint      string    `json:"proxyEndpoint"`
		AuthorizationToken string    `json:"authorizationToken"`
		ExpiresAt          time.Time `json:"expiresAt"`
	} `json:"authorizationData"`
}

func ECRLogin(registryUrl string) {
	utils.CheckBinary("aws")

	utils.GetEnvironment("AWS_PROFILE")
	awsRegion := utils.GetEnvironment("AWS_REGION")

	dockerRegistryLogin(awsRegion, registryUrl)
}

func dockerRegistryLogin(awsRegion string, registryURL string) {
	ecrLoginPassword := ecrGetLoginPassword(awsRegion)

	log.Info(
		"Executing command:",
		"command",
		"docker login [omitted sensitive params]",
	)

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
		log.Info("Login command output: %v", output)
		log.Fatalf("Failed to login to Docker with ECR credentials: %v", err)
	}

	log.Infof("Docker Login successful!")
}

func ecrGetLoginPassword(awsRegion string) string {
	cmd := []string{"aws", "ecr", "get-login-password", "--region", awsRegion}
	log.Info(
		"Executing command:",
		"command",
		strings.Join(cmd, " "),
	)

	loginPassword := exec.Command(cmd[0], cmd[1:]...)

	password, err := loginPassword.Output()

	if err != nil {
		var execError *exec.Error
		if errors.As(err, &execError) {
			log.Fatalf(
				"Command execution failed: %v %v",
				execError.Name,
				execError.Err,
			)
		}
		log.Fatalf("Failed to get AWS ECR login password: %v", err)
	}

	log.Infof("AWS ECR login password colleted!")
	return string(password)
}
