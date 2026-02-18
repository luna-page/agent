package install

import (
	"bytes"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	trm "github.com/luna-page/agent/internal/terminal"
	"github.com/shirou/gopsutil/v4/disk"
)

//go:embed templates
var templatesFS embed.FS

var defaultHiddenMountpoints = map[string]struct{}{
	"/boot":        {},
	"/boot/efi":    {},
	"/var/hdd.log": {}, // from log2ram
}

type installOptions struct {
	InstallDirectory    string
	BinaryPath          string
	ConfigPath          string
	ServicePath         string
	ServiceName         string
	UninstallScriptPath string
	// UpdateScriptPath      string
	LocalAddress          string
	Hostname              string
	AuthToken             string
	Port                  uint16
	HiddenMountpoints     []string
	AddFirewallRule       bool
	EnableAndRunService   bool
	RandomAuthToken       bool
	UsingCustomConfigPath bool
}

type editableInstallOptions []func(*installOptions) (title string, value string, handleChange func() error)

func (e *editableInstallOptions) handleUserInputLoop(options *installOptions) bool {
	for {
		fmt.Println()
		for i := range *e {
			key, value, _ := (*e)[i](options)
			fmt.Println(styledInputOption(strconv.Itoa(i+1)), styledKeyValue(key, value))
		}

		input := takeUserInput(
			fmt.Sprintf(
				"You can begin the installation with the above options by typing %s,\nedit any of the above options by entering their number, or quit the installer by typing %s",
				styledInputOption("install"),
				styledInputOption("quit"),
			),
		)

		if input == "" {
			// Ctrl+C sends an empty string but the process doesn't exit immediately and starts
			// printing the prompt again, then exits halfway through the prompt. That's jank, so
			// we sleep for a bit which allows it to exit without printing anything.
			time.Sleep(100 * time.Millisecond)
			continue
		}

		input = strings.ToLower(input)

		if len(input) >= 3 && input[0] == '[' && input[len(input)-1] == ']' {
			input = input[1 : len(input)-1]
			trm.PrintlnStyled("\nTIP: The square brackets are decorative, you don't have to type them, just the text inside them", trm.FgBrightBlack)
		}

		if input == "install" || input == "i" {
			return true
		}

		if input == "quit" || input == "q" {
			return false
		}

		index, err := strconv.Atoi(input)
		if err != nil || index < 1 || index > len(*e) {
			trm.PrintlnStyled("\nInvalid option", trm.FgRed)
			continue
		}

		_, _, handler := (*e)[index-1](options)
		if handler == nil {
			continue
		}

		for {
			err = handler()
			if err != nil {
				trm.PrintlnStyled("\nError editing option: "+err.Error(), trm.FgRed)
				continue
			}

			break
		}
	}
}

func Init() (err error) {
	defer func() {
		if err != nil {
			trm.PrintlnStyled("\nInstallation error: "+err.Error(), trm.FgRed)
		}
	}()

	fmt.Println()

	if runtime.GOOS != "linux" {
		return errors.New("automatic installation is only supported on Linux systems")
	}

	if !isUsingSystemd() {
		return errors.New("automatic installation is only supported on systems running systemd")
	}

	if isRunningInsideDockerContainer() {
		return errors.New("automatic installation is not possible inside Docker containers")
	}

	currentPathOfBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting current binary path: %v", err)
	}

	var serviceTemplate = mustParseTemplate("luna-agent.service")
	var configTemplate = mustParseTemplate("agent.yml")
	var configEntryTemplate = mustParseTemplate("luna-entry.yml")
	// var updateScriptTemplate = mustParseTemplate("update.sh")
	var uninstallScriptTemplate = mustParseTemplate("uninstall.sh")

	installOptionHandlers := editableInstallOptions{
		func(o *installOptions) (string, string, func() error) {
			return "Install at directory", o.InstallDirectory, func() error {
				newPath := takeUserInput("Enter a new installation path or leave blank to go back without making changes")
				if newPath == "" {
					return nil
				}

				absPath, err := resolvePath(newPath)
				if err != nil {
					return fmt.Errorf("invalid path: %v", err)
				}
				if fileExists(absPath) {
					return fmt.Errorf("a file already exists at %s", absPath)
				}

				o.InstallDirectory = absPath
				return nil
			}
		},
		func(o *installOptions) (string, string, func() error) {
			var configPath string

			if o.UsingCustomConfigPath {
				configPath = o.ConfigPath
			} else {
				configPath = filepath.Join(o.InstallDirectory, "agent.yml")
			}

			return "Create configuration file at", configPath, func() error {
				newPath := takeUserInput("Enter a new path for the config or leave blank to go back without making changes")
				if newPath == "" {
					return nil
				}

				absPath, err := resolvePath(newPath)
				if err != nil {
					return fmt.Errorf("invalid path: %v", err)
				}
				if fileExists(absPath) {
					return fmt.Errorf("a file already exists at %s", absPath)
				}

				o.UsingCustomConfigPath = true
				o.ConfigPath = absPath
				return nil
			}
		},
		func(o *installOptions) (string, string, func() error) {
			return "Create service file at", o.ServicePath, func() error {
				newPath := takeUserInput("Enter a new path for the service or leave blank to go back without making changes")
				if newPath == "" {
					return nil
				}

				absPath, err := resolvePath(newPath)
				if err != nil {
					return fmt.Errorf("invalid path: %v", err)
				}
				if fileExists(absPath) {
					return fmt.Errorf("a file already exists at %s", absPath)
				}

				o.ServicePath = absPath
				return nil
			}
		},
		func(o *installOptions) (string, string, func() error) {
			return "Port to listen on", strconv.Itoa(int(o.Port)), func() error {
				portStr := takeUserInput("Enter a new port number or leave blank to use the current value")
				if portStr == "" {
					return nil
				}

				port, err := strconv.Atoi(portStr)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("invalid port number: %s", portStr)
				}

				o.Port = uint16(port)
				return nil
			}
		},
		func(o *installOptions) (string, string, func() error) {
			return "Use a randomly generated authentication token", ternary(o.RandomAuthToken, "Yes", "No"), func() error {
				input := takeUserInput(fmt.Sprintf(
					"Enter %s to use a random token, %s to remove authentication or leave blank to go back without making changes",
					styledInputOption("yes"),
					styledInputOption("no"),
				))
				if input == "" {
					return nil
				}

				o.RandomAuthToken = stringToBool(input)
				return nil
			}
		},
	}

	hasUFW := false
	if _, err := exec.LookPath("ufw"); err == nil {
		hasUFW = true
	}

	if hasUFW {
		installOptionHandlers = append(installOptionHandlers, func(o *installOptions) (string, string, func() error) {
			message := "Run " + trm.Styledf("ufw allow %d/tcp", trm.FgCyan)(o.Port)
			return message, ternary(o.AddFirewallRule, "Yes", "No"), func() error {
				input := takeUserInput(fmt.Sprintf(
					"Enter %s to add a firewall rule for the port, %s to skip this step or leave blank to go back without making changes",
					styledInputOption("yes"),
					styledInputOption("no"),
				))
				if input == "" {
					return nil
				}

				o.AddFirewallRule = stringToBool(input)
				return nil
			}
		})
	}

	installOptionHandlers = append(installOptionHandlers, func(o *installOptions) (string, string, func() error) {
		serviceName := strings.TrimSuffix(filepath.Base(o.ServicePath), ".service")
		message := "Run " + trm.Styled("systemctl enable --now "+serviceName, trm.FgCyan)
		return message, ternary(o.EnableAndRunService, "Yes", "No"), func() error {
			input := takeUserInput(fmt.Sprintf(
				"Enter %s to enable and start the service, %s to skip this step or leave blank to go back without making changes",
				styledInputOption("yes"),
				styledInputOption("no"),
			))
			if input == "" {
				return nil
			}

			o.EnableAndRunService = stringToBool(input)
			return nil
		}
	})

	options := installOptions{
		InstallDirectory:    "/opt/luna-agent",
		ServicePath:         "/etc/systemd/system/luna-agent.service",
		Port:                27973,
		RandomAuthToken:     true,
		EnableAndRunService: true,
	}

	diskPartitions, err := disk.Partitions(false)
	if err == nil {
		for _, partition := range diskPartitions {
			if _, ok := defaultHiddenMountpoints[partition.Mountpoint]; ok {
				options.HiddenMountpoints = append(options.HiddenMountpoints, partition.Mountpoint)
			}
		}
	}

	if hasUFW {
		options.AddFirewallRule = true
	}

	localAddress, err := getLocalAddress()
	if err == nil {
		options.LocalAddress = localAddress
	} else {
		options.LocalAddress = "<insert IP address or domain of this server>"
	}

	hostname, err := os.Hostname()
	if err == nil {
		options.Hostname = hostname
	} else {
		options.Hostname = "unnamed server"
	}

	fmt.Println("Installation of the luna Agent will use the following default options:")

	if !installOptionHandlers.handleUserInputLoop(&options) {
		trm.PrintlnStyled("\nInstallation cancelled", trm.FgRed)
		return nil
	}

	if options.RandomAuthToken {
		options.AuthToken = makeRandomString(32)
	}

	if !options.UsingCustomConfigPath {
		options.ConfigPath = filepath.Join(options.InstallDirectory, "agent.yml")
	}

	options.BinaryPath = filepath.Join(options.InstallDirectory, "agent")
	options.UninstallScriptPath = filepath.Join(options.InstallDirectory, "uninstall.sh")
	// options.UpdateScriptPath = filepath.Join(options.InstallDirectory, "update.sh")
	options.ServiceName = filepath.Base(options.ServicePath)

	fmt.Println()

	var serviceFileContents []byte
	var configFileContents []byte
	var lunaConfigEntryContents []byte
	// var updateScriptFileContents []byte
	var uninstallScriptFileContents []byte

	if !doWithProgressIndicator("Generating file contents", func() (string, error, bool) {
		fail := func(err error) (string, error, bool) {
			return trm.Styled("FAILED", trm.FgRed), err, false
		}

		serviceFileContents, err = serviceTemplate(options)
		if err != nil {
			return fail(err)
		}

		configFileContents, err = configTemplate(options)
		if err != nil {
			return fail(err)
		}
		configFileContents = append(configFileContents, '\n')

		lunaConfigEntryContents, err = configEntryTemplate(options)
		if err != nil {
			return fail(err)
		}

		// updateScriptFileContents, err = updateScriptTemplate(options)
		// if err != nil {
		// 	return fail(err)
		// }

		uninstallScriptFileContents, err = uninstallScriptTemplate(options)
		if err != nil {
			return fail(err)
		}

		return "DONE", nil, true
	}) {
		return errors.New("cannot continue without generating file contents")
	}

	if !doWithProgressIndicator("Creating installation directory", func() (string, error, bool) {
		created, err := createPathIfNotExists(options.InstallDirectory, 0755)
		if err != nil {
			return trm.Styled("FAILED", trm.FgRed), err, false
		}
		if !created {
			return "SKIPPED (already exists)", nil, true
		}

		return "DONE", nil, true
	}) {
		return errors.New("cannot continue without creating installation directory")
	}

	if !doWithProgressIndicator("Creating installation files", func() (string, error, bool) {
		fail := func(err error) (string, error, bool) {
			return trm.Styled("FAILED", trm.FgRed), err, false
		}

		if err := createFileIfNotExists(options.ServicePath, serviceFileContents, 0644); err != nil {
			return fail(err)
		}

		if err := copyFileFromTo(currentPathOfBinary, options.BinaryPath, 0755); err != nil {
			return fail(err)
		}

		if err := createFileIfNotExists(options.ConfigPath, configFileContents, 0600); err != nil {
			return fail(err)
		}

		if err := createFileIfNotExists(options.UninstallScriptPath, uninstallScriptFileContents, 0744); err != nil {
			return fail(err)
		}

		// if err := createFileIfNotExists(options.UpdateScriptPath, updateScriptFileContents, 0744); err != nil {
		// 	return fail(err)
		// }

		return "DONE", nil, true
	}) {
		return errors.New("cannot continue without necessary installation files")
	}

	if hasUFW && options.AddFirewallRule {
		doWithProgressIndicator("Adding firewall rule", func() (string, error, bool) {
			_, stderr, err := runCommand("ufw", "allow", strconv.Itoa(int(options.Port))+"/tcp")
			if err != nil {
				return trm.Styled("FAILED ("+err.Error()+")", trm.FgRed), errors.New(stderr), false
			}

			return "DONE", nil, true
		})
	}

	if options.EnableAndRunService {
		serviceStarted := doWithProgressIndicator("Enabling and starting service", func() (string, error, bool) {
			_, stderr, err := runCommand("systemctl", "enable", "--now", options.ServiceName)
			if err != nil {
				return trm.Styled("FAILED ("+err.Error()+")", trm.FgRed), errors.New(stderr), false
			}

			return "DONE", nil, true
		})

		if serviceStarted {
			doWithProgressIndicator("Checking if agent is running", func() (string, error, bool) {
				time.Sleep(1 * time.Second)
				var lastErr error

				req, _ := http.NewRequest("GET", "http://localhost:"+strconv.Itoa(int(options.Port))+"/api/healthz", nil)
				if options.RandomAuthToken {
					req.Header.Set("Authorization", "Bearer "+options.AuthToken)
				}

				client := &http.Client{Timeout: 2 * time.Second}

				for range 3 {
					resp, err := client.Do(req)
					if err != nil {
						lastErr = err
						continue
					}
					resp.Body.Close()

					if resp.StatusCode == http.StatusOK {
						return "OK", nil, true
					} else {
						lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
					}
				}

				return trm.Styled("FAILED AFTER 3 TRIES", trm.FgRed), lastErr, false
			})
		}
	}

	doneMessage := "Installation completed"
	trm.PrintlnStyled("\n"+strings.Repeat("=", len(doneMessage)), trm.FgGreen)
	trm.PrintlnStyled(doneMessage, trm.FgGreen)
	trm.PrintlnStyled(strings.Repeat("=", len(doneMessage)), trm.FgGreen)

	time.Sleep(500 * time.Millisecond)

	if !hasUFW || !options.AddFirewallRule {
		trm.PrintStyled("\nNOTE: ", trm.FgRed)
		fmt.Printf("You may need to manually open port %d/tcp on this server if you have a firewall\n", options.Port)
	}

	// fmt.Println("\nTo update the agent run", trm.Styled("sudo "+options.UpdateScriptPath, trm.FgCyan))
	fmt.Println("\nTo uninstall the agent run", trm.Styled("sudo "+options.UninstallScriptPath, trm.FgCyan))

	fmt.Print("\nAdd the following entry to your servers list in luna.yml:\n\n")
	trm.PrintlnStyled(string(lunaConfigEntryContents), trm.FgCyan)

	fmt.Println()

	return nil
}

func runCommand(cmd string, args ...string) (string, string, error) {
	var stderr bytes.Buffer
	var stdout bytes.Buffer

	c := exec.Command(cmd, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()

	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func resolvePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return path, nil
	}

	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting user home directory: %v", err)
		}

		path = filepath.Join(home, path[1:])
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %v", path, err)
	}

	return abs, nil
}

func fileExists(path string) bool {
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		return false
	}

	return true
}

func createFileIfNotExists(path string, contents []byte, perms os.FileMode) error {
	if fileExists(path) {
		return nil
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perms)
	if err != nil {
		return fmt.Errorf("creating file %s: %v", path, err)
	}

	_, err = f.Write(contents)
	if err != nil {
		return fmt.Errorf("writing to file %s: %v", path, err)
	}

	return f.Close()
}

func createPathIfNotExists(path string, perms os.FileMode) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, perms)
		if err != nil {
			return false, fmt.Errorf("creating directory %s: %v", path, err)
		}

		return true, nil
	}

	return false, nil
}

func copyFileFromTo(src string, dest string, perms os.FileMode) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()

	w, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perms)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = w.ReadFrom(r)
	return err
}

func isUsingSystemd() bool {
	stat, err := os.Stat("/run/systemd/system")
	if err != nil {
		return false
	}

	return stat.IsDir()
}

func isRunningInsideDockerContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func takeUserInput(prompt string) string {
	fmt.Printf("\n%s\n%s ", prompt, trm.Styled(">", trm.FgBrightBlack))
	var input string
	fmt.Scanln(&input)
	return strings.TrimSpace(input)
}

func mustParseTemplate(name string) func(any) ([]byte, error) {
	tmpl, err := template.New(name).ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		panic(err)
	}

	return func(data any) ([]byte, error) {
		var buff bytes.Buffer

		err := tmpl.Execute(&buff, data)
		if err != nil {
			return nil, err
		}

		return []byte(strings.TrimSpace(buff.String())), nil
	}
}

func ternary[T any](b bool, trueValue, falseValue T) T {
	if b {
		return trueValue
	}

	return falseValue
}

func stringToBool(s string) bool {
	return s == "yes" || s == "y"
}

func getLocalAddress() (string, error) {
	addres, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addres {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}

	return "", errors.New("no suitable IP address found")
}

func makeRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	charsetLen := byte(len(charset))

	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	for i := range length {
		b[i] = charset[b[i]%charsetLen]
	}

	return string(b)
}

func doWithProgressIndicator(message string, action func() (outcomeText string, err error, proceed bool)) bool {
	printProgress := func(symbol string) {
		fmt.Print(
			"\r" +
				trm.Styled("[", trm.FgBrightBlack) +
				symbol +
				trm.Styled("]", trm.FgBrightBlack) +
				" " +
				styledKeyValue(message, ""),
		)
	}

	loaderCharIndex := 0
	loaderChars := [...]string{"-", "\\", "|", "/"}

	printProgress(loaderChars[loaderCharIndex])

	done := make(chan struct{})
	ticker := time.NewTicker(110 * time.Millisecond)

	var (
		outcome string
		err     error
		proceed bool
	)

	go func() {
		outcome, err, proceed = action()
		ticker.Stop()
		done <- struct{}{}
	}()

	for {
		select {
		case <-done:
			var outcomeSymbol string

			if err == nil {
				outcomeSymbol = trm.Styled("✔", trm.FgGreen)
			} else {
				outcomeSymbol = trm.Styled("✘", trm.FgRed)
			}

			printProgress(outcomeSymbol)
			fmt.Println(outcome)

			if err != nil {
				trm.PrintlnStyled(" └╴ "+err.Error(), trm.FgRed)
			}
			return proceed
		case <-ticker.C:
			printProgress(loaderChars[loaderCharIndex])
			loaderCharIndex = (loaderCharIndex + 1) % len(loaderChars)
		}
	}
}

func styledKeyValue(key, value string) string {
	paddingLength := max(55-len(trm.StripStyle(key)), 3)
	padding := trm.Styled(strings.Repeat(".", paddingLength), trm.FgBrightBlack)

	return key + " " + padding + " " + value
}

func styledInputOption(option string) string {
	return trm.Styled("[", trm.FgBrightBlack) +
		trm.Styled(option, trm.FgYellow) +
		trm.Styled("]", trm.FgBrightBlack)
}
