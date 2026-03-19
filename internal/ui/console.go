package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type Console struct {
	in  *bufio.Reader
	out io.Writer
	err io.Writer
}

func NewConsole(in io.Reader, out, err io.Writer) *Console {
	return &Console{
		in:  bufio.NewReader(in),
		out: out,
		err: err,
	}
}

func (c *Console) Ask(prompt string) (string, error) {
	if _, err := fmt.Fprint(c.out, prompt); err != nil {
		return "", err
	}
	line, err := c.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (c *Console) AskDefault(prompt, defaultValue string) (string, error) {
	value, err := c.Ask(fmt.Sprintf("%s [%s]: ", prompt, defaultValue))
	if err != nil {
		return "", err
	}
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func (c *Console) Confirm(prompt string, defaultValue bool) (bool, error) {
	def := "y"
	if !defaultValue {
		def = "n"
	}
	value, err := c.Ask(fmt.Sprintf("%s [%s]: ", prompt, def))
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", def:
		return defaultValue, nil
	case "y", "yes", "true":
		return true, nil
	case "n", "no", "false":
		return false, nil
	default:
		return defaultValue, nil
	}
}

func (c *Console) Println(args ...any) {
	_, _ = fmt.Fprintln(c.out, args...)
}

func (c *Console) Errorln(args ...any) {
	_, _ = fmt.Fprintln(c.err, args...)
}
