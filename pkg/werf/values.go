package werf

import (
	"github.com/charmbracelet/log"
	"ops/pkg/config"
	"os"
	"path/filepath"
)

func GetValuesPaths() []string {
	var valuesPaths []string
	values := config.NewWerfConfig()

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
