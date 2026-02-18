package terminal

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	FgYellow      = "33"
	FgRed         = "31"
	FgGreen       = "32"
	FgCyan        = "36"
	FgBrightBlack = "90"
)

var useStyles = func() bool {
	_, noColorPresent := os.LookupEnv("NO_COLOR")

	return !noColorPresent && os.Getenv("TERM") != "dumb"
}()

func PrintStyled(text string, options ...string) {
	fmt.Print(Styled(text, options...))
}

func PrintlnStyled(text string, options ...string) {
	fmt.Println(Styled(text, options...))
}

func Styled(text string, options ...string) string {
	if useStyles {
		return "\x1b[" + strings.Join(options, ";") + "m" + text + "\x1b[0m"
	}

	return text
}

func Styledf(text string, options ...string) func(...any) string {
	return func(args ...any) string {
		return Styled(fmt.Sprintf(text, args...), options...)
	}
}

var escapeCodePattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func StripStyle(s string) string {
	return escapeCodePattern.ReplaceAllString(s, "")
}
