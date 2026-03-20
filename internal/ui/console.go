package ui

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
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

type SelectOption struct {
	Label string
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

func (c *Console) Select(prompt string, options []SelectOption, defaultIndex int) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("no select options provided")
	}
	if defaultIndex < 0 || defaultIndex >= len(options) {
		defaultIndex = 0
	}
	for i, option := range options {
		if _, err := fmt.Fprintf(c.out, "  %d) %s\n", i+1, option.Label); err != nil {
			return 0, err
		}
	}
	value, err := c.Ask(fmt.Sprintf("%s [%d]: ", prompt, defaultIndex+1))
	if err != nil {
		return 0, err
	}
	if value == "" {
		return defaultIndex, nil
	}
	if index, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		index--
		if index >= 0 && index < len(options) {
			return index, nil
		}
	}
	return 0, fmt.Errorf("invalid selection %q", value)
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
