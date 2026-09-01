package internal

import (
	"context"
	"io"

	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
)

type meshStorage interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	List(ctx context.Context, prefix string) ([]*storagev1.StatResponse, error)
}

func (m *Module) meshStorage() meshStorage {
	if m.storageTestOverride != nil {
		return m.storageTestOverride
	}
	if m.mc == nil {
		return nil
	}
	return m.mc.Storage
}
