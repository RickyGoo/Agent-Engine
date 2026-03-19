//go:build !darwin

package secret

import (
	"context"
	"errors"
)

type KeychainStore struct{}

func (KeychainStore) Resolve(context.Context, Ref) (string, error) {
	return "", errors.New("keychain storage is only supported on darwin")
}

func (KeychainStore) Save(context.Context, Ref, string) error {
	return errors.New("keychain storage is only supported on darwin")
}
