package k8s

import (
	"github.com/charmbracelet/log"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

func GetCurrentContext() string {
	config := GetConfig()
	return config.CurrentContext
}

func GetContexts() []string {
	config := GetConfig()
	var contexts []string

	for cluster := range config.Contexts {
		contexts = append(contexts, cluster)
	}

	return contexts
}

func GetConfig() *api.Config {
	kubeConfigPath := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.LoadFromFile(kubeConfigPath)

	if err != nil {
		log.Fatalf("Failed to load kube config: %v", err)
	}

	return config
}
