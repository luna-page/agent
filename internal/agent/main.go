package agent

import (
	"fmt"
	"os"

	"github.com/luna-page/agent/internal/install"
)

var buildVersion = "dev"
var isDevBuild = buildVersion == "dev"
var logLevel = os.Getenv("LOG_LEVEL")

var logDebug = func() bool {
	return logLevel == "debug" || (isDevBuild && logLevel == "")
}()

func Main() int {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(buildVersion)
		return 0
	}

	options, err := parseCliOptions()
	if err != nil {
		fmt.Println(err)
		return 1
	}

	switch options.intent {
	case cliIntentServe:
		config, err := loadConfig(options.configPath)
		if err != nil {
			fmt.Println(err)
			return 1
		}

		if err := serve(config); err != nil {
			fmt.Println(err)
			return 1
		}
		return 0
	case cliIntentPrintSensors:
		return cliSensorsPrint()
	case cliIntentInstall:
		if err := install.Init(); err != nil {
			return 1
		}
	}

	return 0
}
