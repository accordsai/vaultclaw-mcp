//go:build !darwin && !linux

package catalog

type fileLock struct{}

func acquireFileLock(_ string) (*fileLock, error) {
	return &fileLock{}, nil
}

func (l *fileLock) Unlock() error {
	_ = l
	return nil
}
