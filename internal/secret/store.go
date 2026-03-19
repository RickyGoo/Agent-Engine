package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
)

type Ref struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Account string `json:"account,omitempty"`
}

type Store interface {
	Resolve(ctx context.Context, ref Ref) (string, error)
	Save(ctx context.Context, ref Ref, value string) error
}

type CompositeStore struct {
	Env      EnvStore
	Keychain KeychainStore
}

func (s CompositeStore) Resolve(ctx context.Context, ref Ref) (string, error) {
	switch ref.Kind {
	case "env":
		return s.Env.Resolve(ctx, ref)
	case "keychain":
		return s.Keychain.Resolve(ctx, ref)
	default:
		return "", fmt.Errorf("unknown secret ref kind %q", ref.Kind)
	}
}

func (s CompositeStore) Save(ctx context.Context, ref Ref, value string) error {
	switch ref.Kind {
	case "env":
		return s.Env.Save(ctx, ref, value)
	case "keychain":
		return s.Keychain.Save(ctx, ref, value)
	default:
		return fmt.Errorf("unknown secret ref kind %q", ref.Kind)
	}
}

type EnvStore struct{}

func (EnvStore) Resolve(_ context.Context, ref Ref) (string, error) {
	if ref.Kind != "env" {
		return "", fmt.Errorf("env store cannot resolve kind %q", ref.Kind)
	}
	if ref.Name == "" {
		return "", errors.New("env ref missing name")
	}
	value := os.Getenv(ref.Name)
	if value == "" {
		return "", fmt.Errorf("environment variable %s is empty", ref.Name)
	}
	return value, nil
}

func (EnvStore) Save(_ context.Context, ref Ref, value string) error {
	if ref.Kind != "env" {
		return fmt.Errorf("env store cannot save kind %q", ref.Kind)
	}
	if ref.Name == "" {
		return errors.New("env ref missing name")
	}
	return os.Setenv(ref.Name, value)
}
