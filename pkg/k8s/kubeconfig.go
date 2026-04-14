package k8s

import (
	"strings"

	"charm.land/log/v2"
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

func SetConfig(clusterName string) {
	config := GetConfig()
	contexts := GetContexts()

	for _, context := range contexts {
		if strings.Contains(context, clusterName) {
			config.CurrentContext = context
		}
	}

	if config.CurrentContext == "" {
		log.Fatal("Context does not exist in kube config!", "context", clusterName)
	}

	kubeConfigPath := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	err := clientcmd.WriteToFile(*config, kubeConfigPath)

	if err != nil {
		log.Fatalf("Failed to write kube config: %v", err)
	}

	log.Info("Context successfully set to selected cluster!", "context", clusterName)
}

func GetConfig() *api.Config {
	kubeConfigPath := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.LoadFromFile(kubeConfigPath)

	if err != nil {
		log.Fatalf("Failed to load kube config: %v", err)
	}

	return config
}
