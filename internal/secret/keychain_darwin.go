//go:build darwin

package secret

import (
	"context"
	"fmt"
	"os/exec"
)

type KeychainStore struct{}

func (KeychainStore) Resolve(_ context.Context, ref Ref) (string, error) {
	if ref.Kind != "keychain" {
		return "", fmt.Errorf("keychain store cannot resolve kind %q", ref.Kind)
	}
	args := []string{"find-generic-password", "-s", ref.Name, "-w"}
	if ref.Account != "" {
		args = append([]string{"find-generic-password", "-s", ref.Name, "-a", ref.Account, "-w"})
	}
	out, err := exec.Command("security", args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (KeychainStore) Save(_ context.Context, ref Ref, value string) error {
	if ref.Kind != "keychain" {
		return fmt.Errorf("keychain store cannot save kind %q", ref.Kind)
	}
	args := []string{"add-generic-password", "-U", "-s", ref.Name, "-w", value}
	if ref.Account != "" {
		args = []string{"add-generic-password", "-U", "-s", ref.Name, "-a", ref.Account, "-w", value}
	}
	return exec.Command("security", args...).Run()
}
