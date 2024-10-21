package werf

import (
	"github.com/charmbracelet/log"
	"os"
	"path/filepath"
)

func GetValuesPaths() []string {
	var valuesPaths []string

	for _, path := range ValuesPaths {
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
				valuesPaths = append(valuesPaths, "--values="+path)
			}

			return nil
		})

		if err != nil {
			log.Fatal("Could not find values files!")
		}
	}

	return valuesPaths
}
