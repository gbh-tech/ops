package kubectl

import (
	"errors"
	"github.com/charmbracelet/log"
	"os/exec"
	"strings"
)

type RequiresAuth bool

func CredentialsRequired(clusterName string) RequiresAuth {
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
