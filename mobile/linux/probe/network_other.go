//go:build !linux && !windows && !darwin

package probe

import "errors"

type network struct{ fingerprint string }

func currentNetwork() (*network, error)         { return nil, errors.New("Linux network is required") }
func (*network) snapshot() (string, error)      { return "", errors.New("unsupported platform") }
func (*network) PrepareSocket(int) error        { return errors.New("unsupported platform") }
func (*network) Resolve(string) (string, error) { return "", errors.New("unsupported platform") }
