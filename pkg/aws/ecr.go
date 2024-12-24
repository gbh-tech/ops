package aws

import (
	"encoding/json"
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
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"authorizationData"`
}

func ECRLogin(registryUrl string) {
	utils.CheckBinary("aws")

	utils.GetEnvironment("AWS_PROFILE")
	awsRegion := utils.GetEnvironment("AWS_REGION")

	if ecrLoginCredentialsRequired() {
		dockerRegistryLogin(awsRegion, registryUrl)
	}
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

	err := registryLogin.Run()
	if err != nil {
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

func ecrLoginCredentialsRequired() RequiresLogin {
	cmd := exec.Command(
		"aws",
		"ecr",
		"get-authorization-token",
		"--no-cli-pager",
	)

	output, err := cmd.Output()
	if err != nil {
		log.Fatalf("Failed to execute command: %v", err)
	}

	var authData ECRAuthorizationData
	err = json.Unmarshal(output, &authData)
	if err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	expiration := authData.AuthorizationData[0].ExpiresAt

	if len(authData.AuthorizationData) > 0 {
		log.Warn(
			"ECR login credentials are still valid.",
			"expiresAt",
			expiration.Format(time.RFC3339),
		)
		return RequiresLogin(false)
	} else {
		log.Info(
			"Authorization data expired or not found.",
			"expiredAt",
			expiration.Format(time.RFC3339),
		)
		return RequiresLogin(true)
	}
}
