package werf

import (
	"ops/pkg/config"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
)

func GetValuesFiles() []string {
	var valuesFiles []string
	values := config.LoadConfig().Werf

	for _, file := range values.ValuesFiles {

		if _, err := os.Stat(file); err != nil {
			log.Fatal(
				"Werf values file does not exist!",
				"file",
				file,
			)
		}

		valuesFiles = append(valuesFiles, "--values")
		valuesFiles = append(valuesFiles, file)
	}

	return valuesFiles
}

func GetSecretValuesFiles() []string {
	var valuesFiles []string
	values := config.LoadConfig().Werf

	for _, file := range values.ValuesFiles {

		if _, err := os.Stat(file); err != nil {
			log.Fatal(
				"Werf secret values file does not exist!",
				"file",
				file,
			)
		}

		valuesFiles = append(valuesFiles, "--secret-values")
		valuesFiles = append(valuesFiles, file)
	}

	return valuesFiles
}

func GetValuesPaths() []string {
	var valuesPaths []string
	values := config.LoadConfig().Werf

	for _, path := range values.ValuesPaths {
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Info(
					"Could not find values files here!",
					"path",
					path,
				)
				return nil
			}

			if !info.IsDir() {
				valuesPaths = append(valuesPaths, "--values")
				valuesPaths = append(valuesPaths, path)
			}

			return nil
		})

		if err != nil {
			log.Fatal("Could not find values files!")
		}
	}

	return valuesPaths
}

func GetSecretValuesPaths() []string {
	var valuesPaths []string
	values := config.LoadConfig().Werf

	for _, path := range values.ValuesPaths {
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Info(
					"Could not find secret values files here!",
					"path",
					path,
				)
				return nil
			}

			if !info.IsDir() {
				valuesPaths = append(valuesPaths, "--secret-values")
				valuesPaths = append(valuesPaths, path)
			}

			return nil
		})

		if err != nil {
			log.Fatal("Could not find secret values files!")
		}
	}

	return valuesPaths
}
