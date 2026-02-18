package agent

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/shirou/gopsutil/v4/sensors"
)

type cliIntent uint8

const (
	cliIntentServe        cliIntent = iota
	cliIntentInstall                = iota
	cliIntentPrintSensors           = iota
)

type cliOptions struct {
	intent     cliIntent
	configPath string
}

func parseCliOptions() (*cliOptions, error) {
	flags := flag.NewFlagSet("", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Println("Usage: agent [options] command")

		fmt.Println("\nOptions:")
		flags.PrintDefaults()

		fmt.Println("\nCommands:")
		fmt.Println("  install        Install the agent as a systemd service")
		fmt.Println("  sensors:print  List all sensors")
	}
	configPath := flags.String("config", "agent.yml", "Set config path")
	err := flags.Parse(os.Args[1:])
	if err != nil {
		return nil, err
	}

	var intent cliIntent
	var args = flags.Args()
	unknownCommandErr := fmt.Errorf("unknown command: %s", strings.Join(args, " "))

	if len(args) == 0 {
		intent = cliIntentServe
	} else if len(args) == 1 {
		switch args[0] {
		case "install":
			intent = cliIntentInstall
		case "sensors:print":
			intent = cliIntentPrintSensors
		default:
			return nil, unknownCommandErr
		}
	} else {
		return nil, unknownCommandErr
	}

	return &cliOptions{
		intent:     intent,
		configPath: *configPath,
	}, nil
}

func cliSensorsPrint() int {
	tempSensors, err := sensors.SensorsTemperatures()
	if err != nil {
		if warns, ok := err.(*sensors.Warnings); ok {
			fmt.Printf("Could not retrieve information for some sensors (%v):\n", err)
			for _, w := range warns.List {
				fmt.Printf(" - %v\n", w)
			}
			fmt.Println()
		} else {
			fmt.Printf("Failed to retrieve sensor information: %v\n", err)
			return 1
		}
	}

	if len(tempSensors) == 0 {
		fmt.Println("No sensors found")
		return 0
	}

	fmt.Println("Sensors found:")
	for _, sensor := range tempSensors {
		fmt.Printf(" %s: %.1f°C\n", sensor.SensorKey, sensor.Temperature)
	}

	return 0
}
